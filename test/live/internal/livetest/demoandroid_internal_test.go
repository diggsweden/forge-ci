// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/config"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	domainrelease "github.com/diggsweden/forge-ci/internal/domain/release"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// Kotlin checks must run on Kotlin code, not a Java fixture in a Kotlin image.
func TestAndroidDemosUseKotlinSourcesAndLintImage(t *testing.T) {
	t.Parallel()

	for demo, module := range map[string]string{"hello-android": "app", "hello-android-library": "lib"} {
		t.Run(demo, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(reporoot.Path(t), "test/live/fixtures/consumers", demo, "consumer")

			for _, sourceSet := range []string{"main", "test"} {
				count := 0
				err := filepath.WalkDir(filepath.Join(root, module, "src", sourceSet), func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}

					require.NotEqual(t, ".java", filepath.Ext(path), "Android demos are Kotlin, including tests")

					if !entry.IsDir() && filepath.Ext(path) == ".kt" {
						count++
					}

					return nil
				})
				require.NoError(t, err)
				require.Positive(t, count, sourceSet)
			}

			plan, err := os.ReadFile(filepath.Join(root, "nanolinter.toml"))
			require.NoError(t, err)
			require.Contains(t, string(plan), `checks = ["kotlin-ktlint", "kotlin-detekt", "secrets", "sast"]`)
			require.Contains(t, string(plan), `block = ["kotlin-ktlint", "kotlin-detekt", "secrets"]`)

			build, err := os.ReadFile(filepath.Join(root, module, "build.gradle.kts"))
			require.NoError(t, err)
			require.Contains(t, string(build), "JvmTarget.JVM_17")
			require.NotContains(t, string(build), `id("org.jetbrains.kotlin.android")`)

			workflow, err := os.ReadFile(filepath.Join(root, "test/live/github/workflows/pullrequest-workflow.yml"))
			require.NoError(t, err)
			require.Contains(t, string(workflow), "nanolinter:kotlin@sha256:")

			found := false

			for _, c := range GitLabPreparedCases {
				if c.Demo == demo && c.Lane == LanePrivateAndroid {
					found = true

					require.Equal(t, flavorKotlin, c.JobImage)
					require.Equal(t, "android", c.Runtime)
				}
			}

			require.True(t, found, "missing prepared Android gate")
			require.NotContains(t, string(plan), "java-")
		})
	}
}

// The app builds its APKs and AAB, the library its AAR: neither signs, and
// both run their tests.
func TestAndroidForgejoGatesSeparateLintAndSDKBuild(t *testing.T) {
	t.Parallel()

	for demo, tasks := range map[string]string{
		"hello-android":         "app:test assembleDemoDebug assembleDemoRelease bundleDemoRelease",
		"hello-android-library": "lib:test lib:lintRelease lib:assembleRelease",
	} {
		t.Run(demo, func(t *testing.T) {
			t.Parallel()
			body := reporoot.ReadFile(t, "test/live/fixtures/consumers/"+demo+"/consumer/.forgejo/workflows/pullrequest-workflow.yml")
			require.NotContains(t, string(body), "secrets.")

			var workflow struct {
				Jobs map[string]struct {
					Container struct{ Image, Options string }
					Needs     []string
					Steps     []struct{ Uses, Run string }
				}
			}
			require.NoError(t, yaml.Unmarshal(body, &workflow))
			require.Equal(t, "${{ vars.CI_JOB_IMAGE }}", workflow.Jobs["lint"].Container.Image)
			require.Equal(t, "${{ vars.CI_JOB_IMAGE_ANDROID }}", workflow.Jobs["build"].Container.Image)
			require.Equal(t, "--user 65532:65532", workflow.Jobs["build"].Container.Options)
			require.Contains(t, workflow.Jobs["build"].Steps[1].Run, "./gradlew --no-daemon "+tasks)
			require.ElementsMatch(t, []string{"lint", "build"}, workflow.Jobs["summary"].Needs)

			found := false

			for _, c := range ForgejoPreparedCases {
				if c.Demo == demo && c.Lane == LanePrivateAndroid {
					found = true

					require.Equal(t, flavorKotlin, c.JobImage)
					require.Equal(t, "android", c.Runtime)
					require.Equal(t, TrialPullRequest, c.Event)
				}
			}

			require.True(t, found)
		})
	}
}

