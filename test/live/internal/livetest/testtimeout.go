// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"fmt"
	"strings"
	"time"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// LiveTestTimeout sizes the process watchdog, not any workflow's deadline.
// A two-release demo can wait for both releases and its final PR; the process
// must outlive all three ordinary workflow waits and allow cleanup to finish.
// An override may extend, but never shorten or disable, the bounded budget.
func LiveTestTimeout(getenv func(string) string) (string, error) {
	minimum := 3 * time.Hour
	if getenv("FORGE_CI_LIVE_CONSUMER_EVENT") == TrialRelease {
		// Only size the watchdog here. Scenario admission validates the
		// provider-specific settings; GitLab and Forgejo share next-version.
		for word := range strings.SplitSeq(getenv("FORGE_CI_LIVE_CONSUMER_SETTINGS"), "\n") {
			if key, value, _ := strings.Cut(word, "="); key == settingNextVersion && value != "" {
				minimum = 3*timeoutConsumerRun + 30*time.Minute
			}
		}
	}
	budget := minimum
	if value := getenv("FORGE_CI_LIVE_TEST_TIMEOUT"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < minimum || parsed > 12*time.Hour {
			return "", fmt.Errorf("FORGE_CI_LIVE_TEST_TIMEOUT must be between %s and 12h, got %q: %w", minimum, value, errs.ErrValidation)
		}
		budget = parsed
	}
	return budget.String(), nil
}
