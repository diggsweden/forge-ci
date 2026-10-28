// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	appconfig "github.com/diggsweden/forge-ci/internal/app/config"
	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/cli/cienv"
	"github.com/diggsweden/forge-ci/internal/cli/deps"
	"github.com/diggsweden/forge-ci/internal/cli/regflags"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
)

const defaultArtifactsFile = ".forge-ci/artifacts.yml"

func releaseImagesPlanCmd() *cli.Command {
	return &cli.Command{
		Name:  "plan",
		Usage: "plan the release images artifacts.yml declares: one build per container and architecture, one publication per container",
		Description: `Reads the containers: section of artifacts.yml and writes three outputs:
builds, a JSON array with one entry per container and architecture for the
native build jobs; images, one entry per container for the job that scans,
assembles and records it; and registry-user, the user the registry token logs
in as there. Each image is published under staging-<final tag>
for the signer to promote; the final tag is the release tag followed by the
container's tag-suffix. A release publishes to one repository, the one its
signer is bound to: --image-repository, or the forge's own registry when it
is empty, so containers are tag variants there and declare no image-name.
An empty --registry-username is the --repository owner on the forge's own
registry; on another registry it is refused here, before any build.

Without an artifacts.yml, a repository releases its own image: one container
named after the repository, built from the root Containerfile with every
default. An artifacts.yml named by --file or ARTIFACTS_CONFIG must exist; an
empty name is no name, as a workflow passes an input left empty.

A consumer with its own image policy can pass --image-plan-json, the same
builds/images contract this planner emits. It is checked against the destination
repository, exact architecture partition, local build paths and the existing
ledger rules before any outputs are emitted. Base entries may use content-derived
tags; their graph and input-ID computation remain consumer policy. This input
does not choose a builder or permit build secrets, and cannot be combined with
an explicit artifacts file.
When RELEASE_PLATFORMS is set, the resolved plan must already match it; the
engine refuses a different frozen selection rather than silently rewriting it.

Declared features the release-images workflow does not carry out (build
secrets, binary extraction, another scan threshold than its CRITICAL,HIGH gate,
platforms other than linux/amd64 and linux/arm64) are refused here, before any
runner starts. RELEASE_PLATFORMS narrows every container's platforms.

EXAMPLE:
   forge-ci container release-images plan --image-repository ghcr.io/owner/app --registry-username owner --release-tag v1.2.3`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "image-plan-json", Sources: cli.EnvVars("IMAGE_PLAN_JSON"), Usage: "resolved consumer image policy as builds/images JSON; admitted against --image-repository instead of loading artifacts.yml; supports content-addressed base tags"},
			&cli.StringFlag{Name: "file", Value: defaultArtifactsFile, Sources: cli.EnvVars("ARTIFACTS_CONFIG"), Usage: "path to the artifacts.yml file declaring the containers; without it and the default file, the root Containerfile is the repository's image"},
			&cli.StringFlag{Name: "image-repository", Sources: cli.EnvVars("IMAGE_REPOSITORY"), Usage: "the one repository the release's images publish to, e.g. ghcr.io/owner/app; empty is the forge's own registry, <server host>/<owner>/<repository>"},
			&cli.StringFlag{Name: flagServerURL, Sources: cienv.ServerURL(), Usage: "forge server URL, whose registry an empty --image-repository names"},
			&cli.StringFlag{Name: flagRepository, Sources: cienv.Repository(), Usage: "owner/repo the image repositories are derived from"},
			// No environment source: the event's ref name, which the tag chain
			// falls back to, is not the release tag on a release-request push.
			&cli.StringFlag{Name: "release-tag", Required: true, Usage: "release tag the image tags are made of, e.g. v1.2.3"},
			&cli.StringFlag{Name: "release-platforms", Sources: cli.EnvVars("RELEASE_PLATFORMS"), Usage: "comma-separated subset of each container's platforms to build; empty builds them all"},
			regflags.UsernameFor("user the registry token logs in as, written to the registry-user output; empty is the --repository owner on the forge's own registry and refused on another one"),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return deps.FromCmd(ctx, cmd, func(dep *deps.Deps) error {
				// A layer passes an input left empty as an empty value, which
				// names no file.
				file := cmd.String("file")
				named := cmd.IsSet("file") && file != ""

				resolved := cmd.String("image-plan-json")
				if resolved != "" && named {
					return fmt.Errorf("image-plan-json and an explicit artifacts file are mutually exclusive: %w", errs.ErrUsage)
				}

				imageRepository, err := releasePlanImageRepository(cmd)
				if err != nil {
					return err
				}

				registryUser, err := releasePlanRegistryUser(cmd, imageRepository)
				if err != nil {
					return err
				}

				plan, err := appconfig.EmitReleaseImagesPlan(ctx, dep.OutputSink, deps.Annotator(cmd), appconfig.EmitReleaseImagesPlanInput{
					Config:            appconfig.EmitConfigPlanInput{Path: cmp.Or(file, defaultArtifactsFile)},
					DefaultWhenAbsent: !named,
					ResolvedPlan:      resolved,
					Images: pipeline.ReleaseImagesInput{
						ImageRepository:  imageRepository,
						Repository:       cmd.String(flagRepository),
						ReleaseTag:       cmd.String("release-tag"),
						ReleasePlatforms: cmd.String("release-platforms"),
					},
				})
				if err != nil {
					return err
				}

				for _, image := range plan.Images {
					_, _ = fmt.Fprintf(os.Stderr, "%s: %s:%s, staged as %s\n", image.Name, image.ImageRepository, image.FinalTagName, image.CandidateTag)
				}

				return dep.OutputSink.Set(ctx, "registry-user", registryUser)
			})
		},
	}
}

// releasePlanRegistryUser is the user the publishing job logs in as, decided
// by the rule the signer's verbs apply, so a release that cannot log in stops
// before it builds.
func releasePlanRegistryUser(cmd *cli.Command, imageRepository string) (string, error) {
	serverHost, err := appcontainer.OptionalRegistryHost(appcontainer.NormalizedServerURL(cmd.String(flagServerURL)))
	if err != nil {
		return "", err
	}

	registry, err := resolveImageRegistry("", serverHost, imageRepository)
	if err != nil {
		return "", err
	}

	// Without a forge to compare with, the owner is no safer a guess.
	registry.Foreign = registry.Foreign || serverHost == ""

	username, err := registryLoginUser(strings.TrimSpace(cmd.String(regflags.FlagUsername)), cmd.String(flagRepository), registry)
	if err != nil {
		return "", fmt.Errorf("release images: %w", err)
	}

	return username, nil
}

// releasePlanImageRepository is --image-repository, or the forge's own
// registry for this repository, as the signer defaults it.
func releasePlanImageRepository(cmd *cli.Command) (string, error) {
	if repository := strings.TrimSpace(cmd.String("image-repository")); repository != "" {
		return repository, nil
	}

	serverHost, err := appcontainer.OptionalRegistryHost(appcontainer.NormalizedServerURL(cmd.String(flagServerURL)))
	if err != nil {
		return "", err
	}

	repository := cmd.String(flagRepository)
	if serverHost == "" || repository == "" {
		return "", fmt.Errorf("release images: --image-repository, or --server-url and --repository for the forge's own registry: %w", errs.ErrUsage)
	}

	return appcontainer.DefaultReleaseImageRepository(serverHost, repository)
}
