// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func TestRuntimePublicationBarriersAndSigningBoundary(t *testing.T) {
	t.Parallel()

	runtimeWorkflow := loadWorkflow(t, "examples/forgejo/runtime-images.yml")
	signerImage := "${{ vars.CI_SIGNER_IMAGE }}"

	for name, prerequisite := range map[string]string{
		"build": "request-runner", "collect": "build", "smoke": "collect",
		"assemble": "smoke", "sign": "assemble", "publish": "sign",
	} {
		require.Contains(t, jobNeeds(runtimeWorkflow.jobs[name]), prerequisite, "%s must wait for the entire preceding matrix", name)
	}

	require.Contains(t, jobNeeds(runtimeWorkflow.jobs["smoke"]), "request-smoke-runner")
	require.Contains(t, jobNeeds(runtimeWorkflow.jobs["request-smoke-runner"]), "collect")

	require.Equal(t, "forgejo.ref == 'refs/heads/main'", scalarString(mappingValue(runtimeWorkflow.jobs["plan"].node, "if")))

	for name, target := range runtimeWorkflow.jobs {
		text := target.node.String()
		if name != "sign" {
			require.NotContains(t, text, "secrets.RELEASE_COSIGN", "signing authority leaked to %s", name)
		}

		if !slices.Contains([]string{"request-runner", "request-smoke-runner"}, name) {
			require.NotContains(t, text, "secrets.CI_RUNNER_LENDER_TOKEN", "runner-lending authority leaked to %s", name)
		}

		for _, step := range target.steps() {
			if strings.Contains(scalarString(mappingValue(step, "run")), "platform checkout") {
				require.Equal(t, "${{ forgejo.token }}", scalarString(mappingValue(mappingValue(step, "env"), "FORGEJO_TOKEN")), "%s must authenticate private source checkout", name)
			}
		}

		if name == "sign" || name == "publish" {
			require.Equal(t, signerImage, scalarString(mappingValue(target.node, "container")), "%s must use the reviewed signer, never a candidate", name)

			for _, forbidden := range []string{"platform checkout", "runtime-smoke.sh", "mise install", "apt-get", "go run", "bash scripts/"} {
				require.NotContains(t, text, forbidden, "%s must not execute source or install tools", name)
			}

			require.Contains(t, text, "multiarch-image verify", "%s must bind the index to its complete native metadata", name)
		}
	}

	require.Equal(t, "${{ matrix.ref }}", scalarString(mappingValue(mappingValue(runtimeWorkflow.jobs["smoke"].node, "container"), "image")))
	require.Contains(t, runtimeWorkflow.jobs["smoke"].node.String(), "sha256sum --check --strict")
	require.True(t, jobRunsStep(runtimeWorkflow.jobs["plan"], "container platform-plan"))
	require.True(t, jobRunsStep(runtimeWorkflow.jobs["request-runner"], "platform request-runner"))
	require.Contains(t, runtimeWorkflow.jobs["build"].node.String(), "vars.CI_IMAGE_BUILDER")
	require.Equal(t, "${{ fromJSON(vars[matrix.arch == 'arm64' && 'CI_RUNNER_LABEL_CONTAINER_ARM64' || 'CI_RUNNER_LABEL_CONTAINER'] || vars[matrix.arch == 'arm64' && 'CI_RUNNER_LABEL_ARM64' || 'CI_RUNNER_LABEL']) }}", runtimeWorkflow.jobs["build"].runsOn())
	require.Equal(t, "${{ fromJSON(vars[matrix.arch == 'arm64' && 'CI_RUNNER_LABEL_ARM64' || 'CI_RUNNER_LABEL'] || vars[matrix.arch == 'arm64' && 'CI_RUNNER_LABEL_CONTAINER_ARM64' || 'CI_RUNNER_LABEL_CONTAINER']) }}", runtimeWorkflow.jobs["smoke"].runsOn())
	require.Contains(t, runtimeWorkflow.jobs["publish"].node.String(), "Refusing stale runtime publication")
	require.Contains(t, runtimeWorkflow.jobs["publish"].node.String(), "--preserve-digests")
	require.NotContains(t, runtimeWorkflow.jobs["publish"].node.String(), "--no-creds")
	require.NotContains(t, runtimeWorkflow.jobs["publish"].node.String(), "git ls-remote")
	require.Contains(t, runtimeWorkflow.jobs["publish"].node.String(), "curl --config -")
	credentials := mappingValue(mappingValue(runtimeWorkflow.jobs["smoke"].node, "container"), "credentials")
	require.Equal(t, "${{ secrets.RELEASE_REGISTRY_TOKEN }}", scalarString(mappingValue(credentials, "password")))
}

