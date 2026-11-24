// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"encoding/json"
	"fmt"

	domaincontainer "github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// RawManifestRegistry fetches the manifest JSON served for an image reference.
type RawManifestRegistry interface {
	Manifest(ctx context.Context, ref string) ([]byte, error)
}

// PlatformRefInput drives PlatformRef.
type PlatformRefInput struct {
	Ref      string
	Platform string
}

// CanonicalRef canonicalizes a Docker image reference.
func CanonicalRef(ref string) (string, error) {
	return domaincontainer.CanonicalImageRef(ref)
}

// ImageNameForRef returns the repository/name for a Docker image reference.
func ImageNameForRef(ref string) (string, error) {
	return domaincontainer.ImageNameForRef(ref)
}

// PlatformRef resolves ref to the single-platform digest ref Buildah should use
// for platform. Non-index manifests return the canonical ref unchanged.
func PlatformRef(ctx context.Context, registry RawManifestRegistry, in PlatformRefInput) (string, error) {
	if registry == nil {
		return "", fmt.Errorf("registry is required: %w", errs.ErrUsage)
	}

	wanted, err := domaincontainer.ParsePlatform(in.Platform)
	if err != nil {
		return "", err
	}

	inspectRef, err := domaincontainer.CanonicalImageRef(in.Ref)
	if err != nil {
		return "", err
	}

	raw, err := registry.Manifest(ctx, inspectRef)
	if err != nil {
		return "", err
	}

	digest, isIndex, err := platformDigest(raw, wanted)
	if err != nil {
		return "", err
	}

	if !isIndex {
		return inspectRef, nil
	}

	if digest == "" {
		return "", fmt.Errorf("image %s lacks platform %s: %w", in.Ref, in.Platform, errs.ErrValidation)
	}

	imageName, err := domaincontainer.ImageNameForRef(inspectRef)
	if err != nil {
		return "", err
	}

	return imageName + "@" + digest, nil
}

func platformDigest(raw []byte, wanted domaincontainer.Platform) (string, bool, error) {
	var doc struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
				Variant      string `json:"variant"`
			} `json:"platform"`
		} `json:"manifests"`
	}

	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", false, fmt.Errorf("parse image manifest JSON: %w: %w", err, errs.ErrMalformedInput)
	}

	if doc.Manifests == nil {
		return "", false, nil
	}

	for _, manifest := range doc.Manifests {
		if manifest.Platform.OS != wanted.OS || manifest.Platform.Architecture != wanted.Arch {
			continue
		}

		if wanted.Variant != "" && manifest.Platform.Variant != wanted.Variant {
			continue
		}

		if !domaincontainer.ValidDigest(manifest.Digest) {
			return "", true, fmt.Errorf("matching platform manifest has an invalid digest: %w", errs.ErrMalformedInput)
		}

		return manifest.Digest, true, nil
	}

	return "", true, nil
}
