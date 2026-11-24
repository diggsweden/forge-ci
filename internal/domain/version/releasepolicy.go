// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package version

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

const (
	// ReleasePolicyStable permits stable versions only and is the default.
	ReleasePolicyStable = "stable"
	// ReleasePolicyStablePrerelease also permits immutable prerelease versions.
	ReleasePolicyStablePrerelease = "stable-prerelease"
	// releasePolicyForkPrefix starts fork:WORD, the policy of a fork that
	// releases its own builds of upstream versions as vX.Y.Z-WORD.N.
	releasePolicyForkPrefix = "fork:"
)

// CanonicalPrereleaseIdentifiers are the prerelease words every tool reads
// as "not ready": the one list of them. release.IsCanonicalPrerelease
// classifies suffixes by it, and no fork may take one as its WORD.
var CanonicalPrereleaseIdentifiers = []string{"alpha", "beta", "rc", "dev", "snapshot", "SNAPSHOT"}

var (
	forkWordPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	forkNumberPattern = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
)

// forkWord returns the WORD of a fork:WORD policy.
func forkWord(policy string) (string, bool) {
	word, ok := strings.CutPrefix(policy, releasePolicyForkPrefix)
	if !ok || !forkWordPattern.MatchString(word) || slices.Contains(CanonicalPrereleaseIdentifiers, word) {
		return "", false
	}

	return word, true
}

// isForkBuild reports whether a prerelease part is WORD.N for the policy's
// WORD: a fork's N-th build of the version, which that fork releases as stable.
func isForkBuild(prerelease, policy string) bool {
	word, ok := forkWord(policy)
	if !ok {
		return false
	}

	n, ok := strings.CutPrefix(prerelease, word+".")

	return ok && forkNumberPattern.MatchString(n)
}

// ForkPolicies returns, sorted, the fork:WORD policy of every fork build
// among tags: a vX.Y.Z-WORD.N tag whose WORD a fork policy admits, which no
// prerelease word such as rc is.
func ForkPolicies(tags []string) []string {
	var policies []string

	for _, tag := range tags {
		parsed, ok := ParseSemverTag(tag)
		if !ok || parsed.Build != "" {
			continue
		}

		word, _, _ := strings.Cut(parsed.Prerelease, ".")
		if policy := releasePolicyForkPrefix + word; isForkBuild(parsed.Prerelease, policy) {
			policies = append(policies, policy)
		}
	}

	slices.Sort(policies)

	return slices.Compact(policies)
}

// IsPrerelease reports whether an admitted release version is a prerelease
// under policy: the one answer every release step asks. A fork's -WORD.N
// build is stable under its fork:WORD policy; without a policy ("") any
// prerelease part counts, as SemVer reads it.
func IsPrerelease(parsed Semver, policy string) bool {
	return parsed.Prerelease != "" && !isForkBuild(parsed.Prerelease, policy)
}

// ReleaseHistoryPattern is the regex of the tags that bound changelog history
// under policy: stable vX.Y.Z, and under fork:WORD also the fork's
// vX.Y.Z-WORD.N. Upstream's tags stay boundaries in a fork: its main is
// rebased onto each upstream release, so its own older tags leave the history.
func ReleaseHistoryPattern(policy string) string {
	if word, ok := forkWord(policy); ok {
		return `^v[0-9]+\.[0-9]+\.[0-9]+(-` + word + `\.[1-9][0-9]{0,8})?$`
	}

	return `^v[0-9]+\.[0-9]+\.[0-9]+$`
}

// IsReleaseHistoryTag reports whether tag matches ReleaseHistoryPattern.
func IsReleaseHistoryTag(tag, policy string) bool {
	parsed, ok := ParseSemverTag(tag)

	return ok && parsed.Build == "" && (parsed.Prerelease == "" || isForkBuild(parsed.Prerelease, policy))
}

// ValidateReleaseTag admits a version under trusted caller policy. Empty policy
// is stable-only; build metadata is not a distinct publishable release identity.
// Under fork:WORD only vX.Y.Z-WORD.N is admitted: upstream's plain vX.Y.Z
// names upstream's commit, never the fork's.
func ValidateReleaseTag(tag, policy string) (Semver, error) {
	if err := ValidateReleasePolicy(policy); err != nil {
		return Semver{}, err
	}

	parsed, ok := ParseSemverTag(tag)
	if !ok || parsed.Build != "" {
		return Semver{}, fmt.Errorf("release tag must be vMAJOR.MINOR.PATCH with an optional prerelease and no build metadata (got %q): %w", tag, errs.ErrValidation)
	}

	if word, fork := forkWord(policy); fork {
		if !isForkBuild(parsed.Prerelease, policy) {
			return Semver{}, fmt.Errorf("release tag must be vMAJOR.MINOR.PATCH-%s.N, N from 1 without a leading zero, under release-policy %s (got %q): %w", word, policy, tag, errs.ErrValidation)
		}

		return parsed, nil
	}

	if parsed.Prerelease != "" && policy != ReleasePolicyStablePrerelease {
		return Semver{}, fmt.Errorf("release tag must be stable vMAJOR.MINOR.PATCH under release-policy stable (got %q): %w", tag, errs.ErrValidation)
	}

	return parsed, nil
}

// ValidateReleasePolicy rejects unknown modes, including legacy overrides.
func ValidateReleasePolicy(policy string) error {
	if _, fork := forkWord(policy); fork || policy == "" || policy == ReleasePolicyStable || policy == ReleasePolicyStablePrerelease {
		return nil
	}

	return fmt.Errorf("release-policy must be stable, stable-prerelease or fork:WORD, WORD lowercase and not a prerelease word such as rc (got %q): %w", policy, errs.ErrUsage)
}

// LatestReleaseTag compares admitted versions numerically, including beta.10.
func LatestReleaseTag(tags []string, policy string) string {
	var latest string

	for _, tag := range tags {
		if _, err := ValidateReleaseTag(tag, policy); err != nil {
			continue
		}

		if latest == "" || semver.Compare(tag, latest) > 0 {
			latest = tag
		}
	}

	return latest
}
