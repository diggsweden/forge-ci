// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package consumerinit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/consumerinit"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// A checkout's origin, in any form git keeps it, names the fork by its web
// address; anything that is not a forge's repository address is refused.
func TestParseForgeURL(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"https://forge.example/org/forge-ci",
		"https://forge.example/org/forge-ci.git",
		"https://forge.example/org/forge-ci/",
		"https://user@forge.example/org/forge-ci",
		"git@forge.example:org/forge-ci.git",
		"ssh://git@forge.example/org/forge-ci.git",
	} {
		got, err := consumerinit.ParseForgeURL(raw)
		if err != nil || got.Host != "forge.example" || got.Repository != "org/forge-ci" {
			t.Errorf("%s: %+v, %v", raw, got, err)
		}
	}

	if got, err := consumerinit.ParseForgeURL("https://forge.example:8443/org/forge-ci"); err != nil || got.Host != "forge.example:8443" {
		t.Errorf("a forge on a port keeps it: %+v, %v", got, err)
	}

	// GitLab groups nest.
	if got, err := consumerinit.ParseForgeURL("git@gitlab.example:team/ci/forge-ci.git"); err != nil || got.Repository != "team/ci/forge-ci" {
		t.Errorf("nested group: %+v, %v", got, err)
	}

	for _, raw := range []string{
		"origin", "/srv/git/forge-ci", "file:///srv/git/forge-ci", "http://forge.example/org/forge-ci",
		"ssh://git@forge.example:2222/org/forge-ci", "https://forge.example/forge-ci", "git@forge.example:forge-ci",
	} {
		if _, err := consumerinit.ParseForgeURL(raw); !errors.Is(err, errs.ErrUsage) {
			t.Errorf("%s: %v, want a usage refusal", raw, err)
		}
	}

	// An origin may carry a token; a refusal never repeats it.
	if _, err := consumerinit.ParseForgeURL("http://oauth2:s3cret-token@forge.example/org/forge-ci"); err == nil || strings.Contains(err.Error(), "s3cret-token") || strings.Contains(err.Error(), "oauth2") {
		t.Errorf("the refusal repeats the origin's credentials: %v", err)
	}
}
