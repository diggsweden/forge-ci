// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
)

func TestGitHubBaseLifecycleKeepsAuthoritiesAndAuthenticatedOutputs(t *testing.T) {
	t.Parallel()

	workflow := readCanonicalConsumerWorkflow(t, "base-image-lifecycle.yml")
	for name, dependencies := range map[string][]string{
		"plan": {"resolve-bootstrap"}, "build": {"resolve-bootstrap", "plan"},
		"publish": {"plan", "build"}, "sign": {"publish"},
		"promote": {"publish", "sign"}, "authenticate": {"publish", "promote"},
	} {
		job := workflow.Jobs[name]
		require.Equal(t, dependencies, job.Needs, name)
		require.NotEmpty(t, job.Steps, name)

		for _, dependency := range dependencies {
			require.Contains(t, job.If, "needs."+dependency+".result == 'success'", name)
		}

		require.Contains(t, job.If, "!cancelled()", name)
	}

	body := reporoot.ReadFile(t, ".github/workflows/base-image-lifecycle.yml")

	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(body, &doc))

	root := doc.Content[0]
	for _, name := range []string{"publish", "sign", "promote", "authenticate"} {
		require.Equal(t, "ubuntu-26.04", scalarChild(mappingChild(mappingChild(root, "jobs"), name), "runs-on"))
	}

	for name, permission := range map[string]string{
		"build": "", "plan": "", "publish": "write", "sign": "write", "promote": "write", "authenticate": "read",
	} {
		require.Equal(t, permission, workflow.Jobs[name].Permissions["packages"], name)
	}

	outputs := mappingChild(mappingChild(mappingChild(root, "on"), "workflow_call"), "outputs")
	for _, name := range []string{"ledger-json", "parent-base-json"} {
		require.Equal(t, "${{ jobs.authenticate.outputs."+name+" }}", scalarChild(mappingChild(outputs, name), "value"))
	}

	for _, name := range []string{"promote", "authenticate"} {
		found := false

		for _, step := range workflow.Jobs[name].Steps {
			if value := step.Env["BASE_INPUTS_JSON"]; value != "" {
				require.Equal(t, "${{ inputs['expected-inputs-json'] }}", value)

				found = true
			}
		}

		require.True(t, found, name)
	}
}

func TestGitHubBaseAuthenticationFailsWithoutEvidence(t *testing.T) {
	t.Parallel()
	workflow := readCanonicalConsumerWorkflow(t, "base-image-lifecycle.yml")

	var script string

	for _, step := range workflow.Jobs["authenticate"].Steps {
		if step.Name == "Authenticate exact promoted parent" {
			script = step.Run
		}
	}

	require.NotEmpty(t, script)

	for _, mode := range []string{"success", "missing-token", "missing-ref", "missing-ledger", "login-failure", "verification-failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			output := filepath.Join(dir, "output")
			require.NoError(t, os.WriteFile(output, nil, 0o600))

			ref, ledger, token := "registry.invalid/base@sha256:"+strings.Repeat("a", 64), "[]", "fixture-token"

			switch mode {
			case "missing-token":
				token = ""
			case "missing-ref":
				ref = ""
			case "missing-ledger":
				ledger = ""
			}

			stub := `forge-ci() {
  test -z "${REGISTRY_TOKEN:-}" || return 90
  case "$1 $2" in
    'container login')
      test "$(cat)" = fixture-token || return 91
      test "$MODE" != login-failure || return 92
      ;;
    'container base-images')
      test "$3" = authenticate-parent || return 93
      test "$9" = '[{"flavor":"go","base_input_id":"independent"}]' || return 94
      test "$MODE" != verification-failure || return 95
      printf '%s\n' '{"ref":"verified","input_id":"independent"}'
      ;;
    *) return 96 ;;
  esac
}
`
			command := exec.CommandContext(t.Context(), testenv.GNUExecutable(t, "bash"), "-c", stub+script)
			command.Dir = dir
			command.Env = []string{"PATH=" + os.Getenv("PATH"), "MODE=" + mode, "RUNNER_TEMP=" + dir,
				"GITHUB_OUTPUT=" + output, "GITHUB_SERVER_URL=https://github.com", "GITHUB_REPOSITORY=owner/app",
				"REGISTRY_TOKEN=" + token, "REGISTRY_USER=owner", "EXPECTED_REPOSITORY=registry.invalid/base",
				"EXPECTED_WORKFLOW=release.yml", "TRUSTED_KEY=public-key", "TRUSTED_KEY_SHA256=key-digest",
				"EXPECTED_REF=" + ref, "LEDGER_JSON=" + ledger,
				`BASE_INPUTS_JSON=[{"flavor":"go","base_input_id":"independent"}]`}
			log, err := command.CombinedOutput()
			body, readErr := os.ReadFile(output)
			require.NoError(t, readErr)
			require.NotContains(t, string(log), "fixture-token")

			if mode == "success" {
				require.NoError(t, err, "%s", log)
				require.Equal(t, "ledger-json=[]\nparent-base-json={\"ref\":\"verified\",\"input_id\":\"independent\"}\n", string(body))
			} else {
				require.Error(t, err)
				require.Empty(t, body)
			}
		})
	}
}

