// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// ValidateGPGKeysFormat reads an armored GPG public-key bundle
// (.forge-ci/allowed_gpg_keys.asc) and returns one human-readable
// error per structural problem. An empty slice means the file
// contains at least one well-formed PGP PUBLIC KEY BLOCK.
//
// Two layers of validation:
//  1. Marker balance — every BEGIN must have a matching END.
//     armor.Decode is lenient about missing END markers (it
//     returns a partial block at EOF), so we check explicitly.
//  2. armor.Decode — validates the base64 body and CRC24
//     checksum using ProtonMail/go-crypto's RFC 4880 parser.
func ValidateGPGKeysFormat(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("cannot read %s: %v", path, err)}
	}

	content := string(data)

	var errs []string

	// Layer 1: marker balance.
	const (
		beginMarker = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
		endMarker   = "-----END PGP PUBLIC KEY BLOCK-----"
	)

	beginCount := strings.Count(content, beginMarker)
	endCount := strings.Count(content, endMarker)

	if beginCount == 0 {
		return []string{fmt.Sprintf(
			"no %q marker found — the file must contain at least one "+
				"armored PGP public key block (RFC 4880 section 6)", beginMarker)}
	}

	if beginCount != endCount {
		errs = append(errs, fmt.Sprintf(
			"BEGIN/END marker mismatch: %d BEGIN, %d END — a block is truncated",
			beginCount, endCount))
	}

	// Layer 2: armor.Decode for body validation (base64, CRC24).
	reader := bufio.NewReader(strings.NewReader(content))
	blockCount := 0

	for {
		block, err := armor.Decode(reader)
		if err == io.EOF {
			break
		}

		if err != nil {
			errs = append(errs, fmt.Sprintf("armor block %d: %v", blockCount+1, err))

			break
		}

		if block.Type != "PGP PUBLIC KEY BLOCK" {
			errs = append(errs, fmt.Sprintf(
				"block %d: unexpected armor type %q, expected \"PGP PUBLIC KEY BLOCK\"",
				blockCount+1, block.Type))
		}

		// Drain the body — base64 decode errors surface here, not
		// in Decode itself.
		if _, err := io.Copy(io.Discard, block.Body); err != nil {
			errs = append(errs, fmt.Sprintf(
				"block %d: body is not valid base64: %v", blockCount+1, err))
		}

		blockCount++
	}

	if blockCount == 0 && len(errs) == 0 {
		errs = append(errs, "no PGP PUBLIC KEY BLOCK found — the file must contain at least one armored public key (RFC 4880 section 6)")
	}

	return errs
}
