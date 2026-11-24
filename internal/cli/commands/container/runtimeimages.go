// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/diggsweden/forge-ci/internal/adapters/ociregistry"
	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/cli/deps"
	"github.com/diggsweden/forge-ci/internal/cli/regflags"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func runtimeImagesCmd() *cli.Command {
	return &cli.Command{
		Name: "runtime-images", Usage: "source-bound native runtime publication",
		Commands: []*cli.Command{runtimeMatrixCmd(), runtimeDigestsCmd(), runtimeImagesPlanCmd(), runtimeImagesCollectCmd(), runtimeImagesSignCmd(), runtimeImagesPromoteCmd(), runtimeImagesVerifySmokeCmd(), runtimeImagesVerifyCLICmd()},
	}
}

func runtimeMatrixCmd() *cli.Command {
	return &cli.Command{
		Name: "matrix", Usage: "select native build and nonempty image matrices without publication authority",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "roster", Value: "containers/runtime/images.json", Usage: "product-owned runtime roster JSON"},
			&cli.StringFlag{Name: flagPlatforms, Value: "linux/amd64,linux/arm64", Usage: "declared native platforms"},
			&cli.StringFlag{Name: flagReleasePlatforms, Usage: "optional subset of the declared platforms"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return deps.FromCmd(ctx, cmd, func(dep *deps.Deps) error {
				return appcontainer.PlanRuntimeMatrix(ctx, dep.OutputSink, cmd.String("roster"), cmd.String(flagPlatforms), cmd.String(flagReleasePlatforms))
			})
		},
	}
}

func runtimeDigestsCmd() *cli.Command {
	return &cli.Command{
		Name: "download-digests", Usage: "require each selected architecture's exact runtime digest artifact before merge",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "image-json", Required: true, Usage: "one image-matrix row from runtime-images matrix"},
			&cli.StringFlag{Name: "dir", Required: true, Usage: "new directory for the complete digest marker set"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return deps.FromCmd(ctx, cmd, func(dep *deps.Deps) error {
				downloader, err := dep.RequireRunArtifactDownloader()
				if err != nil {
					return err
				}

				return appcontainer.DownloadRuntimeDigests(ctx, downloader, cmd.String("image-json"), cmd.String("dir"))
			})
		},
	}
}

func runtimeImagesVerifyCLICmd() *cli.Command {
	return &cli.Command{
		Name: "verify-cli", Usage: "verify downloaded or baked CLI bytes against the source checksum without executing them",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: flagArch, Required: true, Usage: "amd64 or arm64"},
			&cli.StringFlag{Name: "workspace", Value: ".", Usage: "source checkout containing .engine/bin checksum recordings"},
			&cli.StringFlag{Name: flagFile, Usage: "downloaded CLI file (exclusive with image-ref)"},
			&cli.StringFlag{Name: flagImageRef, Usage: "digest-pinned retained runtime image (exclusive with file)"},
			regflags.AuthFile(regflags.AuthFileOpts{}),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return appcontainer.VerifyRuntimeCLI(ctx, ociregistry.WithAuthFile(cmd.String(flagAuthFile)), cmd.String("workspace"), cmd.String(flagArch), cmd.String(flagFile), cmd.String(flagImageRef))
		},
	}
}

func runtimeImagesPlanCmd() *cli.Command {
	return &cli.Command{
		Name: "plan", Usage: "validate the complete product roster and emit native build/image matrices",
		Flags: append(runtimeImagesFlags(),
			&cli.StringFlag{Name: flagTarget, Usage: "project one admitted target as JSON instead of emitting workflow matrices"},
			&cli.StringFlag{Name: flagArch, Usage: "with target: require and select one declared native architecture"},
			&cli.StringFlag{Name: "metadata-root", Usage: "with target: validate all selected native metadata under this directory before emitting candidate refs"}),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.String(flagTarget) != "" {
				return appcontainer.PlanRuntimeTarget(ctx, os.Stdout, runtimeImagesInput(cmd), cmd.String(flagTarget), cmd.String(flagArch), cmd.String("metadata-root"))
			}

			if cmd.IsSet(flagArch) || cmd.IsSet("metadata-root") {
				return fmt.Errorf("runtime architecture/metadata projection requires --target: %w", errs.ErrUsage)
			}

			return deps.FromCmd(ctx, cmd, func(dep *deps.Deps) error {
				return appcontainer.PlanRuntimePublication(ctx, dep.OutputSink, runtimeImagesInput(cmd))
			})
		},
	}
}

func runtimeImagesCollectCmd() *cli.Command {
	return &cli.Command{
		Name: "collect", Usage: "download and validate native candidate metadata before emitting the smoke matrix",
		Flags: runtimeImagesFlags(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return deps.FromCmd(ctx, cmd, func(dep *deps.Deps) error {
				downloader, err := dep.RequireRunArtifactDownloader()
				if err != nil {
					return err
				}

				return appcontainer.CollectRuntimePublication(ctx, downloader, dep.OutputSink, ".", runtimeImagesInput(cmd))
			})
		},
	}
}

func runtimeImagesFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "roster", Value: "containers/runtime/images.json", Usage: "product-owned runtime roster JSON"},
		&cli.StringFlag{Name: "platform-matrix", Required: true, Usage: "matrix-json from container platform-plan"},
		&cli.StringFlag{Name: "image-namespace", Required: true, Usage: "destination registry/owner namespace"},
		&cli.StringFlag{Name: flagSourceSHA, Required: true, Usage: "expected full product source commit"},
		&cli.StringFlag{Name: "checked-out-sha", Required: true, Usage: "observed checkout commit"},
		&cli.StringFlag{Name: "run-id", Required: true, Usage: "numeric run-attempt identity, for example 123-1"},
		&cli.StringFlag{Name: "publication-ref", Required: true, Usage: "full ref of the source being built"},
		runtimeImagesBranchFlag(),
	}
}

func runtimeImagesBranchFlag() cli.Flag {
	return &cli.StringFlag{Name: "publication-branch", Value: "main", Sources: cli.EnvVars("RUNTIME_PUBLICATION_BRANCH"), Usage: "protected publication branch selected by the reviewed caller"}
}

func runtimeImagesInput(cmd *cli.Command) appcontainer.RuntimePublicationInput {
	return appcontainer.RuntimePublicationInput{
		RosterFile: cmd.String("roster"), PlatformMatrix: cmd.String("platform-matrix"),
		Namespace: cmd.String("image-namespace"), SourceSHA: cmd.String(flagSourceSHA),
		CheckedOutSHA: cmd.String("checked-out-sha"), RunID: cmd.String("run-id"), Ref: cmd.String("publication-ref"),
		PublicationBranch: cmd.String("publication-branch"),
	}
}

func runtimeManifestInput(cmd *cli.Command) appcontainer.SignerImageAssembleInput {
	return appcontainer.SignerImageAssembleInput{
		AuthFile: cmd.String(flagAuthFile), SourceSHA: cmd.String(flagSourceSHA),
		ServerURL: cmd.String(flagServerURL), Repository: cmd.String(flagRepository),
		ImageRepository: cmd.String(flagImageRepository), RepositorySuffix: cmd.String("repository-suffix"),
		TagPrefix: cmd.String("tag-prefix"), TagName: cmd.String("tag"),
		Name: cmd.String(flagName), Archs: splitSignerLines(cmd.String(flagArchs)),
	}
}
