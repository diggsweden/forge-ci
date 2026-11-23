// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	appconfig "github.com/diggsweden/forge-ci/internal/app/config"
	apppublish "github.com/diggsweden/forge-ci/internal/app/publish"
	apprelease "github.com/diggsweden/forge-ci/internal/app/release"
	"github.com/diggsweden/forge-ci/internal/cli/deps"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

const (
	outputAndroidMavenPackages = "android-maven-packages"
	// valueFalse is the literal an output or a token-configured flag holds.
	valueFalse = "false"
)

func forgePackagesAndroidReleaseCmd() *cli.Command {
	return &cli.Command{
		Name: "android-release", Usage: "admit or publish the policy release's exported Android Maven libraries",
		Description: "Selects only explicit Android libraries declaring forge-packages. SDK producers export each repository into dist/<publication_artifact_name> and declare its files before signing. --list prints the planned publication artifact names, without credentials or effects. --admit checks the declaration and optional unsigned manifest without resolving a registry; normal publication requires a finalized manifest and an exact version. The trusted caller must authenticate the signed dist handoff first. No project lifecycle is run.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: flagArtifactsFile, Value: defaultArtifactsFile, Usage: "release artifacts declaration"},
			&cli.StringFlag{Name: flagManifest, Usage: "release file manifest, required for publication"},
			&cli.StringFlag{Name: flagDistDir, Value: "dist", Usage: "release dist directory"},
			&cli.StringFlag{Name: flagVersion, Sources: cli.EnvVars("RELEASE_VERSION"), Usage: "exact immutable release version"},
			&cli.BoolFlag{Name: flagAdmit, Usage: "validate without a registry or credential"},
			&cli.BoolFlag{Name: "list", Usage: "print planned publication artifact names only"},
			&cli.BoolFlag{Name: "list-files", Usage: "validate exported repositories and print their release asset paths for declaration; requires --version"},
			&cli.StringFlag{Name: flagTokenConfigured, Usage: "with --admit: true or false, whether the package credential is configured"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return deps.FromCmd(ctx, cmd, func(dep *deps.Deps) error { return publishAndroidRelease(ctx, cmd, dep) })
		},
	}
}

func publishAndroidRelease(ctx context.Context, cmd *cli.Command, dep *deps.Deps) (result error) {
	configured, err := androidReleaseTokenConfigured(cmd)
	if err != nil {
		return err
	}

	plans, err := appconfig.LoadReleaseStagePlans(appconfig.EmitConfigPlanInput{Path: cmd.String(flagArtifactsFile)}, deps.Annotator(cmd), !cmd.IsSet(flagArtifactsFile))
	if err != nil {
		return err
	}

	artifacts, err := pipeline.ReleaseAndroidMavenArtifacts(plans.Build)
	if err != nil {
		return err
	}

	if cmd.Bool("list") {
		return printAndroidPublications(artifacts)
	}

	if cmd.Bool("list-files") {
		return printAndroidReleaseFiles(cmd, artifacts)
	}

	done, err := androidReleaseNeedsNoFiles(ctx, cmd, dep, artifacts, configured)
	if done || err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "forge-ci-android-release-*")
	if err != nil {
		return err
	}

	defer func() { result = errors.Join(result, os.RemoveAll(work)) }()

	version := strings.TrimPrefix(cmd.String(flagVersion), "v")
	if err := stageAndroidPublications(ctx, cmd, artifacts, work, version); err != nil {
		return err
	}

	if cmd.Bool(flagAdmit) {
		return dep.OutputSink.Set(ctx, outputAndroidMavenPackages, "true")
	}

	return uploadAndroidPublications(ctx, dep, artifacts, work, version)
}

// androidReleaseTokenConfigured checks the flag combinations and returns the
// token-configured value.
func androidReleaseTokenConfigured(cmd *cli.Command) (string, error) {
	if (cmd.Bool("list") || cmd.Bool("list-files")) && (cmd.Bool(flagAdmit) || cmd.String(flagManifest) != "" || cmd.IsSet(flagTokenConfigured) || (cmd.Bool("list") && cmd.Bool("list-files"))) {
		return "", fmt.Errorf("--list cannot admit or publish files: %w", errs.ErrUsage)
	}

	if cmd.IsSet(flagTokenConfigured) && !cmd.Bool(flagAdmit) {
		return "", fmt.Errorf("--token-configured requires --admit: %w", errs.ErrUsage)
	}

	configured := cmd.String(flagTokenConfigured)
	if configured != "" && configured != "true" && configured != valueFalse {
		return "", fmt.Errorf("--token-configured takes true or false: %w", errs.ErrUsage)
	}

	return configured, nil
}

