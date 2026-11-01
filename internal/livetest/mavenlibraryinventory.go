// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"errors"
	"fmt"
)

var (
	errUnexpectedMavenAsset = errors.New("unexpected Maven library release asset")
	errDuplicateMavenAsset  = errors.New("duplicate Maven library release asset")
	errEmptyMavenAsset      = errors.New("maven library release asset")
	errMissingMavenAsset    = errors.New("missing Maven library release asset")
)

// MavenLibraryReleaseInventory checks the native Maven library fixture's exact
// signed-file inventory before indexing assets by name. It performs no I/O.
func MavenLibraryReleaseInventory(assets []ReleaseAsset, artifact, version string) (map[string]string, error) {
	stem := artifact + "-" + version
	// The fixture declares four payloads and artifact.cdx.json. See the native
	// hello-maven release recipe and mavenlibraryfixture.go. release files declare
	// defaults to checksums.txt (app/release/releasefilesdeclare.go); the native
	// sign-and-publish-release.yml signs that checksum and the SBOM, then writes
	// and signs slsa-provenance.intoto.json. Finalization publishes those files,
	// not release-files.json or release-notes.md (releasefiles.go).
	required := []string{
		stem + ".pom", stem + ".jar", stem + "-sources.jar", stem + "-javadoc.jar",
		artifact + ".cdx.json", artifact + ".cdx.json.bundle",
		"checksums.txt", "checksums.txt.bundle",
		"slsa-provenance.intoto.json", "slsa-provenance.intoto.json.bundle",
	}

	allowed := make(map[string]bool, len(required))
	for _, name := range required {
		allowed[name] = true
	}

	indexed := make(map[string]string, len(required))

	for _, asset := range assets {
		if !allowed[asset.Name] {
			return nil, fmt.Errorf("%w %q", errUnexpectedMavenAsset, asset.Name)
		}

		if _, exists := indexed[asset.Name]; exists {
			return nil, fmt.Errorf("%w %q", errDuplicateMavenAsset, asset.Name)
		}

		if asset.Size <= 0 || asset.URL == "" {
			return nil, fmt.Errorf("%w %q has no content or download URL", errEmptyMavenAsset, asset.Name)
		}

		indexed[asset.Name] = asset.URL
	}

	for _, name := range required {
		if _, exists := indexed[name]; !exists {
			return nil, fmt.Errorf("%w %q", errMissingMavenAsset, name)
		}
	}

	return indexed, nil
}
