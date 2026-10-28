// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package ci

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

const runtimeExtractDouble = `#!/bin/sh
set -eu
case "$1 $2" in
"artifact download")
  if [ "$4" = runtime-smoke-go-amd64 ]; then
    mkdir -p "$6"
    jq --arg source "$SOURCE_SHA" --arg ref "${SMOKE_REF_OVERRIDE:-}" \
      '{source:$source,target:"go",arch:"amd64",ref:(if $ref == "" then .ref else $ref end)}' \
      runtime-go-arch-amd64/runtime-go-amd64.json >"$6/receipt.json"
  fi
  exit 0
  ;;
"container layout")
  test "$3" = extract-files
  test "$4" = --layout-dir
  test "$6" = --output-dir
  test "$8" = --paths
  test "$9" = /usr/local/bin/forge-ci
  mkdir -p "$7"
  cp "$CLI_FIXTURE" "$7/forge-ci"
  chmod 700 "$7/forge-ci"
  ;;
*) exit 99 ;;
esac
`

func TestRuntimePlanSelectsNativePlatforms(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	sha, err := exec.CommandContext(t.Context(), "git", "-C", root, "rev-parse", "HEAD").Output()
	require.NoError(t, err)

	for _, tc := range []struct {
		name, platforms string
		builds, images  int
	}{
		{"amd64", `[{"suffix":"amd64"}]`, 7, 7},
		{"arm64", `[{"suffix":"arm64"}]`, 6, 6},
		{"both", `[{"suffix":"amd64"},{"suffix":"arm64"}]`, 13, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			output := filepath.Join(t.TempDir(), "output")
			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts/ci/runtime-images.sh"), "plan")
			cmd.Dir = root

			cmd.Env = append(os.Environ(), "RUNTIME_IMAGE_NAMESPACE=registry.example/acme", "SOURCE_SHA="+strings.TrimSpace(string(sha)), "RUNTIME_RUN_ID=123-1", "PUBLICATION_REF=refs/heads/main", "PLATFORM_MATRIX="+tc.platforms, "FORGEJO_OUTPUT="+output)
			body, runErr := cmd.CombinedOutput()
			require.NoError(t, runErr, "%s", body)

			data, readErr := os.ReadFile(output)
			require.NoError(t, readErr)

			for _, item := range []struct {
				key   string
				count int
			}{{"build-matrix", tc.builds}, {"image-matrix", tc.images}} {
				var matrix struct {
					Include []map[string]any `json:"include"`
				}

				found := false

				for line := range strings.SplitSeq(string(data), "\n") {
					if value, ok := strings.CutPrefix(line, item.key+"="); ok {
						require.NoError(t, json.Unmarshal([]byte(value), &matrix))

						found = true
					}
				}

				require.True(t, found, "%s", item.key)
				require.Len(t, matrix.Include, item.count)

				for _, leg := range matrix.Include {
					if leg["target"] == "android" && item.key == "build-matrix" {
						require.Equal(t, "amd64", leg["arch"])
					}
				}
			}
		})
	}
}

func TestRuntimePlanRejectsUntrustedPublicationCoordinates(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	sha, err := exec.CommandContext(t.Context(), "git", "-C", root, "rev-parse", "HEAD").Output()
	require.NoError(t, err)

	for _, extra := range []string{"PUBLICATION_REF=refs/heads/topic", "RUNTIME_IMAGE_NAMESPACE=registry.example/acme/../other", "SOURCE_SHA=main", "RUNTIME_RUN_ID=1;bad"} {
		t.Run(extra, func(t *testing.T) {
			t.Parallel()

			output := filepath.Join(t.TempDir(), "output")
			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts/ci/runtime-images.sh"), "plan")
			cmd.Dir = root

			cmd.Env = append(os.Environ(), "RUNTIME_IMAGE_NAMESPACE=registry.example/acme", "SOURCE_SHA="+strings.TrimSpace(string(sha)), "RUNTIME_RUN_ID=123-1", "PUBLICATION_REF=refs/heads/main", `PLATFORM_MATRIX=[{"suffix":"amd64"}]`, "FORGEJO_OUTPUT="+output, extra)
			body, err := cmd.CombinedOutput()
			require.Error(t, err, "%s", body)
			require.NoFileExists(t, output)
		})
	}
}

