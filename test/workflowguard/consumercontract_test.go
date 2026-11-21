// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package workflowguard holds the guards over .github/workflows, the surface
// this repository ships to the repositories that consume it.
//
// The workflows are the product. Their `on.workflow_call` inputs are a public
// API every adopter's caller workflow codes against, their `uses:` edges have
// to line up with the callee's declared inputs, and one that handles a secret
// must reach the event-context check before it touches it. None of that is
// expressible in Go's type system, and all of it breaks in the adopter's
// repository rather than this one: on their next tag bump, with no signal
// here. These guards turn that silent break into a failing test.
//
// One of the repo-wide guard packages; docs/testing.md says which is which and
// where a new guard belongs.
package workflowguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// regenerateContractsEnv, when set to 1, makes
// TestConsumerContractSnapshot rewrite the snapshot instead of
// comparing against it — the deliberate way to change the contract.
const regenerateContractsEnv = "REGENERATE_CONTRACTS"

// consumerInput is the per-input slice of the consumer contract:
// whether the input is mandatory and what type it carries. Names live
// as the map keys in consumerContract.Inputs.
type consumerInput struct {
	Required bool   `json:"required"`
	Type     string `json:"type"`
	Default  any    `json:"default,omitempty"`
}

// consumerContract is one orchestrator's public API surface: the
// `on.workflow_call` inputs (name, required, type) and secret names
// that every consuming repository's thin caller workflow codes against.
type consumerContract struct {
	Inputs          map[string]consumerInput `json:"inputs"`
	Secrets         []string                 `json:"secrets"`
	RequiredSecrets []string                 `json:"required_secrets"`
	Outputs         map[string]string        `json:"outputs"`
}

// TestConsumerContractSnapshot pins the consumer-facing API of this
// repository on all three forges to a checked-in snapshot: the
// `on.workflow_call` inputs (names + required + types) and secret names
// of every GitHub *-orchestrator.yml and every reusable Forgejo
// workflow, the inputs and outputs of every Forgejo action, and the
// `spec.inputs` of every GitLab component. Renaming an input, flipping
// required, or dropping a secret breaks every consuming repository on
// their NEXT tag bump with no signal here, and Forgejo does not refuse an
// input it does not know; this test turns that silent break into a
// reviewed diff. It compares each file with its own past, not the forges
// with each other.
//
// To change the contract deliberately, regenerate the snapshot and
// commit both sides:
//
//	REGENERATE_CONTRACTS=1 go test ./test/workflowguard -run TestConsumerContractSnapshot
func TestConsumerContractSnapshot(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	contracts := map[string]consumerContract{}

	for _, surface := range []struct {
		pattern string
		parse   func(*testing.T, string) (consumerContract, bool)
	}{
		{".github/workflows/*-orchestrator.yml", parseCallableContract},
		{".forgejo/workflows/*.yml", parseCallableContract},
		{".forgejo/actions/*/action.yml", parseActionContract},
		{"templates/*.yml", parseComponentContract},
	} {
		files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(surface.pattern)))
		require.NoError(t, err)

		found := 0

		for _, path := range files {
			contract, ok := surface.parse(t, path)
			if !ok {
				continue
			}

			relative, err := filepath.Rel(root, path)
			require.NoError(t, err)

			contracts[filepath.ToSlash(relative)] = contract
			found++
		}

		require.NotZerof(t, found, "no consumer-facing file matched %s", surface.pattern)
	}

	blob, err := json.MarshalIndent(contracts, "", "  ")
	require.NoError(t, err)

	got := string(blob) + "\n"

	// Relative to the package directory, which is where `go test` runs, so the
	// snapshot travels with the guard rather than being addressed from the root.
	snapshot := filepath.Join("testdata", "consumer-contracts.json")

	if os.Getenv(regenerateContractsEnv) == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(snapshot), 0o750))
		require.NoError(t, os.WriteFile(snapshot, []byte(got), 0o600))
		t.Logf("regenerated %s", snapshot)

		return
	}

	want, err := os.ReadFile(snapshot)
	require.NoErrorf(t, err,
		"consumer-contract snapshot missing; generate it deliberately with "+
			regenerateContractsEnv+"=1 go test ./test/workflowguard -run TestConsumerContractSnapshot")

	require.Equalf(t, string(want), got,
		"the consumer contract changed: the inputs, secrets and outputs of the workflows, "+
			"actions and components are the public API every consuming repository codes against. "+
			"If the change is deliberate, regenerate the snapshot and commit it: "+
			regenerateContractsEnv+"=1 go test ./test/workflowguard -run TestConsumerContractSnapshot")
}

// parseConsumerContract extracts the workflow_call inputs and secret
// names from one orchestrator file via raw node walking (key strings
// stay literal, so the `on:` key needs no YAML-1.1 bool gymnastics).
func parseConsumerContract(t *testing.T, path string) consumerContract {
	t.Helper()

	contract, ok := parseCallableContract(t, path)
	require.Truef(t, ok, "%s: no on.workflow_call block — not a consumer-facing reusable workflow", path)

	return contract
}

