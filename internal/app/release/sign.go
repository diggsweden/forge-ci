// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	domainrelease "github.com/diggsweden/forge-ci/internal/domain/release"
)

// Signer is the slice of the signing adapter the sign flow needs.
// internal/adapters/openpgp.Signer and the cosign-backed signer in
// signer_cosign.go both satisfy it in production; tests pass in-memory
// fakes.
//
// SignFile produces one sidecar file next to <file>; Extensions()
// lists the file extension(s) the backend writes (GPG: `.asc`;
// cosign: `.bundle` — a single self-contained v3 Sigstore bundle).
// The caller uses Extensions() to drive the rename logic when the
// input path is outside cwd, so per-method sidecar layouts compose
// with the same asset-walking loop.
//
// The interface intentionally hides the key material — backends are
// constructed once at process start, not per-file. Compare with the
// previous adapter that exposed (keyID, file) and required gpg-agent
// to hold the passphrase across multiple invocations.
type Signer interface {
	SignFile(ctx context.Context, file string) error
	Extensions() []string
}

// SignInput drives `forge-ci release sign`.
type SignInput struct {
	ChecksumsFile         string // caller-named file; empty selects domainrelease.ChecksumsFile
	SkipChecksumsFile     bool
	Files                 []string
	AssemblyFile          string     // when set, sign exactly the staged release assembly
	ReleaseFiles          FilesInput // release-file manifest and dist directory used by ManifestSections / ChecksumsFromManifest / FilesFromManifest
	ManifestSections      []string
	ChecksumsFromManifest bool
	FilesFromManifest     bool // sign every file the manifest's checksums file lists, next to the file
}

// SignArtifacts signs the checksums file in place (if present) and each named
// or manifest-listed file, every signature beside its file. It signs exactly
// the files it is given: nothing is found by globbing a directory, so two
// files of the same name in different directories are two files, each with
// its own signature. The CLI wires openpgp.NewSignerFromArmor (env-sourced
// key); tests pass an in-memory fake.
func SignArtifacts(ctx context.Context, signer Signer, out io.Writer, in SignInput) error {
	if signer == nil {
		return fmt.Errorf("sign: signer is required: %w", errs.ErrUsage)
	}

	if _, err := signerExtensions(signer); err != nil {
		return err
	}

	if in.AssemblyFile != "" {
		return signAssemblyArtifacts(ctx, signer, out, in.AssemblyFile)
	}

	resolved, err := resolveSignInput(in)
	if err != nil {
		return err
	}

	in = resolved

	sectionFiles, err := releaseFilesManifestSectionFiles(in)
	if err != nil {
		return err
	}

	in.Files = append(in.Files, sectionFiles...)

	selected, err := selectExactSignFiles(in.Files)
	if err != nil {
		return err
	}

	signsChecksums, err := preflightSigningChecksums(in)
	if err != nil {
		return err
	}

	if err := requireSigningSelection(in, selected, signsChecksums); err != nil {
		return err
	}

	if signsChecksums {
		_, _ = fmt.Fprintf(out, "Signing %s\n", in.ChecksumsFile)

		if err := signFileWithSidecars(ctx, signer, in.ChecksumsFile); err != nil {
			return fmt.Errorf("sign checksums: %w", err)
		}
	}

	for _, file := range selected {
		_, _ = fmt.Fprintf(out, "Signing %s\n", file)
		if err := signFileWithSidecars(ctx, signer, file); err != nil {
			return fmt.Errorf("sign %q: %w", file, err)
		}
	}

	return nil
}

// A call that names nothing to sign and finds no checksums file would pass
// a release on unsigned. An explicitly selected manifest section may be empty.
func requireSigningSelection(in SignInput, selected []string, signsChecksums bool) error {
	if !signsChecksums && len(selected) == 0 && len(in.ManifestSections) == 0 && !in.FilesFromManifest {
		return fmt.Errorf("sign: nothing to sign: name the files with --file, --manifest-section, --files-from-manifest or --assembly, or write the checksums file first: %w", errs.ErrUsage)
	}

	return nil
}