func TestRuntimePublicationVerifiesAuthenticatedPromotedBytes(t *testing.T) {
	t.Parallel()

	w := loadWorkflow(t, "examples/forgejo/runtime-images.yml")
	step, ok := w.findStep("Verify published digest and report the consumer pin")
	require.True(t, ok)
	literal, ok := mappingValue(step, "run").(*ast.LiteralNode)
	require.True(t, ok)

	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			auth := filepath.Join(t.TempDir(), "auth.json")
			require.NoError(t, os.WriteFile(auth, []byte(`{"auths":{}}`), 0o600))

			expected := []byte(`{"schemaVersion":2,"manifests":[]}`)
			ref := fmt.Sprintf("registry.example/acme/runtime@sha256:%x", sha256.Sum256(expected))
			require.NoError(t, os.WriteFile(filepath.Join(root, "published-ref.txt"), []byte(ref+"\n"), 0o600))

			served := expected
			if changed {
				served = []byte(`{"different":"manifest"}`)
			}

			input := filepath.Join(root, "input.json")
			require.NoError(t, os.WriteFile(input, served, 0o600))

			for name, body := range map[string]string{
				"skopeo": `#!/bin/sh
set -eu
test "$1" = inspect
test "$2" = --authfile
test "$3" = "$REGISTRY_AUTH_FILE"
test -f "$3"
test "$4" = --raw
test "$5" = "docker://$IMAGE_REPOSITORY:sha-$SOURCE_SHA"
cat "$INDEX_FIXTURE"
`,
				"forge-ci": "#!/bin/sh\nset -eu\ntest \"$1 $2\" = 'artifact upload'\nprintf uploaded >\"$PUBLISHED_MARKER\"\n",
			} {
				path := filepath.Join(root, name)
				require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
				require.NoError(t, os.Chmod(path, 0o700))
			}

			marker := filepath.Join(root, "uploaded")
			cmd := exec.CommandContext(t.Context(), "bash", "-c", literal.Value.Value)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"), "TARGET=base", "REGISTRY_AUTH_FILE="+auth, "IMAGE_REPOSITORY=registry.example/acme/runtime", "SOURCE_SHA="+strings.Repeat("a", 40), "INDEX_FIXTURE="+input, "PUBLISHED_MARKER="+marker)

			out, err := cmd.CombinedOutput()
			if changed {
				require.Error(t, err, "%s", out)
				require.NoFileExists(t, marker)
			} else {
				require.NoError(t, err, "%s", out)
				require.FileExists(t, marker)
				require.Contains(t, string(out), ref)
			}
		})
	}
}

func TestRuntimeSmokeReceiptIsWrittenOnlyAfterSuccessfulChecks(t *testing.T) {
	t.Parallel()

	w := loadWorkflow(t, "examples/forgejo/runtime-images.yml")
	checked := false
	recorded := false

	for _, step := range w.jobs["smoke"].steps() {
		name := scalarString(mappingValue(step, "name"))
		if name == "Exercise the native runtime toolchain" {
			checked = true

			require.Nil(t, mappingValue(step, "continue-on-error"))
		}

		if name == "Record the digest that passed native smoke checks" {
			recorded = true

			require.True(t, checked, "receipt must follow smoke checks")
			require.Nil(t, mappingValue(step, "if"), "default success gating is required")
			require.Nil(t, mappingValue(step, "continue-on-error"))
			env := mappingValue(step, "env")
			require.Equal(t, "${{ matrix.ref }}", scalarString(mappingValue(env, "TESTED_REF")))
			require.Equal(t, "${{ matrix.arch }}", scalarString(mappingValue(env, "ARCH")))
			require.Equal(t, "${{ matrix.target }}", scalarString(mappingValue(env, "TARGET")))
		}
	}

	require.True(t, recorded)
}

