// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package genericcli_test

import (
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
)

func testBash(t *testing.T) string {
	t.Helper()

	return testenv.GNUExecutable(t, "bash")
}
