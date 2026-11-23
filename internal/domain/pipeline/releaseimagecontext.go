// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/artifact"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// ValidateReleaseImageContext admits checkout-only builds or a complete
// producer artifact/digest pair before any build or registry-fetch scheduling.
// Consumers still verify the actual staged bytes before installing them.
func ValidateReleaseImageContext(name, digest string) error {
	if name == "" && digest == "" {
		return nil
	}

	if name == "" || digest == "" {
		return fmt.Errorf("context-artifact and context-digest must be supplied together: %w", errs.ErrValidation)
	}

	if err := artifact.ValidateName(name); err != nil {
		return err
	}

	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size || digest != strings.ToLower(digest) {
		return fmt.Errorf("context-digest must be a lowercase SHA-256: %w", errs.ErrValidation)
	}

	return nil
}
