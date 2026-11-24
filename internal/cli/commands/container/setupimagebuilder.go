// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"os"
	"runtime"

	"github.com/urfave/cli/v3"

	"github.com/diggsweden/forge-ci/internal/adapters/apt"
	"github.com/diggsweden/forge-ci/internal/adapters/buildah"
	"github.com/diggsweden/forge-ci/internal/adapters/ociregistry"
	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/cli/cienv"
	"github.com/diggsweden/forge-ci/internal/cli/deps"
	"github.com/diggsweden/forge-ci/internal/cli/regflags"
)

func setupImageBuilderCmd() *cli.Command {
	return &cli.Command{
		Name:  "setup-image-builder",
		Usage: "set up Buildah or admit a digest-pinned native Kaniko job image",
		Description: `--image-builder buildah (the default; CI_IMAGE_BUILDER sets it) installs
missing buildah, fuse-overlayfs, netavark and aardvark-dns packages when
requested, writes a job-local containers storage config, prefers
overlay/fuse-overlayfs only after buildah info and an optional layered build
probe succeed, and falls back to vfs. Emits CONTAINERS_STORAGE_CONF and TMPDIR
to the runner env file for later CI steps.

Networking helpers are required independently of the storage driver. netavark
provides container networking and aardvark-dns provides its DNS support. Helpers
in /usr/lib/podman or /usr/libexec/podman are recognized even outside PATH.
With --install-packages=false, provision these runtime packages in the image;
probe failures retain Buildah's diagnostic for missing helpers.

The temp directory is an existing scratch parent, which may be a shared /tmp:
the storage goes into a new private directory created under it. Storage
overrides are reset in place, so they need a job-specific temp directory, not
shared /tmp or /var/tmp, and must remain below it. The runner env file may live
anywhere but must be a regular file. Unsafe paths are refused before package
installation or Buildah invocation.

--image-builder kaniko installs nothing: Kaniko is already provisioned in the
job image, so verify its digest, native architecture and engine source/title
labels.

EXAMPLE:
   forge-ci container setup-image-builder --image-builder buildah --extra-packages "jq skopeo" --summary`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "extra-packages", Sources: cli.EnvVars("SETUP_BUILDAH_EXTRA_PACKAGES"), Usage: "whitespace-separated additional apt packages to install"},
			&cli.BoolFlag{Name: "install-packages", Value: true, Sources: cli.EnvVars("SETUP_BUILDAH_INSTALL_PACKAGES"), Usage: "install missing buildah/fuse-overlayfs/netavark/aardvark-dns/extra packages with apt-get"},
			&cli.BoolFlag{Name: "probe-build", Value: true, Sources: cli.EnvVars("SETUP_BUILDAH_PROBE_BUILD"), Usage: "validate storage with a layered build probe that writes into a directory its base image owns"},
			&cli.BoolFlag{Name: "print-store", Sources: cli.EnvVars("CONTAINER_STORAGE_PRINT_STORE"), Usage: "print selected buildah storage details after setup"},
			&cli.BoolFlag{Name: "summary", Sources: cli.EnvVars("CONTAINER_STORAGE_SUMMARY"), Usage: "append selected storage details to the step summary"},
			&cli.StringFlag{Name: "storage-conf", Sources: cli.EnvVars("CONTAINERS_STORAGE_CONF"), Usage: "containers storage config path (default: $RUNNER_TEMP/containers-storage.conf)"},
			&cli.StringFlag{Name: "storage-root", Sources: cli.EnvVars("CONTAINER_STORAGE_ROOT"), Usage: "containers storage root path (default: $RUNNER_TEMP/containers-storage)"},
			&cli.StringFlag{Name: "container-tmpdir", Sources: cli.EnvVars("CONTAINER_TMPDIR"), Usage: "job-local temp directory (default: $RUNNER_TEMP/container-tmp)"},
			&cli.StringFlag{Name: flagTempDir, Sources: cienv.TempDir(), Usage: "existing scratch parent for a private storage directory; job-specific when storage overrides are set (required)"},
			&cli.StringFlag{Name: "env-file", Sources: cli.EnvVars("FORGEJO_ENV", "GITHUB_ENV"), Usage: "runner env file receiving CONTAINERS_STORAGE_CONF and TMPDIR"},
			&cli.BoolFlag{Name: "require-job-image", Usage: "require a digest-pinned job image for either backend (GitLab release policy)"},
			&cli.StringFlag{Name: flagImageBuilder, Value: imageBuilderBuildah, Sources: cli.EnvVars(envImageBuilder), Usage: imageBuilderUsage},
			&cli.StringFlag{Name: "job-image", Sources: cli.EnvVars("CI_IMAGE_BUILDER_JOB_IMAGE"), Usage: "Kaniko job image pinned by digest"},
			&cli.StringFlag{Name: "arch", Value: runtime.GOARCH, Usage: "architecture the Kaniko job image must serve: the job's build architecture (default: this binary's)"},
			&cli.StringFlag{Name: "engine-repository", Sources: cli.EnvVars("FORGE_CI_REPOSITORY_URL"), Usage: "trusted engine source repository HTTPS URL"},
			regflags.AuthFile(regflags.AuthFileOpts{Usage: "registry auth file for Kaniko job-image inspection"}),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return deps.FromCmd(ctx, cmd, func(dep *deps.Deps) error {
				return appcontainer.SetupImageBuilder(ctx, buildah.New(), apt.New(), ociregistry.WithAuthFile(cmd.String(flagAuthFile)), dep.OutputSink, dep.SummarySink, os.Stderr, appcontainer.SetupImageBuilderInput{
					Builder: cmd.String(flagImageBuilder), JobImage: cmd.String("job-image"),
					EngineRepository: cmd.String("engine-repository"), Arch: cmd.String("arch"), Buildah: setupBuildahInput(cmd), RequireJobImage: cmd.Bool("require-job-image"),
				})
			})
		},
	}
}

func setupBuildahInput(cmd *cli.Command) appcontainer.SetupBuildahInput {
	return appcontainer.SetupBuildahInput{
		ExtraPackages: cmd.String("extra-packages"), InstallPackages: cmd.Bool("install-packages"),
		ProbeBuild: cmd.Bool("probe-build"), PrintStore: cmd.Bool("print-store"), WriteSummary: cmd.Bool("summary"),
		StorageConf: cmd.String("storage-conf"), StorageRoot: cmd.String("storage-root"),
		TmpDir: cmd.String("container-tmpdir"), RunnerTemp: cmd.String(flagTempDir), EnvFile: cmd.String("env-file"),
	}
}