// parseCallableContract does the same for any workflow file. One without
// on.workflow_call is the repository's own entry point, which no consumer
// calls, and is reported as not part of the contract.
func parseCallableContract(t *testing.T, path string) (consumerContract, bool) {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoErrorf(t, err, "read %s", path)

	doc, err := effectiveWorkflow(body)
	require.NoErrorf(t, err, "parse %s", path)

	on := mappingChild(doc, "on")
	if on == nil || on.Kind != yaml.MappingNode {
		return consumerContract{}, false
	}

	call := mappingChild(on, "workflow_call")
	if call == nil {
		for index := 0; index < len(on.Content); index += 2 {
			if on.Content[index].Value == "workflow_call" {
				call = on.Content[index+1] // declared with no inputs
			}
		}

		if call == nil {
			return consumerContract{}, false
		}
	}

	contract := consumerContract{
		Inputs:          map[string]consumerInput{},
		Secrets:         []string{},
		RequiredSecrets: []string{},
		Outputs:         map[string]string{},
	}

	if inputs := mappingChild(call, "inputs"); inputs != nil && inputs.Kind == yaml.MappingNode {
		for index := 0; index < len(inputs.Content); index += 2 {
			spec := inputs.Content[index+1]

			contract.Inputs[inputs.Content[index].Value] = consumerInput{
				Required: scalarChild(spec, "required") == "true",
				Type:     scalarChild(spec, "type"),
			}
			if value := mappingChild(spec, "default"); value != nil {
				input := contract.Inputs[inputs.Content[index].Value]
				require.NoError(t, value.Decode(&input.Default))
				contract.Inputs[inputs.Content[index].Value] = input
			}
		}
	}

	if secrets := mappingChild(call, "secrets"); secrets != nil && secrets.Kind == yaml.MappingNode {
		for i := 0; i < len(secrets.Content); i += 2 {
			contract.Secrets = append(contract.Secrets, secrets.Content[i].Value)
			if scalarChild(secrets.Content[i+1], "required") == "true" {
				contract.RequiredSecrets = append(contract.RequiredSecrets, secrets.Content[i].Value)
			}
		}

		slices.Sort(contract.Secrets)
		slices.Sort(contract.RequiredSecrets)
	}

	if outputs := mappingChild(call, "outputs"); outputs != nil {
		for i := 0; i+1 < len(outputs.Content); i += 2 {
			contract.Outputs[outputs.Content[i].Value] = scalarChild(outputs.Content[i+1], "value")
		}
	}

	return contract, true
}

// parseActionContract extracts the inputs and outputs of one Forgejo action,
// which a consumer's own job calls step by step. An action input has no type.
func parseActionContract(t *testing.T, path string) (consumerContract, bool) {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoErrorf(t, err, "read %s", path)

	var action yaml.Node
	require.NoErrorf(t, yaml.Unmarshal(body, &action), "parse %s", path)

	if len(action.Content) == 0 {
		return consumerContract{}, false
	}

	contract := consumerContract{
		Inputs:          map[string]consumerInput{},
		Secrets:         []string{},
		RequiredSecrets: []string{},
		Outputs:         map[string]string{},
	}

	if inputs := mappingChild(action.Content[0], "inputs"); inputs != nil {
		for index := 0; index+1 < len(inputs.Content); index += 2 {
			spec := inputs.Content[index+1]
			input := consumerInput{Required: scalarChild(spec, "required") == "true"}

			if value := mappingChild(spec, "default"); value != nil {
				require.NoError(t, value.Decode(&input.Default))
			}

			contract.Inputs[inputs.Content[index].Value] = input
		}
	}

	if outputs := mappingChild(action.Content[0], "outputs"); outputs != nil {
		for index := 0; index+1 < len(outputs.Content); index += 2 {
			contract.Outputs[outputs.Content[index].Value] = scalarChild(outputs.Content[index+1], "value")
		}
	}

	return contract, true
}

// parseComponentContract extracts the spec.inputs of one GitLab component:
// its header document. An input without a default is required.
func parseComponentContract(t *testing.T, path string) (consumerContract, bool) {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoErrorf(t, err, "read %s", path)

	var header yaml.Node
	require.NoErrorf(t, yaml.Unmarshal(body, &header), "parse %s", path)

	if len(header.Content) == 0 {
		return consumerContract{}, false
	}

	inputs := mappingChild(mappingChild(header.Content[0], "spec"), "inputs")
	if inputs == nil || inputs.Kind != yaml.MappingNode {
		return consumerContract{}, false
	}

	contract := consumerContract{
		Inputs:          map[string]consumerInput{},
		Secrets:         []string{},
		RequiredSecrets: []string{},
		Outputs:         map[string]string{},
	}

	for index := 0; index < len(inputs.Content); index += 2 {
		spec := inputs.Content[index+1]
		input := consumerInput{Type: scalarChild(spec, "type")}

		if value := mappingChild(spec, "default"); value != nil {
			require.NoError(t, value.Decode(&input.Default))
		} else {
			input.Required = true
		}

		contract.Inputs[inputs.Content[index].Value] = input
	}

	return contract, true
}

// mappingChild returns the value node of key inside a mapping node, or
// nil when node is nil, not a mapping, or lacks the key.
func mappingChild(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

// scalarChild returns the scalar value of key inside a mapping node,
// or "" when absent or non-scalar.
func scalarChild(node *yaml.Node, key string) string {
	child := mappingChild(node, key)
	if child == nil || child.Kind != yaml.ScalarNode {
		return ""
	}

	return child.Value
}
