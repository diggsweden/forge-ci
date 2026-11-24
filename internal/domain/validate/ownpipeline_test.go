// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
)

func TestWorkflowTriggersReadsEveryForm(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		body string
		want []string
	}{
		"scalar":  {"on: workflow_call\njobs: {}\n", []string{"workflow_call"}},
		"list":    {"on: [push, workflow_call]\njobs: {}\n", []string{"push", "workflow_call"}},
		"mapping": {"on:\n  workflow_call:\n    inputs: {}\n  push:\n    tags: ['v*']\njobs: {}\n", []string{"workflow_call", "push"}},
		"none":    {"jobs: {}\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := validate.WorkflowTriggers([]byte(test.body))
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestOwnPipelineMismatch(t *testing.T) {
	t.Parallel()

	const (
		called = "on:\n  workflow_call:\njobs: {}\n"
		pushed = "on:\n  push:\njobs: {}\n"
	)

	for name, test := range map[string]struct {
		path, body string
		agrees     bool
	}{
		"a layer workflow":                    {".forgejo/workflows/prepare-release.yml", called, true},
		"an own pipeline":                     {".forgejo/workflows/self-signer-image.yml", pushed, true},
		"a layer workflow that lost its call": {".forgejo/workflows/prepare-release.yml", pushed, false},
		"an own pipeline that is callable":    {".github/workflows/self-release.yml", "on: [workflow_call, push]\njobs: {}\n", false},
		"a workflow with no trigger":          {".forgejo/workflows/docs.yml", "jobs: {}\n", false},
		"a file that does not parse":          {".forgejo/workflows/self-x.yml", "on: [\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mismatch := validate.OwnPipelineMismatch(test.path, []byte(test.body))
			require.Equal(t, test.agrees, mismatch == "", mismatch)
		})
	}
}
