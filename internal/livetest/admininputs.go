// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/domain/enforcement"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func adminInputEnvironment(check enforcement.Check) string {
	return "FORGE_CI_LIVE_ADMIN_" + strings.ToUpper(strings.ReplaceAll(string(check), "-", "_"))
}

// The controller has four independent booleans, all enabled by default. These
// become literal inputs in its own callers, never job environment settings.
func readAdminInputs(getenv func(string) string) (map[string]bool, error) {
	values := make(map[string]bool, len(enforcement.Checks))
	for _, check := range enforcement.Checks {
		name := adminInputEnvironment(check)

		value := true

		if raw := getenv(name); raw != "" {
			parsed, err := enforcement.ParseAdminValue(name, raw)
			if err != nil {
				return nil, fmt.Errorf("%s must be exactly true or false: %w", name, errs.ErrValidation)
			}

			value = parsed
		}

		values[enforcement.Setting(check)] = value
	}

	return values, nil
}

// AdminInputs returns literal inputs for a harness-owned GitLab policy
// include. It is not used for consumer variables or application source files.
func AdminInputs(tb TB, inputs map[string]any) map[string]any {
	tb.Helper()

	values, err := readAdminInputs(os.Getenv)
	if err != nil {
		tb.Fatalf("livetest: %v", err)
	}

	inputs = maps.Clone(inputs)
	if inputs == nil {
		inputs = map[string]any{}
	}

	for key, value := range values {
		if _, exists := inputs[key]; exists {
			tb.Fatalf("livetest: administrator input %s was already supplied", key)
		}

		inputs[key] = value
	}

	return inputs
}

type workflowAdmin struct {
	commit string
	read   func(string) ([]byte, error)
	values map[string]bool
}

func newWorkflowAdmin(ctx context.Context, root, commit string, getenv func(string) string) (*workflowAdmin, error) {
	values, err := readAdminInputs(getenv)
	if err != nil {
		return nil, err
	}

	if !fixtureObjectID(commit) {
		return nil, fmt.Errorf("administrator input declarations require the pinned engine commit: %w", errs.ErrValidation)
	}

	return &workflowAdmin{commit: commit, values: values, read: func(file string) ([]byte, error) {
		return githubStageGit().output(ctx, root, nil, "show", commit+":"+filepath.ToSlash(file))
	}}, nil
}

// render binds only engine calls owned by the harness's generated wrapper.
// Input declarations come from the engine source, not from a copied allowlist.
func (admin *workflowAdmin) render(text string, prefixes map[string]string, commit string) (string, error) {
	if admin == nil {
		return text, nil
	}

	if admin.commit != commit {
		return "", fmt.Errorf("administrator input declarations do not match the called engine commit: %w", errs.ErrValidation)
	}

	var document yaml.Node
	if err := yaml.Unmarshal([]byte(text), &document); err != nil {
		return "", fmt.Errorf("parse administrator wrapper: %w", err)
	}

	if len(document.Content) == 0 {
		return text, nil
	}

	jobs := gitlabMappingValue(document.Content[0], "jobs")
	if jobs == nil {
		return text, nil
	}

	if jobs.Kind != yaml.MappingNode {
		return "", fmt.Errorf("administrator wrapper jobs must be a mapping: %w", errs.ErrValidation)
	}

	changed := false

	for index := 1; index < len(jobs.Content); index += 2 {
		job := jobs.Content[index]

		bound, err := admin.bindJob(job, prefixes)
		if err != nil {
			return "", err
		}

		changed = changed || bound
	}

	if !changed {
		return text, nil
	}

	return encodeAdminYAML(&document)
}

func (admin *workflowAdmin) bindJob(job *yaml.Node, prefixes map[string]string) (bool, error) {
	changed, err := admin.bindCall(job, prefixes)
	if err != nil {
		return false, err
	}

	steps := gitlabMappingValue(job, "steps")
	if steps == nil {
		return changed, nil
	}

	if steps.Kind != yaml.SequenceNode {
		return false, fmt.Errorf("administrator wrapper steps must be a sequence: %w", errs.ErrValidation)
	}

	for _, step := range steps.Content {
		bound, err := admin.bindCall(step, prefixes)
		if err != nil {
			return false, err
		}

		changed = changed || bound
	}

	return changed, nil
}

