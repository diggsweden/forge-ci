// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/app/gitlabpipeline"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/domain/projecttype"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

const androidAARConsumer = "test/live/fixtures/consumers/hello-android-library/consumer/"

func TestAndroidAARGitLabReleaseChecksMatchTheMergeRequest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "gradlew"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0700))
	script := filepath.Join(reporoot.Path(t), androidAARConsumer, ".gitlab/check-release.sh")
	cmd := exec.CommandContext(t.Context(), "/bin/bash", script)
	cmd.Dir = root
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.Equal(t, "--no-daemon lib:test lib:lintRelease lib:assembleRelease\n", string(out))
	gate := reporoot.ReadFile(t, androidAARConsumer+".gitlab/merge-request.yml")
	require.Contains(t, string(gate), strings.TrimSpace("./gradlew "+string(out)))
}

func TestAndroidAARForgejoReleaseKeepsSDKBuildKeyFree(t *testing.T) {
	t.Parallel()
	body := reporoot.ReadFile(t, androidAARConsumer+".forgejo/workflows/release-workflow.yml")
	var workflow struct {
		Jobs map[string]map[string]any
	}
	require.NoError(t, yaml.Unmarshal(body, &workflow))
	build := workflow.Jobs["build"]
	require.Equal(t, "${{ needs.prepare.result == 'success' }}", build["if"])
	encoded, err := yaml.Marshal(build)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secrets.")
	require.NotContains(t, string(encoded), "KEYSTORE")
	container := build["container"].(map[string]any)
	require.Equal(t, "${{ vars.CI_JOB_IMAGE_ANDROID }}", container["image"])
	require.Equal(t, "--user 65532:65532", container["options"])
	prepare := workflow.Jobs["prepare"]
	sign := workflow.Jobs["sign-and-publish"]
	require.Equal(t, "${{ needs.prepare.result == 'success' && needs.build.result == 'success' }}", sign["if"])
	var checkoutFound bool
	for _, raw := range build["steps"].([]any) {
		step := raw.(map[string]any)
		if step["name"] == "Checkout prepared release" {
			env := step["env"].(map[string]any)
			require.Equal(t, "${{ needs.prepare.outputs.release-sha }}", env["CHECKOUT_REF"])
			require.Equal(t, "${{ forgejo.token }}", env["FORGEJO_TOKEN"])
			checkoutFound = true
		}
	}
	require.True(t, checkoutFound, "build must check out the prepared release with private-repository authentication")
	require.Contains(t, prepare["uses"], "/prepare-release.yml@")
	require.Contains(t, sign["uses"], "/sign-and-publish-release.yml@")
	with := sign["with"].(map[string]any)
	require.Equal(t, "${{ needs.build.outputs.dist-digest }}", with["dist-digest"])
	require.Equal(t, "${{ needs.prepare.outputs.artifacts-config }}", with["artifacts-config"])
}

