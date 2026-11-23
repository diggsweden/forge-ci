// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/diggsweden/forge-ci/internal/adapters/ociregistry"
	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/cli/regflags"
	domaincontainer "github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/urfave/cli/v3"
)

func releaseBuildInputFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "build-json", Required: true, Usage: "one build entry from the admitted release image plan"},
		&cli.StringFlag{Name: flagReleaseSHA, Required: true, Usage: "prepared release source commit"},
		&cli.StringFlag{Name: flagServerURL, Required: true, Usage: "source forge URL"},
		&cli.StringFlag{Name: flagRepository, Required: true, Usage: "source owner/repository"},
		&cli.StringFlag{Name: flagInputsDir, Required: true, Usage: "relative run-scoped directory holding only fetched build inputs"},
	}
}

func releaseBuildFromJSON(cmd *cli.Command) (pipeline.ReleaseImageBuild, error) {
	var build pipeline.ReleaseImageBuild

	decoder := json.NewDecoder(strings.NewReader(cmd.String("build-json")))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&build); err != nil {
		return build, fmt.Errorf("invalid planned image build: %w", errs.ErrUsage)
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return build, fmt.Errorf("planned image build must contain exactly one JSON object: %w", errs.ErrUsage)
	}

	if err := pipeline.ValidateRegistryInputs(build); err != nil {
		return build, err
	}

	if !filepath.IsLocal(cmd.String(flagInputsDir)) {
		return build, fmt.Errorf("inputs-dir must be a safe relative directory: %w", errs.ErrUsage)
	}

	return build, nil
}

func releaseImagesFetchBuildInputsCmd() *cli.Command {
	return &cli.Command{
		Name: "fetch-build-inputs", Usage: "fetch planned cache and pinned parents without running consumer code",
		Description: "--inputs-dir must not exist yet; its missing parents are created, so a build's inputs/<name>-arch-<arch> can be named directly.",
		Flags:       append(append(releaseBuildInputFlags(), registryPullFlags()...), parentBaseProjectionFlag(false)),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			build, err := releaseBuildFromJSON(cmd)
			if err != nil {
				return err
			}

			if cmd.IsSet(flagParentBaseJSON) {
				if err := validateBuildParentProjection(cmd, build); err != nil {
					return err
				}
			}

			input := releaseImageBuildInput(cmd, build)
			input.AuthFile = cmd.String(flagAuthFile)

			registry, err := registryForInputPull(cmd, []pipeline.ReleaseImageBuild{build})
			if err != nil {
				return err
			}

			return appcontainer.FetchReleaseBuildInputs(ctx, registry, os.Stderr, input, build.RegistryInputs, cmd.String(flagInputsDir))
		},
	}
}

func validateBuildParentProjection(cmd *cli.Command, build pipeline.ReleaseImageBuild) error {
	parent, err := childBaseParent(cmd)
	if err != nil {
		return err
	}

	if !domaincontainer.ValidSHA256Hex(parent.InputID) || len(build.RegistryInputs) != 1 || build.CacheTag != "" {
		return fmt.Errorf("parent-base-json requires a valid input_id, exactly one planned parent and no architecture cache: %w", errs.ErrInvalidConfig)
	}

	for _, ref := range build.RegistryInputs {
		if ref != parent.Ref {
			return fmt.Errorf("planned digest-pinned parent does not match parent-base-json: %w", errs.ErrInvalidConfig)
		}
	}

	return nil
}

func registryPullFlags() []cli.Flag {
	return []cli.Flag{
		regflags.AuthFile(regflags.AuthFileOpts{}),
		&cli.StringFlag{Name: "parent-registry-repository", Usage: "exact repository authorized for the pull-only credential"},
		&cli.StringFlag{Name: "parent-registry-auth-origin", Usage: "optional trusted HTTPS origin for a separate registry token service"},
		&cli.StringFlag{Name: "parent-registry-user", Usage: "username for the pull-only credential"},
		&cli.StringFlag{Name: "parent-registry-password-file", Usage: "pull-only credential file; - reads stdin"},
	}
}