func TestRuntimeStageCLIRejectsSubstitutedBytes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".engine/bin"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "runtime-cli"), 0o750))

	content := []byte("recorded engine bytes")
	record := fmt.Sprintf("%x  bin/forge-ci\n", sha256.Sum256(content))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".engine/bin/forge-ci-linux-amd64.sha256"), []byte(record), 0o600))
	artifact := filepath.Join(root, "runtime-cli/forge-ci-amd64")
	require.NoError(t, os.WriteFile(artifact, []byte("substituted"), 0o600))

	run := func() ([]byte, error) {
		cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(reporoot.Path(t), "scripts/ci/runtime-images.sh"), "stage-cli", "amd64")
		cmd.Dir = root

		return cmd.CombinedOutput()
	}
	body, err := run()
	require.Error(t, err, "%s", body)
	require.NoFileExists(t, filepath.Join(root, "cli-prebuilt/forge-ci"))
	require.NoError(t, os.WriteFile(artifact, content, 0o600))

	body, err = run()
	require.NoError(t, err, "%s", body)
	got, err := os.ReadFile(filepath.Join(root, "cli-prebuilt/forge-ci"))
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestRuntimeCollectBindsCandidateMetadataBeforeSchedulingSmoke(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, field, value string
	}{
		{name: "valid"},
		{"foreign_registry", "ref", "registry.other/acme/forge-ci-runtime-go-1.27@sha256:" + strings.Repeat("b", 64)},
		{"wrong_digest", "digest", "sha256:" + strings.Repeat("c", 64)},
		{"wrong_arch", "arch", "arm64"},
		{"another_run", "tag", "registry.example/acme/forge-ci-runtime-go-1.27:staging-other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for _, dir := range []string{"containers/runtime", ".engine/bin", "runtime-go-arch-amd64", "tools"} {
				require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o750))
			}

			require.NoError(t, os.WriteFile(filepath.Join(root, "containers/runtime/images.json"), []byte(`[{"target":"go","name":"forge-ci-runtime-go-1.27","archs":["amd64"]}]`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".engine/bin/forge-ci-linux-amd64.sha256"), []byte(strings.Repeat("d", 64)+"  bin/forge-ci\n"), 0o600))
			// The artifact is already placed in the fixture. No forge/registry call.
			require.NoError(t, os.WriteFile(filepath.Join(root, "tools/forge-ci"), []byte("#!/bin/sh\n[ \"$1 $2\" = 'artifact download' ]\n"), 0o600))
			require.NoError(t, os.Chmod(filepath.Join(root, "tools/forge-ci"), 0o700))

			repo := "registry.example/acme/forge-ci-runtime-go-1.27"

			data := map[string]string{"arch": "amd64", "tag": repo + ":staging-123-1-aaaaaaaaaaaa-amd64", "digest": "sha256:" + strings.Repeat("b", 64), "ref": repo + "@sha256:" + strings.Repeat("b", 64)}
			if tc.field != "" {
				data[tc.field] = tc.value
			}

			encoded, err := json.Marshal(data)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "runtime-go-arch-amd64/runtime-go-amd64.json"), encoded, 0o600))
			output := filepath.Join(root, "output")
			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(reporoot.Path(t), "scripts/ci/runtime-images.sh"), "collect")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "tools")+":"+os.Getenv("PATH"), "RUNTIME_IMAGE_NAMESPACE=registry.example/acme", "SOURCE_SHA="+strings.Repeat("a", 40), "RUNTIME_RUN_ID=123-1", `PLATFORM_MATRIX=[{"suffix":"amd64"}]`, "FORGEJO_OUTPUT="+output)

			body, err := cmd.CombinedOutput()
			if tc.field != "" {
				require.Error(t, err, "%s", body)
				require.NoFileExists(t, output)
			} else {
				require.NoError(t, err, "%s", body)

				result, readErr := os.ReadFile(output)
				require.NoError(t, readErr)
				require.Contains(t, string(result), repo+"@sha256:"+strings.Repeat("b", 64))
				require.Contains(t, string(result), strings.Repeat("d", 64))
			}
		})
	}
}

