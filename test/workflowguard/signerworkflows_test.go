// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// validate isolation lets a signing secret reach a GitHub job only when the
// job calls one of the layer's GitHub signer workflows. Those are the
// orchestrators a consumer calls whose workflow_call secrets name a signing
// secret: one left off the list would have its callers refused, and one kept
// on it after it stopped taking keys would let any caller hand it one. The
// release orchestrator is also the GitHub release entry point.
func TestIsolationGitHubSignerWorkflowsAreTheOrchestratorsThatTakeSigningSecrets(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)

	signing := validate.DefaultSigningSecrets()

	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*-orchestrator.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	var takers []string

	for _, file := range files {
		contract, callable := parseCallableContract(t, file)
		if callable && slices.ContainsFunc(contract.Secrets, func(secret string) bool { return slices.Contains(signing, secret) }) {
			takers = append(takers, ".github/workflows/"+filepath.Base(file))
		}
	}

	listed := slices.DeleteFunc(validate.SignerWorkflows(), func(workflow string) bool { return !strings.HasPrefix(workflow, ".github/") })
	slices.Sort(listed)
	slices.Sort(takers)

	require.Equal(t, takers, listed, "the isolation check's GitHub signer workflows must be the orchestrators whose secrets name a signing secret")
	require.Contains(t, validate.ReleaseEntryWorkflows(), ".github/workflows/release-orchestrator.yml")
}

// The release orchestrator checks the caller's workflows before anything
// reads the release configuration: right after the caller checkout of the
// plan job, on every caller but the engine's own release, with the engine
// checkout as the contract root.
func TestReleaseOrchestratorChecksTheCallersIsolation(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(reporoot.Path(t), ".github", "workflows", "release-orchestrator.yml"))
	require.NoError(t, err)

	type step struct {
		Name string `yaml:"name"`
		If   string `yaml:"if"`
		Run  string `yaml:"run"`
	}

	var workflow struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(body, &workflow))

	steps := workflow.Jobs["parse-config"].Steps
	checkout := slices.IndexFunc(steps, func(s step) bool { return s.Name == "Checkout caller repository" })
	require.GreaterOrEqual(t, checkout, 0)
	require.Less(t, checkout+1, len(steps))

	check := steps[checkout+1]
	require.Contains(t, check.Run, "forge-ci validate isolation --workflows-dir .github/workflows")
	require.Contains(t, check.Run, `--single-pin-subject "$ENGINE_REPOSITORY"`)
	require.Contains(t, check.Run, "--contract-root _forge_ci_bootstrap")
	require.Equal(t, "${{ github.repository != env.ENGINE_REPOSITORY }}", check.If)
}
