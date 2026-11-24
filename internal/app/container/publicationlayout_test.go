// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	"errors"
	"os"
	"testing"
)

func assertPublicationLayoutRemoved(t *testing.T, directory string) {
	t.Helper()

	if directory == "" {
		t.Fatal("no publication layout was created")
	}

	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary layout retained: %v", err)
	}
}