func TestRuntimeScannerAuthIsPrivateAndRemovedOnFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, dir := range []string{"containers/runtime", "runtime-go-arch-amd64", "tools", ".engine/bin"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o750))
	}

	require.NoError(t, os.WriteFile(filepath.Join(root, "containers/runtime/images.json"), []byte(`[{"target":"go","name":"forge-ci-runtime-go-1.27","archs":["amd64"]}]`), 0o600))

	repo := "registry.example/acme/forge-ci-runtime-go-1.27"
	data, err := json.Marshal(map[string]string{"arch": "amd64", "tag": repo + ":staging-123-1-aaaaaaaaaaaa-amd64", "digest": "sha256:" + strings.Repeat("b", 64), "ref": repo + "@sha256:" + strings.Repeat("b", 64)})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "runtime-go-arch-amd64/runtime-go-amd64.json"), data, 0o600))
	auth := filepath.Join(root, "registry-auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"auths":{}}`), 0o600))

	cliFile := filepath.Join(root, "expected-cli")
	cliBytes := []byte("recorded engine bytes")
	require.NoError(t, os.WriteFile(cliFile, cliBytes, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".engine/bin/forge-ci-linux-amd64.sha256"), fmt.Appendf(nil, "%x  bin/forge-ci\n", sha256.Sum256(cliBytes)), 0o600))

	for name, body := range map[string]string{
		"forge-ci": runtimeExtractDouble,
		"skopeo":   "#!/bin/sh\n[ \"$1\" = copy ]\n",
		"syft": `#!/bin/sh
set -eu
mode() { stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"; }
test "$(mode "$DOCKER_CONFIG")" = 700
test "$(mode "$DOCKER_CONFIG/config.json")" = 600
cmp "$REGISTRY_AUTH_FILE" "$DOCKER_CONFIG/config.json"
printf '%s' "$DOCKER_CONFIG" > "$AUTH_DIR_RECORD"
exit 42
`,
	} {
		path := filepath.Join(root, "tools", name)
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		require.NoError(t, os.Chmod(path, 0o700))
	}

	record := filepath.Join(root, "auth-dir")
	cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(reporoot.Path(t), "scripts/ci/runtime-images.sh"), "assemble", "go")
	cmd.Dir = root

	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "tools")+":"+os.Getenv("PATH"), "RUNTIME_IMAGE_NAMESPACE=registry.example/acme", "SOURCE_SHA="+strings.Repeat("a", 40), "RUNTIME_RUN_ID=123-1", `PLATFORM_MATRIX=[{"suffix":"amd64"}]`, "RUNNER_TEMP="+root, "REGISTRY_AUTH_FILE="+auth, "AUTH_DIR_RECORD="+record, "CLI_FIXTURE="+cliFile)
	body, err := cmd.CombinedOutput()
	require.Error(t, err, "%s", body)
	require.Equal(t, 42, cmd.ProcessState.ExitCode())

	privateDir, err := os.ReadFile(record)
	require.NoError(t, err)
	require.NoDirExists(t, string(privateDir))
	require.NoFileExists(t, filepath.Join(root, "evidence/config.json"))
	require.FileExists(t, auth, "the login owner, not the scanner bridge, owns the original auth file")

	// A substituted binary must stop assembly before even starting the scanner.
	require.NoError(t, os.Remove(record))
	require.NoError(t, os.WriteFile(cliFile, []byte("substituted CLI"), 0o600))
	retry := exec.CommandContext(t.Context(), "bash", filepath.Join(reporoot.Path(t), "scripts/ci/runtime-images.sh"), "assemble", "go")
	retry.Dir = root
	retry.Env = cmd.Env
	body, err = retry.CombinedOutput()
	require.Error(t, err, "%s", body)
	require.Contains(t, string(body), "baked CLI does not match source checksum")
	require.NoFileExists(t, record, "scanner must not run after the byte-identity refusal")

	// Rebuilt bytes with a green smoke job for the old digest must not reach
	// scanning or index publication, even though the dependency stayed green.
	require.NoError(t, os.WriteFile(cliFile, cliBytes, 0o600))
	stale := exec.CommandContext(t.Context(), "bash", filepath.Join(reporoot.Path(t), "scripts/ci/runtime-images.sh"), "assemble", "go")
	stale.Dir = root
	stale.Env = append(stale.Env, cmd.Env...)
	stale.Env = append(stale.Env, "SMOKE_REF_OVERRIDE="+repo+"@sha256:"+strings.Repeat("c", 64))
	body, err = stale.CombinedOutput()
	require.Error(t, err, "%s", body)
	require.Contains(t, string(body), "does not match its successful smoke receipt")
	require.NoFileExists(t, record, "scanner must not run for candidates without matching smoke evidence")

	leftovers, err := filepath.Glob(filepath.Join(root, "runtime-registry.*"))
	require.NoError(t, err)
	require.Empty(t, leftovers)
}

