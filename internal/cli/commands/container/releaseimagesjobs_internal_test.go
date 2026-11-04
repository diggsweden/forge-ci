// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/imageledger"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
)

const releaseJobsArtifacts = `containers:
  - name: app
    container-file: Containerfile
    context: .
    platforms: linux/amd64,linux/arm64
`

func releaseJobsPlanArgs(extra ...string) []string {
	return append([]string{
		"plan", "--file", ".forge-ci/artifacts.yml", "--image-repository", "registry.example/group/app",
		"--repository", "group/app", "--registry-username", "gitlab-ci-token", "--release-tag", "v1.2.3",
	}, extra...)
}

func writeReleaseJobsArtifacts(t *testing.T) {
	t.Helper()

	env := testenv.New(t)
	t.Chdir(env.Temp)
	require.NoError(t, os.MkdirAll(".forge-ci", 0o700))
	require.NoError(t, os.WriteFile(".forge-ci/artifacts.yml", []byte(releaseJobsArtifacts), 0o600))
	require.NoError(t, os.WriteFile("Containerfile", []byte("FROM scratch\n"), 0o600))
	t.Setenv("RELEASE_PLATFORMS", "")
	t.Setenv("IMAGE_PLAN_JSON", "")
}

// The plan file is the job verbs' transport: what plan --output writes,
// build and publish read back and admit against the same destination and tag.
func TestReleaseImagesPlanOutput_RoundTripsThroughTheJobVerbs(t *testing.T) {
	writeReleaseJobsArtifacts(t)

	require.NoError(t, releaseImagesPlanCmd().Run(t.Context(), releaseJobsPlanArgs("--output", "plan.json", "--runner-archs", "amd64,arm64")))

	var plan pipeline.ReleaseImagesPlan

	reader := &cli.Command{
		Name:  "read",
		Flags: releaseImagesJobFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			var err error

			plan, err = readReleaseImagesPlanFile(cmd)

			return err
		},
	}
	require.NoError(t, reader.Run(t.Context(), []string{"read", "--plan", "plan.json", "--image-repository", "registry.example/group/app", "--release-tag", "v1.2.3", "--release-sha", "abc"}))
	require.Len(t, plan.Builds, 2)
	require.Len(t, plan.Images, 1)
	require.Equal(t, "staging-v1.2.3", plan.Images[0].CandidateTag)

	// Another destination is refused: the plan is not trusted for it.
	err := reader.Run(t.Context(), []string{"read", "--plan", "plan.json", "--image-repository", "registry.example/other/app", "--release-tag", "v1.2.3", "--release-sha", "abc"})
	require.ErrorIs(t, err, errs.ErrInvalidConfig)
}

// A container whose architecture has no native build job stops at the plan,
// before any runner builds.
func TestReleaseImagesPlanOutput_RefusesAnArchitectureWithoutABuildJob(t *testing.T) {
	writeReleaseJobsArtifacts(t)

	err := releaseImagesPlanCmd().Run(t.Context(), releaseJobsPlanArgs("--output", "plan.json", "--runner-archs", "amd64"))
	require.ErrorIs(t, err, errs.ErrInvalidConfig)
	require.ErrorContains(t, err, "linux/arm64")
	require.NoFileExists(t, "plan.json")

	t.Setenv("RELEASE_PLATFORMS", "linux/amd64")
	require.NoError(t, releaseImagesPlanCmd().Run(t.Context(), releaseJobsPlanArgs("--output", "plan.json", "--runner-archs", "amd64")))

	err = releaseImagesPlanCmd().Run(t.Context(), releaseJobsPlanArgs("--runner-archs", "amd64"))
	require.ErrorIs(t, err, errs.ErrUsage)
}

// What the job verbs do not carry out stops the plan before any image job
// runs, and is refused again when a job reads the plan.
func TestReleaseImagesPlanFile_RefusesWhatTheJobVerbsDoNotCarryOut(t *testing.T) {
	for name, plan := range map[string]pipeline.ReleaseImagesPlan{
		"base image":          {Images: []pipeline.ReleaseImage{{Name: "base", ImageKind: string(imageledger.ImageKindBase)}}},
		"builder material ID": {Builds: []pipeline.ReleaseImageBuild{{Name: "app", Arch: "amd64", BuilderMaterialID: "sha256:x"}}},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")

			require.ErrorIs(t, writeReleaseImagesPlanFile(path, plan, ""), errs.ErrInvalidConfig)
			require.NoFileExists(t, path)
			require.ErrorIs(t, refuseUnsupportedJobPlan(plan), errs.ErrInvalidConfig)
		})
	}
}

