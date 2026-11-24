// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package version_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	versioncmd "github.com/diggsweden/forge-ci/internal/cli/commands/version"
	domainversion "github.com/diggsweden/forge-ci/internal/domain/version"
)

// The workflow that runs git-cliff itself takes its history pattern from here.
func TestHistoryPatternCmd(t *testing.T) {
	for policy, want := range map[string]string{
		"stable":    domainversion.ReleaseHistoryPattern("stable"),
		"fork:acme": domainversion.ReleaseHistoryPattern("fork:acme"),
	} {
		var out bytes.Buffer

		cmd := versioncmd.New()
		cmd.Writer = &out

		if err := cmd.Run(context.Background(), []string{"version", "history-pattern", "--release-policy", policy}); err != nil {
			t.Fatalf("%s: %v", policy, err)
		}

		if got := strings.TrimSpace(out.String()); got != want {
			t.Errorf("%s: printed %q, want %q", policy, got, want)
		}
	}

	if err := versioncmd.New().Run(context.Background(), []string{"version", "history-pattern", "--release-policy", "fork:rc"}); err == nil {
		t.Error("fork:rc was accepted")
	}
}