func TestRuntimeRejectsMalformedOrEmptyPlatformMatrices(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	sha, err := exec.CommandContext(t.Context(), "git", "-C", root, "rev-parse", "HEAD").Output()
	require.NoError(t, err)

	for _, mode := range []string{"plan", "collect"} {
		for _, matrix := range []string{"invalid-json", "[]", "{}", "null", `[{"suffix":"s390x"}]`, `[{"suffix":"amd64"},{"suffix":"amd64"}]`} {
			t.Run(mode+"/"+matrix, func(t *testing.T) {
				t.Parallel()
				tmp := t.TempDir()
				output := filepath.Join(tmp, "output")
				called := filepath.Join(tmp, "called")
				tool := filepath.Join(tmp, "forge-ci")
				require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\nprintf called >\"$CALL_MARKER\"\nexit 99\n"), 0o600))
				require.NoError(t, os.Chmod(tool, 0o700))
				cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts/ci/runtime-images.sh"), mode)
				cmd.Dir = root

				cmd.Env = append(os.Environ(), "PATH="+tmp+":"+os.Getenv("PATH"), "CALL_MARKER="+called, "RUNTIME_IMAGE_NAMESPACE=registry.example/acme", "SOURCE_SHA="+strings.TrimSpace(string(sha)), "RUNTIME_RUN_ID=123-1", "PUBLICATION_REF=refs/heads/main", "PLATFORM_MATRIX="+matrix, "FORGEJO_OUTPUT="+output)
				body, runErr := cmd.CombinedOutput()
				require.Error(t, runErr, "%s", body)
				require.NoFileExists(t, output, "invalid data must not produce even partial job outputs")
				require.NoFileExists(t, called, "invalid data must be rejected before any artifact operation")
			})
		}
	}
}

func TestRuntimeBakedCLIIsCheckedWithoutExecutingCandidateTools(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		substituted bool
	}{{"recorded_bytes", false}, {"substituted_bytes", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for _, dir := range []string{".engine/bin", "tools"} {
				require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o750))
			}
			// Executing these bytes would create the marker and fail the test.
			expected := []byte("#!/bin/sh\nprintf executed >\"$EXECUTION_MARKER\"\nexit 99\n")

			candidate := expected
			if tc.substituted {
				candidate = []byte("substituted image CLI")
			}

			cliFile := filepath.Join(root, "candidate-cli")
			require.NoError(t, os.WriteFile(cliFile, candidate, 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".engine/bin/forge-ci-linux-amd64.sha256"), fmt.Appendf(nil, "%x  bin/forge-ci\n", sha256.Sum256(expected)), 0o600))

			for name, script := range map[string]string{"forge-ci": runtimeExtractDouble, "skopeo": "#!/bin/sh\n[ \"$1\" = copy ]\n"} {
				path := filepath.Join(root, "tools", name)
				require.NoError(t, os.WriteFile(path, []byte(script), 0o600))
				require.NoError(t, os.Chmod(path, 0o700))
			}

			marker := filepath.Join(root, "executed")
			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(reporoot.Path(t), "scripts/ci/runtime-images.sh"), "verify-cli", "amd64", "registry.example/acme/runtime@sha256:"+strings.Repeat("b", 64))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "tools")+":"+os.Getenv("PATH"), "CLI_FIXTURE="+cliFile, "RUNNER_TEMP="+root, "REGISTRY_AUTH_FILE="+filepath.Join(root, "unused-auth"), "EXECUTION_MARKER="+marker)

			body, err := cmd.CombinedOutput()
			if tc.substituted {
				require.Error(t, err, "%s", body)
				require.Contains(t, string(body), "baked CLI does not match")
			} else {
				require.NoError(t, err, "%s", body)
			}

			require.NoFileExists(t, marker)

			leftovers, err := filepath.Glob(filepath.Join(root, "runtime-cli-check.*"))
			require.NoError(t, err)
			require.Empty(t, leftovers, "temporary image bytes must be cleaned on success and failure")
		})
	}
}