func TestRuntimeSmokeRunnerRequestsOnlyMissingNativeARMCapacity(t *testing.T) {
	t.Parallel()

	w := loadWorkflow(t, "examples/forgejo/runtime-images.yml")

	var script string

	for _, step := range w.jobs["request-smoke-runner"].steps() {
		if scalarString(mappingValue(step, "name")) == "Request fresh smoke runners when ARM64 uses the build pool" {
			require.Equal(t, "vars.CI_RUNNER_LABEL_ARM64 == ''", scalarString(mappingValue(step, "if")))
			literal, ok := mappingValue(step, "run").(*ast.LiteralNode)
			require.True(t, ok)

			script = literal.Value.Value
		}
	}

	require.NotEmpty(t, script)

	for _, tc := range []struct {
		name, matrix string
		count        int
		fails        bool
	}{
		{"amd64_only", `{"include":[{"arch":"amd64"}]}`, 0, false},
		{"arm64_only", `{"include":[{"arch":"arm64"}]}`, 1, false},
		{"both", `{"include":[{"arch":"amd64"},{"arch":"arm64"},{"arch":"arm64"}]}`, 2, false},
		{"malformed", `{`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			log := filepath.Join(root, "calls")
			tool := filepath.Join(root, "forge-ci")
			body := `#!/bin/sh
set -eu
test "${CI_RUNNER_LENDER_TOKEN+x}" != x
test "${lender_token+x}" != x
test "$(cat)" = fixture-token
printf '%s\n' "$*" >> "$CALL_LOG"
`
			require.NoError(t, os.WriteFile(tool, []byte(body), 0o600))
			require.NoError(t, os.Chmod(tool, 0o700))
			cmd := exec.CommandContext(t.Context(), "bash", "-c", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"), "CALL_LOG="+log, "CI_RUNNER_LENDER_TOKEN=fixture-token", "lender_token=previous-export", "SMOKE_MATRIX_JSON="+tc.matrix)

			out, err := cmd.CombinedOutput()
			if tc.fails {
				require.Error(t, err, "%s", out)
			} else {
				require.NoError(t, err, "%s", out)
			}

			if tc.count == 0 {
				require.NoFileExists(t, log)
			} else {
				calls, readErr := os.ReadFile(log)
				require.NoError(t, readErr)
				require.Equal(t, strings.Repeat("platform request-runner --arch arm64 --token-file -\n", tc.count), string(calls))
			}
		})
	}
}

func TestRuntimeFlavoursMatchTheGitHubPublisherAndContainerfile(t *testing.T) {
	t.Parallel()

	var roster []struct {
		Target string   `json:"target"`
		Name   string   `json:"name"`
		Archs  []string `json:"archs"`
	}
	require.NoError(t, json.Unmarshal(reporoot.ReadFile(t, "containers/runtime/images.json"), &roster))
	require.Len(t, roster, 7)
	containerfile := string(reporoot.ReadFile(t, "containers/runtime/Containerfile"))

	github := loadWorkflow(t, ".github/workflows/self-runtime-container.yml")
	for _, name := range []string{"verify", "publish-arch", "publish-merge"} {
		entries := sequenceValues(mappingValue(mappingValue(mappingValue(github.jobs[name].node, "strategy"), "matrix"), "runtime"))
		require.Len(t, entries, len(roster))

		for _, image := range roster {
			found := false

			for _, entry := range entries {
				if scalarString(mappingValue(entry, "target")) == image.Target && scalarString(mappingValue(entry, "name")) == image.Name {
					found = true
				}
			}

			require.True(t, found, "%s omits %s", name, image.Name)
			require.Contains(t, containerfile, " AS "+image.Target+"\n")

			if image.Target == "android" {
				require.Equal(t, []string{"amd64"}, image.Archs)
			} else {
				require.True(t, slices.Equal(image.Archs, []string{"amd64", "arm64"}))
			}
		}
	}
}

func TestRuntimeExampleRequiresExplicitPublicationPolicy(t *testing.T) {
	t.Parallel()

	for _, file := range []string{"runtime-images.yml"} {
		body := string(reporoot.ReadFile(t, "examples/forgejo/"+file))
		require.Contains(t, body, "GIT_DEFAULT_HASH: sha1", file)
		require.Contains(t, body, "vars.RUNTIME_IMAGE_NAMESPACE", file)
		require.Contains(t, body, "vars.COSIGN_PUBLIC_KEY_SHA256", file)
		require.NotContains(t, body, "itiquette", file)

		for _, forbidden := range []string{"182c68718a03dc3f085cab23897f5eb5d8c69d29", "localhost/forge-ci-signer", "--object-format sha256", "object-format: sha256"} {
			require.NotContains(t, body, forbidden, file)
		}

		for line := range strings.SplitSeq(body, "\n") {
			if strings.Contains(line, "uses:") {
				require.Contains(t, line, "https://forge.example/owner/forge-ci/", file)
				require.Contains(t, line, "@FORGE_CI_COMMIT", file)
			}
		}
	}
}
