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
	resolved := map[string]map[string]string{}
	visiting := map[string]bool{}
	var resolve func(string) (map[string]string, error)
	resolve = func(name string) (map[string]string, error) {
		if found, ok := resolved[name]; ok {
			return found, nil
		}
		if visiting[name] {
			return nil, fmt.Errorf("local workflow cycle at %s: %w", name, errs.ErrValidation)
		}
		body, ok := workflows[name]
		if !ok {
			return nil, fmt.Errorf("local workflow %s is missing: %w", name, errs.ErrValidation)
		}
		visiting[name] = true
		defer delete(visiting, name)
		var workflow struct {
			Jobs map[string]struct {
				Uses    string    `yaml:"uses"`
				Secrets yaml.Node `yaml:"secrets"`
				Steps   []struct {
					Uses string    `yaml:"uses"`
					With yaml.Node `yaml:"with"`
					Env  yaml.Node `yaml:"env"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(body), &workflow); err != nil {
			return nil, fmt.Errorf("parse workflow %s: %w", name, err)
		}
		bindings := map[string]string{}
		for _, jobName := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[jobName]
			where := "workflow " + name + " job " + jobName
			for _, step := range job.Steps {
				if plan.Pin.Actions == "" || !strings.HasPrefix(step.Uses, plan.Pin.Actions) {
					continue
				}
				for _, forwards := range []yaml.Node{step.With, step.Env} {
					if err := bindSecretForwards(bindings, facts, forwards, false, where+" layer action"); err != nil {
						return nil, err
					}
				}
			}
			var required map[string]string
			switch {
			case strings.HasPrefix(job.Uses, "./.forgejo/workflows/"):
				var err error
				required, err = resolve(strings.TrimPrefix(job.Uses, "./.forgejo/workflows/"))
				if err != nil {
					return nil, err
				}
			case plan.Pin.Workflows != "" && strings.HasPrefix(job.Uses, plan.Pin.Workflows):
				required = facts
			default:
				continue
			}
			if job.Secrets.Kind == yaml.ScalarNode && job.Secrets.Value == "inherit" {
				for _, secret := range sortedKeys(required) {
					if err := bind(bindings, "secret", secret, required[secret]); err != nil {
						return nil, err
					}
				}
				continue
			}
			if err := bindSecretForwards(bindings, required, job.Secrets, strings.HasPrefix(job.Uses, "./.forgejo/workflows/"), where); err != nil {
				return nil, err
			}
		}
		resolved[name] = bindings
		return bindings, nil
	}
	for _, name := range sortedKeys(entryPoints) {
		bindings, err := resolve(name)
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
