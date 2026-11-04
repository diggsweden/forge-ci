// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/diggsweden/forge-ci/internal/cliio"
	"github.com/diggsweden/forge-ci/internal/domain/ci"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/imageledger"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/pathsafe"
)

// ReleaseImagesLedgerName is the ledger PublishReleaseImages writes in its
// output directory.
const ReleaseImagesLedgerName = "release-images.json"

// PublishReleaseImagesInput is one release-images publish job: the plan, the
// layouts the build jobs staged, and where the ledger and evidence go.
type PublishReleaseImagesInput struct {
	Plan pipeline.ReleaseImagesPlan
	// LayoutsDir holds the build jobs' <name>-arch-<arch> layouts. They ran
	// consumer code, so the directory is read only after it is admitted, and
	// never written.
	LayoutsDir string
	// OutputDir receives the ledger, the scan results and the index
	// manifests. It must not exist yet.
	OutputDir     string
	AuthFile      string
	ReleaseTag    string
	ReleaseSHA    string
	ServerURL     string
	Repository    string
	RetryAttempts int
	Trivy         ImageEvidenceTrivy
	// Gate judges each scanned layout before any upload.
	Gate func(ctx context.Context, layout, platform string) error
}

// PublishReleaseImages scans and pushes every planned image from its staged
// layouts, as the Forgejo publish job does for one, and writes their ledger
// to OutputDir. The ledger is built in memory: nothing under LayoutsDir is
// appended to or trusted beyond the planned layouts.
func PublishReleaseImages(ctx context.Context, registry StagedSignerRegistry, summary ci.SummarySink, out io.Writer, in PublishReleaseImagesInput) error {
	for _, image := range in.Plan.Images {
		// Base images have their own import and signing path.
		if image.ImageKind == string(imageledger.ImageKindBase) {
			return fmt.Errorf("release image %s: a base image is not published by release-images publish: %w", image.Name, errs.ErrInvalidConfig)
		}
	}

	if _, err := os.Lstat(in.OutputDir); err == nil {
		return fmt.Errorf("release-images publish: %s already exists: %w", in.OutputDir, errs.ErrUsage)
	}

	if err := AdmitReleaseImageLayouts(in.LayoutsDir, in.Plan.Images); err != nil {
		return err
	}

	if err := os.MkdirAll(in.OutputDir, 0o750); err != nil {
		return fmt.Errorf("release-images publish: create %s: %w", in.OutputDir, err)
	}

	// The index metadata assembly writes is workspace-relative and of no use
	// after this job; it stays in a private directory of the job's own.
	work, err := os.MkdirTemp(".", ".forge-ci-release-images-")
	if err != nil {
		return fmt.Errorf("release-images publish: create work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	ledger, err := imageledger.Marshal(nil)
	if err != nil {
		return err
	}

	for _, image := range in.Plan.Images {
		ledger, err = publishReleaseImage(ctx, registry, summary, out, in, image, work, ledger)
		if err != nil {
			return fmt.Errorf("release image %s: %w", image.Name, err)
		}
	}

	path := filepath.Join(in.OutputDir, ReleaseImagesLedgerName)
	if err := cliio.WriteFile(path, ledger, 0o644); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "ledger: %d release image(s) recorded → %s\n", len(in.Plan.Images), path)

	return nil
}

// publishReleaseImage scans and assembles one image, then returns ledger
// with its entry appended.
func publishReleaseImage(ctx context.Context, registry StagedSignerRegistry, summary ci.SummarySink, out io.Writer, in PublishReleaseImagesInput, image pipeline.ReleaseImage, work string, ledger []byte) ([]byte, error) {
	revision := cmp.Or(image.Revision, in.ReleaseSHA)

	meta, err := PublishAndAssembleSignerImage(ctx, registry, nil, summary, out, SignerImageAssembleInput{
		LayoutsDir:      in.LayoutsDir,
		AuthFile:        in.AuthFile,
		SourceSHA:       revision,
		ServerURL:       in.ServerURL,
		Repository:      in.Repository,
		ImageRepository: image.ImageRepository,
		TagName:         image.CandidateTag,
		RequiredLabels:  append([]string{"org.opencontainers.image.revision=" + revision}, nonEmptyLines(image.RequiredLabels)...),
		CacheTagPrefix:  image.CacheTagPrefix,
		ManifestOutput:  filepath.Join(in.OutputDir, "index-"+image.Name+".json"),
		Name:            image.Name,
		Archs:           nonEmptyLines(image.Archs),
		MetadataDir:     filepath.Join(work, image.Name+"-index"),
		RetryAttempts:   in.RetryAttempts,
		Evidence: &StagedLayoutEvidence{
			Trivy:               in.Trivy,
			Stderr:              out,
			TrivyOutputTemplate: filepath.Join(in.OutputDir, "trivy-results-"+image.Name+"-{arch}.json"),
			Gate:                in.Gate,
		},
	})
	if err != nil {
		return nil, err
	}

	return AppendLedgerEntry(ledger, AddLedgerEntryInput{
		ReleaseTag:         in.ReleaseTag,
		Role:               cmp.Or(image.Role, image.Name),
		ImageKind:          cmp.Or(image.ImageKind, string(imageledger.ImageKindRelease)),
		ImageName:          image.ImageRepository,
		Digest:             meta.Digest,
		ProvenanceJSON:     image.ProvenanceJSON,
		DefaultSBOM:        true,
		FinalTagName:       image.FinalTagName,
		MovingTagName:      image.MovingTagName,
		Flavor:             image.Flavor,
		DeriveCandidateTag: true,
		BaseRef:            image.BaseRef,
		BaseInputID:        image.BaseInputID,
	})
}

// AdmitReleaseImageLayouts admits the directory the build jobs' layouts were
// gathered into. Those jobs ran consumer Containerfiles, so it must hold
// exactly the planned <name>-arch-<arch> directories, each with its metadata
// file and its layout directory, and nothing else: an unplanned entry, a
// missing one or a link is refused before anything is read or uploaded.
func AdmitReleaseImageLayouts(dir string, images []pipeline.ReleaseImage) error {
	root, err := pathsafe.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("release image layouts %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()

	// Each planned layout directory, and the entries it holds by type.
	want := map[string]map[string]fs.FileMode{}

	for _, image := range images {
		for _, arch := range nonEmptyLines(image.Archs) {
			want[image.Name+"-arch-"+arch] = map[string]fs.FileMode{
				image.Name + "-" + arch + ".json": 0,
				"layout":                          fs.ModeDir,
			}
		}
	}

	dirs := map[string]fs.FileMode{}
	for name := range want {
		dirs[name] = fs.ModeDir
	}

	if err := admitEntries(root.FS(), ".", dirs); err != nil {
		return fmt.Errorf("release image layouts %s: %w", dir, err)
	}

	for name, entries := range want {
		if err := admitEntries(root.FS(), name, entries); err != nil {
			return fmt.Errorf("release image layouts %s: %w", dir, err)
		}
	}

	return nil
}

// admitEntries requires dir to hold exactly the entries of want, each of the
// type it gives, and nothing else. A link is never of a wanted type.
func admitEntries(fsys fs.FS, dir string, want map[string]fs.FileMode) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := path.Join(dir, entry.Name())

		mode, ok := want[entry.Name()]
		if !ok {
			return fmt.Errorf("%s is not a planned layout entry: %w", name, errs.ErrValidation)
		}

		if entry.Type() != mode {
			return fmt.Errorf("%s is a link or of another type than its layout entry: %w", name, errs.ErrValidation)
		}
	}

	if len(entries) != len(want) {
		return fmt.Errorf("%s lacks a planned layout entry: %w", dir, errs.ErrValidation)
	}

	return nil
}