func registryForInputPull(cmd *cli.Command, builds []pipeline.ReleaseImageBuild) (*ociregistry.Adapter, error) {
	authFile := cmd.String(flagAuthFile)
	if !cmd.IsSet("parent-registry-repository") && !cmd.IsSet("parent-registry-user") && !cmd.IsSet("parent-registry-password-file") && !cmd.IsSet("parent-registry-auth-origin") {
		if authFile != "" {
			return ociregistry.WithAuthFile(authFile), nil
		}

		return ociregistry.NewAnonymous(), nil
	}

	repository, username, path := cmd.String("parent-registry-repository"), strings.TrimSpace(cmd.String("parent-registry-user")), cmd.String("parent-registry-password-file")
	for _, build := range builds {
		if err := validateParentPullScope(build, repository, username, path, authFile); err != nil {
			return nil, err
		}
	}

	password, err := regflags.FileOrEnv(path)
	if err != nil {
		return nil, err
	}

	if password == "" {
		return nil, fmt.Errorf("parent-registry-password-file must supply a pull credential: %w", errs.ErrUsage)
	}

	return ociregistry.WithRepositoryPullCredentials(repository, username, password, cmd.String("parent-registry-auth-origin"))
}

func validateParentPullScope(build pipeline.ReleaseImageBuild, repository, username, path, authFile string) error {
	if !domaincontainer.ValidRepositoryName(repository) || username == "" || path == "" || authFile != "" {
		return fmt.Errorf("authenticated parent pulls require parent-registry-repository, parent-registry-user and parent-registry-password-file, without auth-file: %w", errs.ErrUsage)
	}

	if len(build.RegistryInputs) != 1 || build.CacheTag != "" {
		return fmt.Errorf("a scoped pull requires exactly one parent and no architecture cache: %w", errs.ErrInvalidConfig)
	}

	for _, ref := range build.RegistryInputs {
		if parent, _, _ := strings.Cut(ref, "@"); parent != repository {
			return fmt.Errorf("registry input does not match parent-registry-repository: %w", errs.ErrInvalidConfig)
		}
	}

	return nil
}

func releaseImagesBuildPreparedCmd() *cli.Command {
	return &cli.Command{
		Name: "build-prepared", Usage: "import verified parents and build using only secret-free prepared registry inputs",
		Flags: append(append(releaseBuildInputFlags(), imageBuilderFlags()...),
			&cli.StringFlag{Name: flagReleaseTag, Required: true, Usage: "prepared release tag"},
			&cli.StringFlag{Name: flagCreated, Required: true, Usage: "source commit timestamp"},
			&cli.StringFlag{Name: flagSourceDir, Value: "source", Usage: "consumer checkout"},
			&cli.StringFlag{Name: flagMetadataDir, Value: ".", Usage: "relative architecture layout parent directory"}),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			build, err := releaseBuildFromJSON(cmd)
			if err != nil {
				return err
			}

			builder, err := imageBuilderFromCmd(cmd)
			if err != nil {
				return err
			}

			if err := validateReleaseImageBuilder(builder, build, true); err != nil {
				return err
			}

			return stagePreparedReleaseImageBuild(ctx, cmd, builder, build, cmd.String(flagInputsDir))
		},
	}
}

// Both job verbs use the same selected-builder policy. The native batch calls
// this for its whole share before executing any build; the app rechecks identity
// at the actual build/cache-import boundary.
func validateReleaseImageBuilder(builder imageBuilder, build pipeline.ReleaseImageBuild, prepared bool) error {
	if err := appcontainer.CheckSignerImageBuilderIdentity(signerImageTool{builder}, build.BuilderMaterialID); err != nil {
		return err
	}

	if len(build.RegistryInputs) == 0 {
		return nil
	}

	if !prepared {
		return fmt.Errorf("release image %s requires secret-free fetched parents: pass --inputs-dir: %w", build.Name, errs.ErrUsage)
	}

	return nil
}

func stagePreparedReleaseImageBuild(ctx context.Context, cmd *cli.Command, builder imageBuilder, build pipeline.ReleaseImageBuild, directory string) error {
	// The parents loaded for this build are its own, also when it fails
	// before it runs.
	defer builder.DiscardRegistryInputs()

	_, err := appcontainer.StageReleaseBuildWithInputs(ctx, signerImageTool{builder}, builder, ociregistry.NewAnonymous(), os.Stderr, releaseImageBuildInput(cmd, build), build.RegistryInputs, directory)

	return err
}
