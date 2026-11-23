// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

var consumerSecretExpression = regexp.MustCompile(`^\$\{\{\s*secrets\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}$`)

// bindWorkflowSecrets traces known layer credentials back through local
// workflow calls. Callee secret names are scoped, not repository secret names.
// Unrelated credentials (notably parent pull-only tokens) are never inferred.
func (plan *ConsumerPlan) bindWorkflowSecrets(workflows, entryPoints map[string]string, facts map[string]string) error {
	binder := workflowSecretBinder{
		pin: plan.Pin, workflows: workflows, facts: facts,
		resolved: map[string]map[string]string{}, visiting: map[string]bool{},
	}

	for _, name := range sortedKeys(entryPoints) {
		bindings, err := binder.resolve(name)
		if err != nil {
			return err
		}

		for _, secret := range sortedKeys(bindings) {
			if err := bind(plan.Secrets, "secret", secret, bindings[secret]); err != nil {
				return err
			}
		}
	}

	return nil
}

// localWorkflowPrefix is how a job calls a workflow of its own repository.
const localWorkflowPrefix = "./.forgejo/workflows/"

// secretWorkflowJob is the part of a workflow job that forwards secrets.
type secretWorkflowJob struct {
	Uses    string    `yaml:"uses"`
	Secrets yaml.Node `yaml:"secrets"`
	Steps   []struct {
		Uses string    `yaml:"uses"`
		With yaml.Node `yaml:"with"`
		Env  yaml.Node `yaml:"env"`
	} `yaml:"steps"`
}

// workflowSecretBinder resolves each local workflow's required secrets once.
type workflowSecretBinder struct {
	pin       ConsumerPin
	workflows map[string]string
	facts     map[string]string
	resolved  map[string]map[string]string
	visiting  map[string]bool
}

func (b *workflowSecretBinder) resolve(name string) (map[string]string, error) {
	if found, ok := b.resolved[name]; ok {
		return found, nil
	}

	if b.visiting[name] {
		return nil, fmt.Errorf("local workflow cycle at %s: %w", name, errs.ErrValidation)
	}

	body, ok := b.workflows[name]
	if !ok {
		return nil, fmt.Errorf("local workflow %s is missing: %w", name, errs.ErrValidation)
	}

	b.visiting[name] = true
	defer delete(b.visiting, name)

	var workflow struct {
		Jobs map[string]secretWorkflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(body), &workflow); err != nil {
		return nil, fmt.Errorf("parse workflow %s: %w", name, err)
	}

	bindings := map[string]string{}

	for _, jobName := range slices.Sorted(maps.Keys(workflow.Jobs)) {
		if err := b.bindJob(bindings, workflow.Jobs[jobName], "workflow "+name+" job "+jobName); err != nil {
			return nil, err
		}
	}

	b.resolved[name] = bindings

	return bindings, nil
}

func (b *workflowSecretBinder) bindJob(bindings map[string]string, job secretWorkflowJob, where string) error {
	if err := b.bindLayerActions(bindings, job, where); err != nil {
		return err
	}

	local := strings.HasPrefix(job.Uses, localWorkflowPrefix)

	var required map[string]string

	switch {
	case local:
		var err error

		required, err = b.resolve(strings.TrimPrefix(job.Uses, localWorkflowPrefix))
		if err != nil {
			return err
		}
	case b.pin.Workflows != "" && strings.HasPrefix(job.Uses, b.pin.Workflows):
		required = b.facts
	default:
		return nil
	}

	if job.Secrets.Kind == yaml.ScalarNode && job.Secrets.Value == "inherit" {
		for _, secret := range sortedKeys(required) {
			if err := bind(bindings, "secret", secret, required[secret]); err != nil {
				return err
			}
		}

		return nil
	}

	return bindSecretForwards(bindings, required, job.Secrets, local, where)
}

func (b *workflowSecretBinder) bindLayerActions(bindings map[string]string, job secretWorkflowJob, where string) error {
	for _, step := range job.Steps {
		if b.pin.Actions == "" || !strings.HasPrefix(step.Uses, b.pin.Actions) {
			continue
		}

		for _, forwards := range []yaml.Node{step.With, step.Env} {
			if err := bindSecretForwards(bindings, b.facts, forwards, false, where+" layer action"); err != nil {
				return err
			}
		}
	}

	return nil
}

func bindSecretForwards(bindings, required map[string]string, node yaml.Node, requireAll bool, where string) error {
	var forwards map[string]string
	if node.Kind != 0 {
		if err := node.Decode(&forwards); err != nil {
			return fmt.Errorf("%s has invalid secret forwarding: %w", where, errs.ErrValidation)
		}
	}

	for _, secret := range sortedKeys(required) {
		written, passed := forwards[secret]
		if !passed {
			if requireAll {
				return fmt.Errorf("%s does not forward required secret %s: %w", where, secret, errs.ErrValidation)
			}

			continue
		}

		match := consumerSecretExpression.FindStringSubmatch(written)
		if match == nil {
			return fmt.Errorf("%s secret %s must forward a single named secret: %w", where, secret, errs.ErrValidation)
		}

		if err := bind(bindings, "secret", match[1], required[secret]); err != nil {
			return err
		}
	}

	return nil
}