func preflightSigningChecksums(in SignInput) (bool, error) {
	if in.SkipChecksumsFile {
		return false, nil
	}

	if err := validateReleasePathComponents(in.ChecksumsFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	return checksumsFilePresent(in.ChecksumsFile)
}

// resolveSignInput applies the checksums-file default and resolves --checksums-from-manifest before any signing starts.
func resolveSignInput(in SignInput) (SignInput, error) {
	if in.ChecksumsFromManifest {
		checksumFile, err := manifestChecksumsFile(in)
		if err != nil {
			return in, err
		}

		in.ChecksumsFile = checksumFile
	}

	if in.ChecksumsFile == "" && !in.SkipChecksumsFile {
		in.ChecksumsFile = domainrelease.ChecksumsFile
	}

	return in, nil
}

// manifestChecksumsFile returns the manifest's single validated checksums
// file for --checksums-from-manifest. ChecksumsFile is empty unless the caller
// named one, so any value conflicts.
func manifestChecksumsFile(in SignInput) (string, error) {
	if in.SkipChecksumsFile {
		return "", fmt.Errorf("sign: --checksums-from-manifest conflicts with skipping checksums: %w", errs.ErrInvalidConfig)
	}

	checksumFile, _, err := manifestReleaseChecksums(in.ReleaseFiles, in.ChecksumsFile)

	return checksumFile, err
}

func releaseFilesManifestSectionFiles(in SignInput) ([]string, error) {
	var files []string

	for _, section := range in.ManifestSections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}

		paths, err := ListReleaseFiles(ListReleaseFilesInput{FilesInput: in.ReleaseFiles, Section: section})
		if err != nil {
			return nil, err
		}

		files = append(files, paths...)
	}

	if in.FilesFromManifest {
		subjects, err := checksumSubjectsToSign(in.ReleaseFiles)
		if err != nil {
			return nil, err
		}

		files = append(files, subjects...)
	}

	return files, nil
}

func signAssemblyArtifacts(ctx context.Context, signer Signer, out io.Writer, assemblyFile string) error {
	extensions, err := signerExtensions(signer)
	if err != nil {
		return err
	}

	selected, err := validatedSigningAssembly(assemblyFile, extensions)
	if err != nil {
		return err
	}

	for _, path := range selected {
		_, _ = fmt.Fprintf(out, "Signing %s\n", path)
		if err := signFileWithSidecars(ctx, signer, path); err != nil {
			return fmt.Errorf("sign %q: %w", path, err)
		}
	}

	return nil
}

// selectExactSignFiles preflights operator-supplied files for in-place
// signing. It does not glob, and each sidecar stays beside its file.
func selectExactSignFiles(files []string) ([]string, error) {
	selected := make([]string, 0, len(files))
	seen := map[string]struct{}{}

	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}

		if _, ok := seen[file]; ok {
			continue
		}

		if err := validateReleasePathComponents(file); err != nil {
			return nil, err
		}

		selected = append(selected, file)
		seen[file] = struct{}{}
	}

	return selected, nil
}

// checksumsFilePresent reports whether the checksums file exists and is not
// empty, the condition under which it is signed.
func checksumsFilePresent(checksumsFile string) (bool, error) {
	info, err := os.Stat(checksumsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("checksums file %s: %w", checksumsFile, err)
	}

	return info.Size() != 0, nil
}

func signerExtensions(signer Signer) ([]string, error) {
	extensions := signer.Extensions()
	if len(extensions) == 0 {
		return nil, fmt.Errorf("signer advertises no signature sidecar extension: %w", errs.ErrInvalidConfig)
	}

	seen := make(map[string]struct{}, len(extensions))
	for _, ext := range extensions {
		if ext == "." || !strings.HasPrefix(ext, ".") || strings.ContainsAny(ext, "/\\\r\n") {
			return nil, fmt.Errorf("signer advertises unsafe signature sidecar extension %q: %w", ext, errs.ErrInvalidConfig)
		}

		if _, ok := seen[ext]; ok {
			return nil, fmt.Errorf("signer advertises duplicate signature sidecar extension %q: %w", ext, errs.ErrInvalidConfig)
		}

		seen[ext] = struct{}{}
	}

	return extensions, nil
}

// signFileWithSidecars removes stale outputs, invokes the backend, and proves
// that every advertised sidecar was freshly produced as a non-empty regular
// file before the release flow may continue.
func signFileWithSidecars(ctx context.Context, signer Signer, file string) error {
	if err := validateReleasePathComponents(file); err != nil {
		return err
	}

	extensions, err := signerExtensions(signer)
	if err != nil {
		return err
	}

	for _, ext := range extensions {
		if err := os.Remove(file + ext); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stale sidecar %q: %w", file+ext, err)
		}
	}

	if err := signer.SignFile(ctx, file); err != nil {
		return err
	}

	for _, ext := range extensions {
		info, err := os.Lstat(file + ext)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("signer did not produce non-empty regular sidecar %q: %w", file+ext, errs.ErrValidation)
		}
	}

	return nil
}