func TestAndroidDemosBindBothJobsToPreparedEngine(t *testing.T) {
	t.Parallel()

	for demo, variant := range map[string][2]string{
		"hello-android":         {"include-aab", "true"},
		"hello-android-library": {"library", "true"},
	} {
		t.Run(demo, func(t *testing.T) {
			t.Parallel()

			body := reporoot.ReadFile(t, "test/live/fixtures/consumers/"+demo+"/consumer/test/live/github/workflows/pullrequest-workflow.yml")
			rendered, _, err := RenderGitHubConsumer("pullrequest-workflow.yml", string(body), trialEngine, nil)
			require.NoError(t, err)

			var workflow struct {
				Jobs map[string]struct {
					Uses string
					If   string
					With map[string]string
				}
			}
			require.NoError(t, yaml.Unmarshal([]byte(rendered), &workflow))

			for job, leaf := range map[string]string{"pr-checks": "pullrequest-orchestrator.yml", "build": "build-gradle-android.yml"} {
				call, ok := workflow.Jobs[job]
				require.True(t, ok, job)
				require.Equal(t, trialEngine.Owner+"/"+trialEngine.Repository+"/.github/workflows/"+leaf+"@"+trialEngine.Commit, call.Uses)
				require.Equal(t, trialEngine.Commit, call.With["forge-ci-sha"], "missing binary ref would resolve latest stable instead of the prepared engine")
			}

			build := workflow.Jobs["build"]
			require.Equal(t, "${{ github.event_name == 'workflow_dispatch' }}", build.If)
			require.Equal(t, "ghcr.io/example-org/forge-ci-runtime-android-35@"+trialEngine.Digests["forge-ci-runtime-android-35"], build.With["runtime-image"])
			require.Equal(t, "false", build.With["enable-signing"])
			require.Equal(t, "false", build.With["skip-tests"])
			require.Equal(t, variant[1], build.With[variant[0]])
		})
	}
}

// The library's release as its declaration plans it and the hosted harness
// reads it: the build uploads the AAR under the artifact's name and the
// exported Maven publication beside it, the names the publish stage downloads
// by; the declared GPG method keeps the trial from rekeying the release to
// Cosign; the oracle reads a Maven package back; and the fixture hands the
// orchestrator the Android image of the build and the Java image of the
// publication.
func TestAndroidLibraryDemoReleasePublishesItsMavenPublication(t *testing.T) {
	t.Parallel()

	consumer := "test/live/fixtures/consumers/hello-android-library/consumer/"
	declaration := string(reporoot.ReadFile(t, consumer+".forge-ci/artifacts.yml"))
	fixture := string(reporoot.ReadFile(t, consumer+"test/live/github/workflows/release-workflow.yml"))

	cfg, err := parseServiceDeclaration(declaration)
	require.NoError(t, err)

	plan := pipeline.NewConfigPlan(cfg)
	require.Len(t, plan.Artifacts.ForgePackages, 1)

	library := plan.Artifacts.ForgePackages[0]
	require.Equal(t, config.BuildTypeLibrary, library.BuildType)
	require.Equal(t, "hello-android-library", library.BuildArtifactName, "the AAR's workflow artifact")
	require.Equal(t, "hello-android-library-maven-publications", library.PublicationArtifactName)
	require.Equal(t, domainrelease.SignMethodGPG, plan.Sign.Method)
	require.False(t, plan.Sign.RequiresSigstoreEgress)
	require.False(t, ReleaseWritesPublicLog(declaration), "without a declared GPG method the trial signs with its Cosign key instead")

	staged := t.TempDir()
	writeFile(t, filepath.Join(staged, ".forge-ci", "artifacts.yml"), declaration)
	writeFile(t, filepath.Join(staged, ".github", "workflows", "release-workflow.yml"), fixture)

	maven, err := githubReleasesMavenPackages(staged, "release-workflow.yml")
	require.NoError(t, err)
	require.True(t, maven, "the release oracle must read the Maven package back")

	rendered, _, err := RenderGitHubConsumer("release-workflow.yml", fixture, trialEngine, nil)
	require.NoError(t, err)

	var workflow struct {
		Jobs map[string]struct {
			Uses string
			With map[string]string
		}
	}
	require.NoError(t, yaml.Unmarshal([]byte(rendered), &workflow))

	release := workflow.Jobs["release"]
	require.Equal(t, trialEngine.Owner+"/"+trialEngine.Repository+"/.github/workflows/release-orchestrator.yml@"+trialEngine.Commit, release.Uses)

	for input, image := range map[string]string{"runtime-image-android": "forge-ci-runtime-android-35", "runtime-image-java": "forge-ci-runtime-java-25"} {
		require.Equal(t, "ghcr.io/example-org/"+image+"@"+trialEngine.Digests[image], release.With[input])
	}
}