func printAndroidPublications(artifacts []pipeline.PlannedArtifact) error {
	for _, artifact := range artifacts {
		if _, err := fmt.Fprintln(os.Stdout, artifact.PublicationArtifactName); err != nil {
			return err
		}
	}

	return nil
}

func printAndroidReleaseFiles(cmd *cli.Command, artifacts []pipeline.PlannedArtifact) error {
	if cmd.String(flagVersion) == "" {
		return fmt.Errorf("--list-files requires --version: %w", errs.ErrUsage)
	}

	var paths []string

	for _, artifact := range artifacts {
		directory := filepath.Join(cmd.String(flagDistDir), artifact.PublicationArtifactName)

		set, err := apppublish.InspectMavenPublication(directory, strings.TrimPrefix(cmd.String(flagVersion), "v"), false)
		if err != nil {
			return err
		}

		for _, name := range set.Files {
			paths = append(paths, filepath.Join(directory, name))
		}
	}

	for _, path := range paths {
		if _, err := fmt.Fprintln(os.Stdout, path); err != nil {
			return err
		}
	}

	return nil
}

// androidReleaseNeedsNoFiles settles the cases that end before any file is
// read, and reports whether the command is done.
func androidReleaseNeedsNoFiles(ctx context.Context, cmd *cli.Command, dep *deps.Deps, artifacts []pipeline.PlannedArtifact, configured string) (bool, error) {
	if len(artifacts) == 0 {
		if cmd.Bool(flagAdmit) {
			return true, dep.OutputSink.Set(ctx, outputAndroidMavenPackages, valueFalse)
		}

		return true, nil
	}

	if cmd.Bool(flagAdmit) && configured == valueFalse {
		return true, fmt.Errorf("the Android Maven publication requires the release package token: %w", errs.ErrPermissionDenied)
	}

	if cmd.Bool(flagAdmit) && cmd.String(flagManifest) == "" {
		return true, dep.OutputSink.Set(ctx, outputAndroidMavenPackages, "true")
	}

	if cmd.String(flagManifest) == "" || cmd.String(flagVersion) == "" {
		return true, fmt.Errorf("the Android Maven release files require --manifest and --version: %w", errs.ErrUsage)
	}

	return false, nil
}

// stageAndroidPublications validates every repository before the first
// registry operation.
func stageAndroidPublications(ctx context.Context, cmd *cli.Command, artifacts []pipeline.PlannedArtifact, work, version string) error {
	files := apprelease.FilesInput{DistDir: cmd.String(flagDistDir), ManifestFile: cmd.String(flagManifest), ManifestExplicit: true}

	for _, artifact := range artifacts {
		directory := filepath.Join(files.DistDir, artifact.PublicationArtifactName)
		destination := filepath.Join(work, artifact.PublicationArtifactName)

		var err error
		if cmd.Bool(flagAdmit) {
			err = apppublish.AdmitReleaseMavenPublication(ctx, files, directory, destination, version)
		} else {
			err = apppublish.StageReleaseMavenPublication(ctx, files, directory, destination, version)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func uploadAndroidPublications(ctx context.Context, dep *deps.Deps, artifacts []pipeline.PlannedArtifact, work, version string) error {
	resolver, err := dep.RequireForgeMavenRegistryResolver()
	if err != nil {
		return err
	}

	if verifier, ok := resolver.(provider.ForgeProjectVerifier); ok {
		if err := verifier.VerifyForgeProject(ctx); err != nil {
			return err
		}
	}

	for _, artifact := range artifacts {
		if err := uploadForgeMaven(ctx, resolver, filepath.Join(work, artifact.PublicationArtifactName), version, false); err != nil {
			return err
		}
	}

	return nil
}
