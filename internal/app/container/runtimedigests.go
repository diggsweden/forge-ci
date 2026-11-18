// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	appartifact "github.com/diggsweden/forge-ci/internal/app/artifact"
	domaincontainer "github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

// DownloadRuntimeDigests requires one exact artifact and digest marker for each
// selected native leg. It exposes a merge directory only after the set is complete.
func DownloadRuntimeDigests(ctx context.Context, downloader provider.RunArtifactDownloader, imageJSON, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var image domaincontainer.RuntimeImage
	if err := decodeRuntimeJSON([]byte(imageJSON), &image); err != nil {
		return err
	}
	if _, err := domaincontainer.PlanRuntimeImages([]domaincontainer.RuntimeImage{image}, image.Archs); err != nil {
		return err
	}
	if destination == "" {
		return fmt.Errorf("runtime digest destination is required: %w", errs.ErrUsage)
	}
	work, err := os.MkdirTemp(filepath.Dir(destination), ".runtime-digests-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	seen := map[string]bool{}
	for _, arch := range image.Archs {
		name := "digests-" + image.Target + "-" + arch
		dir := filepath.Join(work, name)
		if _, err := appartifact.Download(ctx, downloader, nil, nil, provider.RunArtifactDownload{Name: name, Dir: dir}); err != nil {
			return err
		}
		digests, err := digestFiles(dir)
		if err != nil {
			return err
		}
		if len(digests) != 1 {
			return fmt.Errorf("runtime artifact %s requires exactly one digest marker: %w", name, errs.ErrMissingInput)
		}
		if seen[digests[0]] {
			return fmt.Errorf("runtime architecture artifacts repeat a digest: %w", errs.ErrValidation)
		}
		seen[digests[0]] = true
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Refuse an existing destination rather than merge stale markers into this run.
	if err := os.Mkdir(destination, 0755); err != nil {
		return err
	}
	for digest := range seen {
		if _, err := WriteDigestMarker(WriteDigestMarkerInput{Digest: digest, DigestsDir: destination}); err != nil {
			return err
		}
	}
	return nil
}