// Stand-ins exercise only the fixture's handoff wiring. They are not an
// Android compilation, SBOM generation, signature or publication acceptance.
func TestAndroidAARReleaseScriptStagesUnsignedFiles(t *testing.T) {
	t.Parallel()
	script := filepath.Join(reporoot.Path(t), androidAARConsumer, ".gitlab/build-release.sh")
	root, tools := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(tools, path), []byte(body), 0700))
	}
	write("git", "#!/bin/sh\nprintf '%s\\n' \"${FIXTURE_HEAD}\"\n")
	write("forge-ci", `#!/bin/sh
set -eu
printf '%s\n' "$*" >>calls
case "$1 $2 $3" in
  'build gradle-android run')
    mkdir -p lib/build/outputs/aar build/reports/cyclonedx
    printf 'fixture AAR bytes\n' >lib/build/outputs/aar/lib-release.aar
    printf '{"bomFormat":"CycloneDX"}\n' >build/reports/cyclonedx/bom.json
    ;;
  'release notes --changelog-file') printf 'release notes\n' >dist/release-notes.md ;;
  'release files declare') ;;
  'publish forge-packages android-release')
    if [ -n "${FIXTURE_PUBLICATIONS:-}" ]; then
      case "$4" in
        --list) printf '%s\n' "$FIXTURE_PUBLICATIONS" ;;
        --list-files) printf '%s\n' "dist/$FIXTURE_PUBLICATIONS/com/example/library/0.2.0/library-0.2.0.pom" "dist/$FIXTURE_PUBLICATIONS/com/example/library/0.2.0/library-0.2.0.aar" ;;
      esac
    fi
    ;;
  'build gradle export-publications')
    mkdir -p "dist/$FIXTURE_PUBLICATIONS/com/example/library/0.2.0"
    printf 'fixture exported AAR\n' >"dist/$FIXTURE_PUBLICATIONS/com/example/library/0.2.0/library-0.2.0.aar"
    ;;
  *) exit 99 ;;
esac
`)
	publications := ""
	run := func(family, head string) error {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/bin/bash", script)
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + tools + ":/usr/bin:/bin", "HOME=" + root, "FIXTURE_HEAD=" + head, "RELEASE_TAG=v0.2.0", "RELEASE_VERSION=0.2.0", "RELEASE_SHA=prepared", "FORGE_CI_BUILD_FAMILY=" + family, "FIXTURE_PUBLICATIONS=" + publications}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("fixture refused: %s", out)
		}
		return err
	}
	families, err := gitlabpipeline.PlanReleaseFamilies(pipeline.ReleasePrepareStagePlan{}, pipeline.ReleaseBuildStagePlan{
		Targets: pipeline.ReleaseBuildTargets{GradleAndroid: pipeline.TargetPlan[pipeline.PlannedArtifact]{
			Runs: true, Items: []pipeline.PlannedArtifact{{Name: "hello-android-library", ProjectType: projecttype.GradleAndroid, WorkingDirectory: "."}},
		}},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"android"}, families.Build)
	family := families.Build[0]
	require.Error(t, run(family, "foreign"))
	_, err = os.Stat(filepath.Join(root, "calls"))
	require.True(t, os.IsNotExist(err), "foreign source must fail before build effects")
	require.NoError(t, run(family, "prepared"))
	require.NoError(t, run("", "prepared"))
	aar, err := os.ReadFile(filepath.Join(root, "dist/hello-android-library-0.2.0.aar"))
	require.NoError(t, err)
	require.Equal(t, "fixture AAR bytes\n", string(aar))
	sbom, err := os.ReadFile(filepath.Join(root, "dist/hello-android-library-0.2.0.cdx.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"bomFormat":"CycloneDX"}`, string(sbom))
	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	require.NoError(t, err)
	require.Contains(t, string(calls), "--library --build-module lib")
	require.Contains(t, string(calls), "--enable-signing=false")
	require.Contains(t, string(calls), "release files declare --asset dist/*.aar --sbom dist/hello-android-library-0.2.0.cdx.json")
	for _, name := range strings.Fields("*.asc *.bundle *.sig") {
		matches, err := filepath.Glob(filepath.Join(root, "dist", name))
		require.NoError(t, err)
		require.Empty(t, matches, "the producer must not manufacture signer files")
	}
	require.Error(t, run("rust", "prepared"))
	publications = "hello-android-library-maven-publications"
	require.NoError(t, run(family, "prepared"))
	require.NoError(t, run("", "prepared"))
	calls, err = os.ReadFile(filepath.Join(root, "calls"))
	require.NoError(t, err)
	require.Contains(t, string(calls), "build gradle export-publications --project :lib --output dist/hello-android-library-maven-publications --version 0.2.0")
	require.Contains(t, string(calls), "--asset dist/hello-android-library-maven-publications/com/example/library/0.2.0/library-0.2.0.pom")
}

func TestAndroidAARFilesFixtureKeepsMavenPublicationExplicit(t *testing.T) {
	t.Parallel()
	root := filepath.Join(reporoot.Path(t), androidAARConsumer)
	changelog, err := GitLabChangelogInputs(root)
	require.NoError(t, err)
	require.Empty(t, changelog, "use the native git-cliff default, not absent git-chglog configs")
	for _, name := range []string{"keepachangelog.toml", "minimal.toml"} {
		require.FileExists(t, filepath.Join(root, ".forge-ci/gitcliff", name))
	}
	read := func(path string) map[string]any {
		var config map[string]any
		require.NoError(t, yaml.Unmarshal(reporoot.ReadFile(t, androidAARConsumer+path), &config))
		return config
	}
	full := read(".forge-ci/artifacts.yml")
	files := read("test/live/aar-files/.forge-ci/artifacts.yml")
	fullArtifact := full["artifacts"].([]any)[0].(map[string]any)
	fileArtifact := files["artifacts"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"forge-packages"}, fullArtifact["publish-to"])
	require.NotContains(t, fileArtifact, "publish-to")
	delete(fullArtifact, "publish-to")
	require.Equal(t, full, files, "file-only selection must preserve build and signing policy")
	workflow := reporoot.ReadFile(t, androidAARConsumer+".forgejo/workflows/release-workflow.yml")
	require.Contains(t, string(workflow), "artifacts-config: ${{ vars.CI_ARTIFACTS_CONFIG || 'test/live/aar-files/.forge-ci/artifacts.yml' }}")
	require.Contains(t, string(workflow), "FORGE_CI_BUILD_FAMILY=android")
}
