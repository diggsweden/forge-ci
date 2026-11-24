// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package snapshotimage

import (
	"github.com/diggsweden/forge-ci/internal/domain/snapshotimage"
)

// WriteDependencyInventory writes the frozen dependency inventory a trusted
// producer hands to builds, for fixtures that start after that handoff.
func WriteDependencyInventory(plan snapshotimage.Plan, destination string) error {
	manifest, err := dependencyInventory(plan)
	if err != nil {
		return err
	}

	return WriteJSON(destination, manifest)
}
