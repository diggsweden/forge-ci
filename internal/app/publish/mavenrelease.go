// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	apprelease "github.com/diggsweden/forge-ci/internal/app/release"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provenance"
	domainpublish "github.com/diggsweden/forge-ci/internal/domain/publish"
	"github.com/diggsweden/forge-ci/internal/pathsafe"
)

// StageReleaseMavenPublication selects only declared, checksum-bound publication
// bytes from a finalized release. Signer bundles stay release evidence, not Maven
// repository files. No project code, toolchain or credential is used here.
// The caller must first authenticate the signer's handoff, as for release publish.
func StageReleaseMavenPublication(ctx context.Context, files apprelease.FilesInput, directory, destination, version string) error {
	return stageReleaseMavenPublication(ctx, files, directory, destination, version, true)
}

// AdmitReleaseMavenPublication validates the unsigned producer handoff using the
// same selection and byte checks as publication, without a registry or token.
func AdmitReleaseMavenPublication(ctx context.Context, files apprelease.FilesInput, directory, destination, version string) error {
	return stageReleaseMavenPublication(ctx, files, directory, destination, version, false)
}

func stageReleaseMavenPublication(ctx context.Context, files apprelease.FilesInput, directory, destination, version string, finalized bool) (result error) {
	if err := domainpublish.ValidateMavenVersion(version, false); err != nil {
		return err
	}

	if !files.ManifestExplicit || files.ManifestFile == "" || files.DistDir == "" {
		return fmt.Errorf("an explicit release manifest and dist directory are required: %w", errs.ErrUsage)
	}

	relative, err := filepath.Rel(files.DistDir, directory)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return fmt.Errorf("publication directory must be below the release dist directory: %w", errs.ErrValidation)
	}

	assets, err := releaseMavenAssets(files, finalized)
	if err != nil {
		return err
	}

	digests, err := releaseChecksumDigests(files)
	if err != nil {
		return err
	}

	root, err := pathsafe.OpenRoot(files.DistDir)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, root.Close()) }()

	stage, err := pathsafe.NewArtifactStaging(destination)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, stage.Close()) }()

	if err := stageReleaseMavenAssets(ctx, root, stage.Root(), assets, digests, directory, relative); err != nil {
		return err
	}

	if _, err := InspectMavenPublication(stage.Root().Name(), version, false); err != nil {
		return err
	}

	return stage.Install()
}

func releaseMavenAssets(files apprelease.FilesInput, finalized bool) ([]string, error) {
	if finalized {
		return apprelease.PublishableReleaseFiles(files)
	}

	return apprelease.ListReleaseFiles(apprelease.ListReleaseFilesInput{FilesInput: files})
}

// releaseChecksumDigests maps each file the validated checksums name to its
// SHA-256.
func releaseChecksumDigests(files apprelease.FilesInput) (map[string]string, error) {
	body, err := apprelease.ReadValidatedReleaseChecksums(files, "")
	if err != nil {
		return nil, err
	}

	subjects, err := provenance.ParseChecksums(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	digests := make(map[string]string, len(subjects))
	for _, subject := range subjects {
		digests[subject.Name] = subject.SHA256
	}

	return digests, nil
}

// stageReleaseMavenAssets copies each checksum-bound publication file below
// directory into the staging root.
func stageReleaseMavenAssets(ctx context.Context, root, staging *os.Root, assets []string, digests map[string]string, directory, relative string) error {
	var total int64

	count := 0

	for _, asset := range assets {
		name, relErr := filepath.Rel(directory, asset)
		if relErr != nil || name == "." || !filepath.IsLocal(name) || strings.HasSuffix(name, ".bundle") {
			continue
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		expected := digests[filepath.Base(asset)]
		// GPG release sidecars, like Cosign bundles, are evidence rather than
		// publication payloads. Retain a producer's checksum-bound .asc file.
		if expected == "" && strings.HasSuffix(name, ".asc") {
			continue
		}

		if expected == "" {
			return fmt.Errorf("publication file %s is not bound by the release checksums: %w", asset, errs.ErrValidation)
		}

		info, err := root.Lstat(filepath.Join(relative, name))
		if err != nil {
			return err
		}

		if err := validatePublicationFile(info); err != nil {
			return err
		}

		total += info.Size()

		count++
		if total > 2<<30 || count > 10000 {
			return fmt.Errorf("publication exceeds 2 GiB or 10000 files: %w", errs.ErrValidation)
		}

		if err := stageReleaseMavenFile(root, staging, filepath.Join(relative, name), name, expected); err != nil {
			return err
		}
	}

	return nil
}

func stageReleaseMavenFile(source, destination *os.Root, sourceName, name, expected string) error {
	file, err := source.Open(sourceName)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	if err := validatePublicationFile(info); err != nil {
		return err
	}

	if err := destination.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}

	out, err := destination.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	hash := sha256.New()

	_, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(file, publicationFileLimit(name)+1))
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return err
	}

	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("publication file %s changed while staging: %w", sourceName, errs.ErrValidation)
	}

	return nil
}