// releaseImageBuildInputFor is the build input of build under the release
// job verbs' flags.
func releaseImageBuildInputFor(t *testing.T, build pipeline.ReleaseImageBuild) appcontainer.SignerImageBuildArchInput {
	t.Helper()

	var input appcontainer.SignerImageBuildArchInput

	cmd := &cli.Command{
		Name: "build",
		Flags: append(releaseImagesJobFlags(),
			&cli.StringFlag{Name: flagSourceDir, Value: "."},
			&cli.StringFlag{Name: flagCreated},
			&cli.StringFlag{Name: flagMetadataDir},
		),
		Action: func(_ context.Context, cmd *cli.Command) error {
			input = releaseImageBuildInput(cmd, build)

			return nil
		},
	}
	require.NoError(t, cmd.Run(t.Context(), []string{
		"build", "--plan", "p", "--image-repository", "r", "--release-tag", "v1.2.3", "--release-sha", "abc",
		"--server-url", "https://gitlab.example/", "--repository", "group/app", "--created", "2026-01-02T03:04:05Z",
		"--source-dir", "consumer", "--metadata-dir", "staged",
	}))

	return input
}

// Each planned build is the build-arch --stage call the Forgejo workflow's
// build step makes: the same tag, paths and labels.
func TestReleaseImageBuildInput_IsTheForgejoBuildStep(t *testing.T) {
	build := pipeline.ReleaseImageBuild{
		Name: "app", Arch: "amd64", ContainerFile: "Containerfile", Context: ".", Target: "runtime",
		BuildArgs: "A=1\nB=2", Labels: "org.opencontainers.image.licenses=MIT",
		ImageRepository: "registry.example/group/app", CandidateTag: "staging-v1.2.3", FinalTagName: "v1.2.3",
	}

	in := releaseImageBuildInputFor(t, build)
	require.Equal(t, "abc", in.SourceSHA)
	require.Equal(t, "staging-v1.2.3", in.TagName)
	require.Equal(t, "runtime", in.Target)
	require.Equal(t, filepath.Join("consumer", "Containerfile"), in.Containerfile)
	require.Equal(t, "consumer", in.Context)
	require.Equal(t, filepath.Join("staged", "app-arch-amd64"), in.MetadataDir)
	require.Equal(t, []string{"A=1", "B=2"}, in.BuildArgs)
	require.Equal(t, []string{
		"org.opencontainers.image.version=v1.2.3",
		"org.opencontainers.image.url=https://gitlab.example/group/app",
		"org.opencontainers.image.documentation=https://gitlab.example/group/app#readme",
		"org.opencontainers.image.ref.name=v1.2.3",
		"org.opencontainers.image.created=2026-01-02T03:04:05Z",
		"org.opencontainers.image.licenses=MIT",
	}, in.Labels)

	build.Revision = "def"
	in = releaseImageBuildInputFor(t, build)
	require.Equal(t, "def", in.SourceSHA)
}

// Each architecture's job builds that architecture's planned builds alone, in
// plan order; an architecture the plan does not build leaves its job no work.
func TestReleaseImageBuildsOf_IsTheArchitecturesShareOfThePlan(t *testing.T) {
	plan := pipeline.ReleaseImagesPlan{Builds: []pipeline.ReleaseImageBuild{
		{Name: "app", Arch: "amd64"}, {Name: "app", Arch: "arm64"}, {Name: "worker", Arch: "amd64"},
	}}

	require.Equal(t, []pipeline.ReleaseImageBuild{{Name: "app", Arch: "amd64"}, {Name: "worker", Arch: "amd64"}}, releaseImageBuildsOf(plan, "amd64"))
	require.Equal(t, []pipeline.ReleaseImageBuild{{Name: "app", Arch: "arm64"}}, releaseImageBuildsOf(plan, "arm64"))

	plan.Builds = plan.Builds[:1]
	require.Empty(t, releaseImageBuildsOf(plan, "arm64"))
}
