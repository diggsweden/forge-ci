// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	appconfig "github.com/diggsweden/forge-ci/internal/app/config"
	"github.com/diggsweden/forge-ci/internal/domain/output"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// Run 660 published successfully; the ad-hoc driver then selected the binary
// extraction declaration for image-only readback. Keep the engine's refusal:
// readback must use the same signed-source declaration as the publishing job.
func TestWorkspaceReadbackUsesThePublishingDeclaration(t *testing.T) {
	t.Parallel()
	const consumer = "test/live/fixtures/consumers/hello-cargo-workspace/consumer"
	var workflow struct {
		Jobs map[string]struct {
			With map[string]string
		}
	}
	require.NoError(t, yaml.Unmarshal(reporoot.ReadFile(t, consumer+"/.forgejo/workflows/release-workflow.yml"), &workflow))
	declaration := workflow.Jobs["release-images"].With["artifacts-config"]
	require.Equal(t, ".forge-ci/release-images.yml", declaration)
	var found bool
	for _, c := range ForgejoPreparedCases {
		if c.Demo == "hello-cargo-workspace" && c.Event == TrialRelease && c.ReleaseImagesConfig != "" {
			require.Equal(t, declaration, c.ReleaseImagesConfig)
			found = true
		}
	}
	require.True(t, found, "canonical workspace release must remain covered")
	input := appconfig.EmitReleaseImagesPlanInput{
		Config: appconfig.EmitConfigPlanInput{Path: filepath.Join(reporoot.Path(t), consumer, DefaultArtifactsConfig)},
		Images: pipeline.ReleaseImagesInput{ImageRepository: "registry.example/owner/workspace", Repository: "owner/workspace", ReleaseTag: "v0.2.0", ReleasePlatforms: "linux/amd64"},
	}
	_, err := appconfig.ResolveReleaseImagesPlan(input, output.Annotator{})
	require.ErrorContains(t, err, "declares extract")
	input.Config.Path = filepath.Join(reporoot.Path(t), consumer, declaration)
	plan, err := appconfig.ResolveReleaseImagesPlan(input, output.Annotator{})
	require.NoError(t, err)
	require.Len(t, plan.Images, 2)
	require.Len(t, plan.Builds, 2)
	for _, image := range plan.Images {
		require.Equal(t, "registry.example/owner/workspace", image.ImageRepository)
		require.Equal(t, "amd64", image.Archs)
	}
}

func TestForgejoGPGFilesCaseIncludesIndependentImageReadback(t *testing.T) {
	t.Parallel()
	const consumer = "test/live/fixtures/consumers/hello-go-service/consumer"
	var workflow struct {
		Jobs map[string]struct {
			With map[string]string
		}
	}
	require.NoError(t, yaml.Unmarshal(reporoot.ReadFile(t, consumer+"/.forgejo/workflows/release-workflow.yml"), &workflow))
	declaration := workflow.Jobs["release-images"].With["artifacts-config"]
	require.NotEmpty(t, declaration)
	var found bool
	for _, c := range ForgejoPreparedCases {
		if c.Lane == LaneGPGFiles && c.Demo == "hello-go-service" {
			require.Equal(t, ForgejoGPGFilesFixtures, c.Fixtures)
			require.Equal(t, declaration, c.ReleaseImagesConfig)
			found = true
		}
	}
	require.True(t, found)
	plan, err := appconfig.ResolveReleaseImagesPlan(appconfig.EmitReleaseImagesPlanInput{
		Config: appconfig.EmitConfigPlanInput{Path: filepath.Join(reporoot.Path(t), consumer, declaration)},
		Images: pipeline.ReleaseImagesInput{ImageRepository: "registry.example/owner/service", Repository: "owner/service", ReleaseTag: "v0.2.0", ReleasePlatforms: "linux/amd64"},
	}, output.Annotator{})
	require.NoError(t, err)
	require.Len(t, plan.Images, 1)
	require.Equal(t, "my-service", plan.Images[0].Name)
	require.Equal(t, "registry.example/owner/service", plan.Images[0].ImageRepository)
}