func (admin *workflowAdmin) bindCall(call *yaml.Node, prefixes map[string]string) (bool, error) {
	if call.Kind != yaml.MappingNode {
		return false, fmt.Errorf("administrator wrapper calls must be mappings: %w", errs.ErrValidation)
	}

	use := gitlabMappingValue(call, "uses")
	if use == nil || use.Kind != yaml.ScalarNode {
		return false, nil
	}

	for prefix, directory := range prefixes {
		rest, matches := strings.CutPrefix(use.Value, prefix)
		if !matches {
			continue
		}

		name, pin, found := strings.Cut(rest, "@")
		if !found || pin != admin.commit || !filepath.IsLocal(name) || strings.ContainsAny(name, "\\/") {
			return false, fmt.Errorf("administrator wrapper must call the pinned engine directly: %w", errs.ErrValidation)
		}

		file := filepath.Join(directory, name)
		if strings.Contains(directory, "/actions/") || strings.HasSuffix(directory, "/actions") {
			file = filepath.Join(file, "action.yml")
		}

		declared, err := admin.declaredInputs(file)
		if err != nil {
			return false, err
		}

		return admin.bindInputs(call, declared, "with")
	}

	return false, nil
}

func (admin *workflowAdmin) declaredInputs(file string) (*yaml.Node, error) {
	body, err := admin.read(file)
	if err != nil {
		return nil, fmt.Errorf("read engine input declaration %s: %w", file, err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(body, &document); err != nil {
		return nil, err
	}

	if len(document.Content) == 0 {
		return nil, fmt.Errorf("empty engine input declaration %s: %w", file, errs.ErrValidation)
	}

	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("engine input declaration %s must be a mapping: %w", file, errs.ErrValidation)
	}

	if strings.HasPrefix(filepath.ToSlash(file), "templates/") {
		return gitlabMappingValue(gitlabMappingValue(root, "spec"), "inputs"), nil
	}

	if filepath.Base(file) == "action.yml" {
		return gitlabMappingValue(root, "inputs"), nil
	}

	return gitlabMappingValue(gitlabMappingValue(gitlabMappingValue(root, "on"), "workflow_call"), "inputs"), nil
}

func (admin *workflowAdmin) bindInputs(call, declared *yaml.Node, field string) (bool, error) {
	inputs := gitlabMappingValue(call, field)
	if inputs != nil && inputs.Kind != yaml.MappingNode {
		return false, fmt.Errorf("administrator wrapper inputs must be a direct mapping: %w", errs.ErrValidation)
	}

	for _, check := range enforcement.Checks {
		if gitlabMappingValue(inputs, enforcement.Setting(check)) != nil {
			return false, fmt.Errorf("consumer wrapper already supplies administrator input %s: %w", enforcement.Setting(check), errs.ErrValidation)
		}
	}

	if gitlabMappingValue(inputs, "<<") != nil {
		return false, fmt.Errorf("administrator wrapper inputs cannot merge another mapping: %w", errs.ErrValidation)
	}

	changed := false

	for _, check := range enforcement.Checks {
		key := enforcement.Setting(check)
		if gitlabMappingValue(declared, key) == nil {
			continue
		}

		if inputs == nil {
			inputs = &yaml.Node{Kind: yaml.MappingNode}
			call.Content = append(call.Content, gitlabYAMLString(field), inputs)
		}

		inputs.Content = append(inputs.Content, gitlabYAMLString(key), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(admin.values[key])})
		changed = true
	}

	return changed, nil
}

func encodeAdminYAML(document *yaml.Node) (string, error) {
	var encoded bytes.Buffer

	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	err := encoder.Encode(document)

	return encoded.String(), err
}
