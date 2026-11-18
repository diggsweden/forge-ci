// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func TestRuntimeImagesUseCanonicalDeliveryArtifacts(t *testing.T) {
	t.Parallel()

	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(reporoot.ReadFile(t, ".github/workflows/self-runtime-container.yml"), &workflow))

	for _, job := range []string{"verify", "platforms", "publish-arch"} {
		downloads, stageChecks, bakedChecks := 0, 0, 0

		for _, step := range workflow.Jobs[job].Steps {
			if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
				require.Equal(t, "engine-cli", step.With["name"], job)

				downloads++
			}

			if strings.Contains(step.Run, "engine-binary.sh stage") {
				require.Contains(t, step.Run, "engine-cli cli-prebuilt/forge-ci", job)
				require.NotContains(t, step.Run, "install -m0755 engine-cli", job)
				require.NotContains(t, step.Run, "tar -x", job)

				stageChecks++
			}

			if step.Name == "Verify baked forge-ci runs" || step.Name == "Verify baked CLI == published binary (build-once gate)" {
				require.Contains(t, step.Run, "bash scripts/ci/engine-binary.sh verify", job)

				bakedChecks++
			}
		}

		require.Equal(t, 1, downloads, job)
		require.Equal(t, 1, stageChecks, job)

		if job != "platforms" {
			require.Equal(t, 1, bakedChecks, "SDK qualification must verify actual baked bytes: "+job)
		}
	}

	build := string(reporoot.ReadFile(t, ".github/workflows/build-cli.yml"))
	require.Contains(t, build, `bash scripts/ci/engine-binary.sh build "$arch"`)
	require.Contains(t, build, `bash scripts/ci/engine-binary.sh verify "$arch"`)
	require.Contains(t, build, "name: engine-cli")
	require.Contains(t, build, "path: engine-cli/")
}

func TestRuntimeNativeSubsetsDriveVerificationBuildsAndMerge(t *testing.T) {
	t.Parallel()
	var workflow struct {
		Jobs map[string]struct {
			Needs    []string `yaml:"needs"`
			If       string   `yaml:"if"`
			Runner   string   `yaml:"runs-on"`
			Strategy struct {
				Matrix string `yaml:"matrix"`
			} `yaml:"strategy"`
		}
	}
	body := reporoot.ReadFile(t, ".github/workflows/self-runtime-container.yml")
	require.NoError(t, yaml.Unmarshal(body, &workflow))
	for _, name := range []string{"verify", "publish-arch"} {
		job := workflow.Jobs[name]
		require.Contains(t, job.Needs, "platforms")
		require.Equal(t, "${{ fromJSON(needs.platforms.outputs.build-matrix) }}", job.Strategy.Matrix)
		require.Equal(t, "${{ matrix.arch == 'arm64' && 'ubuntu-26.04-arm' || 'ubuntu-26.04' }}", job.Runner)
	}
	require.Contains(t, workflow.Jobs["publish-merge"].Needs, "platforms")
	require.Equal(t, "${{ fromJSON(needs.platforms.outputs.image-matrix) }}", workflow.Jobs["publish-merge"].Strategy.Matrix)
	require.Empty(t, workflow.Jobs["platforms"].If, "verification also needs a plan on PR and verify-only dispatch")
	require.Contains(t, string(body), "container runtime-publication matrix")
	require.Contains(t, string(body), "BUILD_PLATFORM: linux/${{ matrix.arch }}")
	require.Contains(t, string(body), "engine-binary.sh verify '${{ matrix.arch }}'")
	require.NotContains(t, string(body), "matrix.runtime")
	require.NotContains(t, string(body), "matrix.platform")
	require.Contains(t, string(body), "RUNTIME_IMAGE_JSON: ${{ toJSON(matrix) }}")
	require.Contains(t, string(body), "container runtime-publication download-digests")
	require.NotContains(t, string(body), "--pattern")
}
