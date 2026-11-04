// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type bootstrapWorkflow struct {
	Jobs map[string]struct {
		With  map[string]string `yaml:"with"`
		Steps []struct {
			ID   string            `yaml:"id"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func readBootstrapWorkflow(t *testing.T, path string) bootstrapWorkflow {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err)

	var workflow bootstrapWorkflow
	require.NoError(t, yaml.Unmarshal(body, &workflow))

	return workflow
}

func TestHostInstallersBuildTheirBootstrapCheckoutCommit(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join(reporoot.Path(t), ".github", "workflows", "*.yml"))
	require.NoError(t, err)

	installs := 0

	for _, path := range paths {
		workflow := readBootstrapWorkflow(t, path)
		for jobName, job := range workflow.Jobs {
			checkoutFound := false

			for _, step := range job.Steps {
				if step.ID == "forge-ci-bootstrap" {
					require.True(t, strings.HasPrefix(step.Uses, "actions/checkout@"), "%s/%s", path, jobName)
					require.Equal(t, ".github-shared", step.With["path"])
					require.Equal(t, "${{ needs.resolve-bootstrap.outputs.forge-ci-binary-ref }}", step.With["ref"])
					require.Equal(t, resolvedForgeCIRepository, step.With["repository"], "%s/%s", path, jobName)

					checkoutFound = true
				}

				if !strings.Contains(step.Run, ".github-shared/scripts/bootstrap/install-forge-ci.sh") {
					continue
				}

				installs++

				require.True(t, checkoutFound, "%s/%s installs without a preceding pinned checkout", path, jobName)
				require.Equal(t, "${{ needs.resolve-bootstrap.outputs.forge-ci-binary-ref }}", step.Env["FORGE_CI_BINARY_REF"], "%s/%s", path, jobName)
				require.Equal(t, "${{ steps.forge-ci-bootstrap.outputs.commit }}", step.Env["FORGE_CI_SOURCE_SHA"], "%s/%s", path, jobName)
				require.Equal(t, resolvedForgeCIRepository, step.Env["FORGE_CI_REPOSITORY"], "%s/%s", path, jobName)
			}
		}
	}

	require.Positive(t, installs)
}

// resolvedForgeCIRepository is the repository the resolver admitted. Jobs take
// it from there rather than re-reading job.workflow_repository, which is empty
// on GitHub Enterprise Server.
const resolvedForgeCIRepository = "${{ needs.resolve-bootstrap.outputs.forge-ci-repository }}"

func TestBootstrapResolverRefusesAnUnknownRepository(t *testing.T) {
	t.Parallel()
	workflow := readBootstrapWorkflow(t, filepath.Join(reporoot.Path(t), ".github", "workflows", "resolve-forge-ci-bootstrap.yml"))

	steps := workflow.Jobs["resolve"].Steps
	require.NotEmpty(t, steps)
	require.Equal(t, "repository", steps[0].ID, "the repository is admitted before anything reads it")
	require.Equal(t, "${{ vars.FORGE_CI_REPOSITORY || job.workflow_repository }}", steps[0].Env["REPOSITORY"])

	for _, step := range steps[1:] {
		for _, value := range []string{step.With["repository"], step.Env["REPOSITORY"]} {
			if value != "" {
				require.Equal(t, "${{ steps.repository.outputs.repository }}", value)
			}
		}
	}

	for _, tc := range []struct{ repository, say string }{
		{"", "Set the FORGE_CI_REPOSITORY variable"},
		{"noslash", "must be owner/name"},
		{"owner/../name", "must be owner/name"},
		{"owner/name/extra", "must be owner/name"},
		{"examplescope/forge-ci", ""},
	} {
		t.Run(tc.repository, func(t *testing.T) {
			t.Parallel()

			output := filepath.Join(t.TempDir(), "outputs")
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "--noprofile", "--norc", "-c", steps[0].Run)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "REPOSITORY=" + tc.repository, "GITHUB_OUTPUT=" + output}

			out, err := cmd.CombinedOutput()
			if tc.say != "" {
				require.Error(t, err, "%s", out)
				require.Contains(t, string(out), tc.say)

				_, statErr := os.Stat(output)
				require.True(t, os.IsNotExist(statErr))

				return
			}

			require.NoError(t, err, "%s", out)
			body, err := os.ReadFile(output)
			require.NoError(t, err)
			require.Equal(t, "repository="+tc.repository+"\n", string(body))
		})
	}
}

func TestOnlyTheResolverReadsTheWorkflowRepository(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	for _, dir := range []string{filepath.Join(root, ".github", "workflows"), filepath.Join(root, ".github", "actions")} {
		require.NoError(t, filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || filepath.Ext(path) != ".yml" {
				return err
			}

			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			want := 0
			if filepath.Base(path) == "resolve-forge-ci-bootstrap.yml" {
				want = 1
			}

			require.Equal(t, want, strings.Count(string(body), "job.workflow_repository"),
				"%s reads job.workflow_repository, which is empty on GitHub Enterprise Server; take needs.resolve-bootstrap.outputs.forge-ci-repository instead", path)

			return nil
		}))
	}
}

func TestBootstrapResolverPreservesSignedAssetsAndPinsSource(t *testing.T) {
	t.Parallel()
	workflow := readBootstrapWorkflow(t, filepath.Join(reporoot.Path(t), ".github", "workflows", "resolve-forge-ci-bootstrap.yml"))
	script := bootstrapSelectionScript(t, workflow, "select")

	sha := strings.Repeat("a", 40)
	for _, tc := range []struct{ ref, sha, want string }{
		{"main", sha, sha},
		{"vfeature", sha, sha},
		{sha, sha, sha},
		{strings.Repeat("b", 64), strings.Repeat("b", 64), strings.Repeat("b", 64)},
		{"main", strings.Repeat("b", 64), strings.Repeat("b", 64)},
		{"v0.92.1", sha, "v0.92.1"},
		{"v0.92.1-rc.1", sha, "v0.92.1-rc.1"},
		{"v0.90.0-pre", sha, "v0.90.0-pre"},
		{"main", "", ""},
		{"main", "short", ""},
	} {
		t.Run(tc.ref+tc.sha, func(t *testing.T) {
			t.Parallel()

			output := filepath.Join(t.TempDir(), "outputs")
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "--noprofile", "--norc", "-c", script)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "REQUESTED_REF=" + tc.ref, "SOURCE_SHA=" + tc.sha, "REPOSITORY=diggsweden/forge-ci", "IMAGE_NAMESPACE=", "GITHUB_OUTPUT=" + output}

			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				require.Error(t, err, "%s", out)

				_, statErr := os.Stat(output)
				require.True(t, os.IsNotExist(statErr))

				return
			}

			require.NoError(t, err, "%s", out)
			body, err := os.ReadFile(output)
			require.NoError(t, err)

			selected := map[string]string{}

			for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
				key, value, _ := strings.Cut(line, "=")
				selected[key] = value
			}

			require.Equal(t, tc.want, selected["ref"])

			tag := tc.want
			if len(tag) == 40 || len(tag) == 64 {
				tag = "sha-" + tag[:7]
			}

			require.Equal(t, "ghcr.io/diggsweden/forge-ci-runtime-base:"+tag, selected["runtime-image"])
			require.Equal(t, "ghcr.io/diggsweden/forge-ci-runtime:"+tag, selected["runtime-image-full"])
			require.Len(t, selected, 8)
		})
	}
}

func TestPRQualityForwardsTheSelectedBinaryToEveryLinter(t *testing.T) {
	t.Parallel()

	workflow := readBootstrapWorkflow(t, filepath.Join(reporoot.Path(t), ".github", "workflows", "pullrequest-quality-stage.yml"))
	for _, job := range []string{"nanolinter", "megalinter", "swift"} {
		require.Equal(t, "${{ needs.resolve-bootstrap.outputs.forge-ci-binary-ref }}", workflow.Jobs[job].With["forge-ci-binary-ref"], job)
	}
}

func bootstrapSelectionScript(t *testing.T, workflow bootstrapWorkflow, id string) string {
	t.Helper()

	for _, step := range workflow.Jobs["resolve"].Steps {
		if step.ID == id {
			return step.Run
		}
	}

	t.Fatalf("missing bootstrap selection step %s", id)

	return ""
}

func TestBootstrapDefaultResolvesOneConcreteStableTag(t *testing.T) {
	t.Parallel()
	workflow := readBootstrapWorkflow(t, filepath.Join(reporoot.Path(t), ".github", "workflows", "resolve-forge-ci-bootstrap.yml"))

	script := bootstrapSelectionScript(t, workflow, "requested")
	for _, tc := range []struct{ ref, metadata, want string }{
		{"", "v0.93.0", "ref=v0.93.0\ncheckout=false\n"},
		{"v0.92.1", "must-not-query", "ref=v0.92.1\ncheckout=false\n"},
		{strings.Repeat("a", 64), "must-not-query", "ref=" + strings.Repeat("a", 64) + "\ncheckout=false\n"},
		{"main", "must-not-query", "ref=main\ncheckout=true\n"},
		{"", "v0.93.0-rc.1", ""},
		{"", "", ""},
	} {
		t.Run(tc.ref+tc.metadata, func(t *testing.T) {
			t.Parallel()

			output := filepath.Join(t.TempDir(), "outputs")
			body := `gh() { [[ "$METADATA" != must-not-query ]] || return 94; printf '%s' "$METADATA"; }; ` + script
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "--noprofile", "--norc", "-c", body)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "REQUESTED_REF=" + tc.ref, "METADATA=" + tc.metadata, "SERVER_URL=https://github.com", "REPOSITORY=diggsweden/forge-ci", "GITHUB_OUTPUT=" + output}

			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				require.Error(t, err, "%s", out)

				return
			}

			require.NoError(t, err, "%s", out)
			bodyBytes, err := os.ReadFile(output)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(bodyBytes))
		})
	}
}

func TestSourceRefResolutionIsSharedAcrossPRAndSnapshotJobs(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"pullrequest-orchestrator.yml", "release-snapshot-orchestrator.yml"} {
		workflow := readBootstrapWorkflow(t, filepath.Join(reporoot.Path(t), ".github", "workflows", name))
		for jobName, job := range workflow.Jobs {
			if jobName == "resolve-bootstrap" {
				continue
			}

			if ref, ok := job.With["forge-ci-binary-ref"]; ok {
				require.Equal(t, "${{ needs.resolve-bootstrap.outputs.forge-ci-binary-ref }}", ref, "%s/%s", name, jobName)
			}

			for _, step := range job.Steps {
				if ref, ok := step.Env["FORGE_CI_BINARY_REF"]; ok {
					require.Equal(t, "${{ needs.resolve-bootstrap.outputs.forge-ci-binary-ref }}", ref, "%s/%s", name, jobName)
				}
			}
		}
	}
}

type reusableWorkflow struct {
	On struct {
		WorkflowCall struct {
			Inputs map[string]struct {
				Default any `yaml:"default"`
			} `yaml:"inputs"`
		} `yaml:"workflow_call"`
	} `yaml:"on"`
	Jobs map[string]struct {
		Uses string            `yaml:"uses"`
		With map[string]string `yaml:"with"`
	} `yaml:"jobs"`
}

func readReusableWorkflows(t *testing.T) map[string]reusableWorkflow {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(reporoot.Path(t), ".github", "workflows", "*.yml"))
	require.NoError(t, err)

	workflows := map[string]reusableWorkflow{}

	for _, path := range paths {
		body, err := os.ReadFile(path)
		require.NoError(t, err)

		var workflow reusableWorkflow
		require.NoError(t, yaml.Unmarshal(body, &workflow), path)

		workflows[filepath.Base(path)] = workflow
	}

	return workflows
}

// TestRuntimeImageDefaultsResolveTheServingRepository keeps every runtime
// image a reusable workflow runs in a resolver output. A literal default names
// one registry and one release, so a fork serving these workflows, or a caller
// on a later release, ran upstream's older image without asking for it.
func TestRuntimeImageDefaultsResolveTheServingRepository(t *testing.T) {
	t.Parallel()

	for name, workflow := range readReusableWorkflows(t) {
		for input, spec := range workflow.On.WorkflowCall.Inputs {
			if strings.HasPrefix(input, "runtime-image") {
				require.Emptyf(t, spec.Default, "%s: input %s defaults to a fixed image; default it to empty and fall back to needs.resolve-bootstrap.outputs['%s']", name, input, input)
			}
		}
	}
}

// TestLocalCallsForwardTheResolvedBinaryRef makes a workflow that resolved the
// forge-ci revision hand it to every local reusable workflow that resolves its
// own. Left empty, the callee asks for the latest stable release again: on a
// branch or commit run that is another revision than the caller's, and on a
// fork without releases it fails.
// resolvedBinaryRef is the resolver's output, read directly or through a job
// that relays it, as the release orchestrator's parse-config does.
var resolvedBinaryRef = regexp.MustCompile(`^\$\{\{ needs\.[a-z0-9-]+\.outputs\.forge-ci-binary-ref \}\}$`)

func TestLocalCallsForwardTheResolvedBinaryRef(t *testing.T) {
	t.Parallel()

	workflows := readReusableWorkflows(t)
	calls := 0

	for name, workflow := range workflows {
		if _, resolves := workflow.Jobs["resolve-bootstrap"]; !resolves {
			continue
		}

		for jobName, job := range workflow.Jobs {
			callee, local := strings.CutPrefix(job.Uses, "./.github/workflows/")
			if !local || callee == "resolve-forge-ci-bootstrap.yml" {
				continue
			}

			if _, takes := workflows[callee].On.WorkflowCall.Inputs[binaryRefInput]; !takes {
				continue
			}

			calls++

			require.Regexpf(t, resolvedBinaryRef, job.With[binaryRefInput],
				"%s/%s calls %s without the resolved %s", name, jobName, callee, binaryRefInput)
		}
	}

	require.Positive(t, calls)
}
