// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
	"os"
	"strings"
)

// PGP armor markers per RFC 4880 section 6 (ASCII Armor).
const (
	pgpPublicKeyBegin = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	pgpPublicKeyEnd   = "-----END PGP PUBLIC KEY BLOCK-----"
)

// ValidateGPGKeysFormat reads an armored GPG public-key bundle
// (.forge-ci/allowed_gpg_keys.asc) and returns one human-readable
// error per structural problem. An empty slice means the file has
// at least one well-formed PGP PUBLIC KEY BLOCK.
//
// The check is deliberately structural — it verifies that BEGIN/END
// markers are present and correctly ordered, and that there is
// content between them. It does NOT parse the OpenPGP packet
// structure or verify the CRC24 checksum; that is the job of
// `git verify-tag` at release time.
func ValidateGPGKeysFormat(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("cannot read %s: %v", path, err)}
	}

	content := string(data)

	var errs []string

	beginIdx := strings.Index(content, pgpPublicKeyBegin)
	if beginIdx == -1 {
		return []string{fmt.Sprintf(
			"no %q marker found — the file must contain at least one "+
				"armored PGP public key block (RFC 4880 section 6)", pgpPublicKeyBegin)}
	}

	// Find the END marker after the first BEGIN.
	rest := content[beginIdx+len(pgpPublicKeyBegin):]
	endIdx := strings.Index(rest, pgpPublicKeyEnd)
	if endIdx == -1 {
		errs = append(errs, fmt.Sprintf(
			"missing %q marker — the armor block is truncated", pgpPublicKeyEnd))

		return errs
	}

	// Content between BEGIN and END must be non-empty (base64 body
	// plus optional Comment: headers and a =checksum line).
	body := strings.TrimSpace(rest[:endIdx])
	if body == "" {
		errs = append(errs, "armor block body is empty — expected base64-encoded key data")
	}

	// Check for additional BEGIN markers after the first END (multiple
	// concatenated blocks are valid; each must have its own END).
	afterFirst := rest[endIdx+len(pgpPublicKeyEnd):]

	for {
		nextBegin := strings.Index(afterFirst, pgpPublicKeyBegin)
		if nextBegin == -1 {
			break
		}

		afterBegin := afterFirst[nextBegin+len(pgpPublicKeyBegin):]
		nextEnd := strings.Index(afterBegin, pgpPublicKeyEnd)
		if nextEnd == -1 {
			errs = append(errs, fmt.Sprintf(
				"additional %q block is missing its %q marker — truncated",
				pgpPublicKeyBegin, pgpPublicKeyEnd))

			break
		}

		body := strings.TrimSpace(afterBegin[:nextEnd])
		if body == "" {
			errs = append(errs, "armor block body is empty — expected base64-encoded key data")
		}

		afterFirst = afterBegin[nextEnd+len(pgpPublicKeyEnd):]
	}

	return errs
}