func TestGitHubBasePlanSelectsOneStageAndNativeBuilder(t *testing.T) {
	t.Parallel()
	workflow := readCanonicalConsumerWorkflow(t, "base-image-lifecycle.yml")

	var script string

	for _, step := range workflow.Jobs["plan"].Steps {
		if step.Name == "Admit one native base stage" {
			script = step.Run
		}
	}

	require.NotEmpty(t, script)

	for _, tc := range []struct {
		name, flavor, builder, parent, stage, image string
	}{
		{"root", "go", "buildah", "", "root-base", ""},
		{"child", "java", "buildah", `{"ref":"verified","input_id":"independent"}`, "child-base", ""},
		{"several flavors", "go,java", "buildah", "", "", ""},
		{"missing flavor", "", "buildah", "", "", ""},
		{"unsupported builder", "go", "other", "", "", ""},
		{"missing Kaniko image", "go", "kaniko", "", "", ""},
		{"mutable Kaniko image", "go", "kaniko", "", "", "registry.invalid/kaniko:latest"},
		{"Kaniko root", "go", "kaniko", "", "root-base", "registry.invalid/kaniko@sha256:" + strings.Repeat("a", 64)},
		{"Kaniko child", "java", "kaniko", `{"ref":"verified","input_id":"independent"}`, "child-base", "registry.invalid/kaniko@sha256:" + strings.Repeat("a", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			command := exec.CommandContext(t.Context(), testenv.GNUExecutable(t, "bash"), "-c",
				`forge-ci() { printf '%s\n' "$@" >"$CALLS"; }`+"\n"+script)
			command.Dir = dir
			command.Env = []string{"PATH=" + os.Getenv("PATH"), "CALLS=" + calls, "RUNNER_TEMP=" + dir,
				"GITHUB_OUTPUT=" + filepath.Join(dir, "outputs"), "BUILDER_JOB_IMAGE=" + tc.image,
				"STAGE_FLAVOR=" + tc.flavor, "BUILDER=" + tc.builder, "PARENT_BASE_JSON=" + tc.parent,
				`IMAGE_PLAN_JSON={}`, "IMAGE_REPOSITORY=registry.invalid/base", "RELEASE_SHA=approved-source",
				"GITHUB_SERVER_URL=https://github.com", "GITHUB_REPOSITORY=owner/app",
				`RUNS_ON=["ordinary"]`, `BUILD_RUNS_ON_LINUX_AMD64=["amd64"]`, `BUILD_RUNS_ON_LINUX_ARM64=["arm64"]`}
			log, err := command.CombinedOutput()
			body, readErr := os.ReadFile(calls)

			if tc.stage == "" {
				require.Error(t, err)
				require.ErrorIs(t, readErr, os.ErrNotExist, "invalid selection must fail before planning")

				return
			}

			require.NoError(t, err, "%s", log)
			require.NoError(t, readErr)
			require.True(t, strings.HasPrefix(string(body), "container\nbase-images\n"+tc.stage+"\nplan\n"))

			for _, pair := range []string{"--stage-flavors\n" + tc.flavor, "--release-sha\napproved-source",
				`build-runs-on-linux-amd64=["amd64"]`, `build-runs-on-linux-arm64=["arm64"]`} {
				require.Contains(t, string(body), pair+"\n")
			}

			if tc.parent != "" {
				require.Contains(t, string(body), "--parent-base-json\n"+tc.parent+"\n")
			}
		})
	}
}
