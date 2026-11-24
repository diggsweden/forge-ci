// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package version_test

import (
	"regexp"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/version"
)

func TestForkPolicyNamesALowercaseWord(t *testing.T) {
	t.Parallel()

	for policy, want := range map[string]bool{
		"fork:itiquette": true,
		"fork:my-org2":   true,
		"fork:":          false,
		"fork:Itiquette": false,
		"fork:rc":        false, // every tool reads rc as not ready
		"fork:beta":      false,
		"fork:a.b":       false,
		"fork:2org":      false,
		"fork-itiquette": false,
	} {
		if err := version.ValidateReleasePolicy(policy); (err == nil) != want {
			t.Errorf("ValidateReleasePolicy(%q) error = %v, want valid %t", policy, err, want)
		}
	}
}

func TestForkPolicyAdmitsOnlyItsBuilds(t *testing.T) {
	t.Parallel()

	for tag, want := range map[string]bool{
		"v0.94.4-itiquette.1":     true,
		"v0.94.4-itiquette.10":    true,
		"v0.94.4":                 false, // upstream's tag names upstream's commit
		"v0.94.4-itiquette.0":     false,
		"v0.94.4-itiquette.01":    false,
		"v0.94.4-itiquette.1.2":   false,
		"v0.94.4-itiquette":       false,
		"v0.94.4-other.1":         false,
		"v0.94.4-rc.1":            false,
		"v0.94.4-itiquette.1+b.1": false,
	} {
		if _, err := version.ValidateReleaseTag(tag, "fork:itiquette"); (err == nil) != want {
			t.Errorf("ValidateReleaseTag(%q, fork:itiquette) error = %v, want admitted %t", tag, err, want)
		}
	}

	if _, err := version.ValidateReleaseTag("v0.94.4-itiquette.1", version.ReleasePolicyStable); err == nil {
		t.Error("a fork build was admitted under release-policy stable")
	}
}

// The fork's build is stable under its own policy only; plain SemVer orders
// its builds, .2 before .10, with no rule of Forge CI's own.
func TestForkBuildIsStableAndOrderedUnderItsPolicy(t *testing.T) {
	t.Parallel()

	parsed, ok := version.ParseSemverTag("v0.94.4-itiquette.1")
	if !ok {
		t.Fatal("parse")
	}

	if version.IsPrerelease(parsed, "fork:itiquette") {
		t.Error("a fork build is a prerelease under its fork policy")
	}

	for _, policy := range []string{"", version.ReleasePolicyStablePrerelease, "fork:other"} {
		if !version.IsPrerelease(parsed, policy) {
			t.Errorf("v0.94.4-itiquette.1 is stable under %q", policy)
		}
	}

	tags := []string{"v0.94.5", "v0.94.4-itiquette.2", "v0.94.4-itiquette.10", "v0.94.3-itiquette.7", "v0.94.4-rc.1"}
	if got := version.LatestReleaseTag(tags, "fork:itiquette"); got != "v0.94.4-itiquette.10" {
		t.Errorf("LatestReleaseTag = %q, want v0.94.4-itiquette.10", got)
	}
}

// The changelog's history pattern and the same-commit check agree: under
// fork:WORD upstream's tags and the fork's builds bound history, nothing else.
func TestReleaseHistoryPatternMatchesIsReleaseHistoryTag(t *testing.T) {
	t.Parallel()

	for _, policy := range []string{"", "stable", "stable-prerelease", "fork:itiquette"} {
		pattern := regexp.MustCompile(version.ReleaseHistoryPattern(policy))

		for _, tag := range []string{"v0.94.4", "v0.94.4-itiquette.1", "v0.94.4-itiquette.10", "v0.94.4-itiquette.01", "v0.94.4-rc.1", "v0.94.4-other.1", "v0.94.4+b", "release-request/v0.94.4"} {
			if got, want := pattern.MatchString(tag), version.IsReleaseHistoryTag(tag, policy); got != want {
				t.Errorf("policy %q tag %q: pattern %t, IsReleaseHistoryTag %t", policy, tag, got, want)
			}
		}
	}

	if !version.IsReleaseHistoryTag("v0.94.4", "fork:itiquette") || !version.IsReleaseHistoryTag("v0.94.4-itiquette.2", "fork:itiquette") {
		t.Error("a fork's history is bounded by upstream's tags and its own builds")
	}

	if version.IsReleaseHistoryTag("v0.94.4-itiquette.2", "stable-prerelease") {
		t.Error("only fork:WORD makes a fork build a history boundary")
	}
}
