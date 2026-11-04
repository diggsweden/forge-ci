// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// Scenario inputs select fixed recipes and exact tests. They cannot inject
// environment assignments or arbitrary regular expressions into the launcher.
func campaignGitLabScenarioArgs(provider string, env map[string]string, words []string) ([]string, bool, error) {
	if len(words) < 2 {
		return nil, false, nil
	}

	name := words[1]
	release, releaseErr := GitLabConsumerReleaseScenario(name)
	protection := name == "TestGitLabRelease_ConsumerBranchProtection"
	probe := name == "TestGitLabRelease_BuildImageCapability"
	interrupt := name == "TestGitLabWorkflow_InterruptedJob"

	if releaseErr != nil && !protection && !probe && !interrupt {
		return nil, false, nil
	}

	if provider != campaignGitLab {
		return nil, true, fmt.Errorf("this scenario requires a GitLab lane: %w", errs.ErrValidation)
	}

	if interrupt {
		if len(words) != 2 {
			return nil, true, fmt.Errorf("manual interruption takes no case settings: %w", errs.ErrValidation)
		}

		return []string{"test-live-gitlab-interrupted-job"}, true, nil
	}

	if probe {
		args, err := campaignBuildImageProbe(env, words[2:])

		return args, true, err
	}

	// A bare scenario remains the ordinary test-suite entry point. Explicit
	// checkout/archive words use the operator-facing recipe instead.
	if len(words) == 2 {
		return nil, false, nil
	}

	archives, settings, err := campaignSettings(provider, env, words[2:])
	if err != nil {
		return nil, true, err
	}

	args, err := campaignConsumerScenarioArgs(env, words, release, protection, archives, settings)

	return args, true, err
}

func campaignBuildImageProbe(env map[string]string, words []string) ([]string, error) {
	archives, settings, err := campaignSettings(campaignGitLab, env, words)
	if err != nil {
		return nil, err
	}

	if len(archives) != 1 || archives["build-archive"] == "" || len(settings) != 0 {
		return nil, fmt.Errorf("build-image probe takes build-archive= alone: %w", errs.ErrValidation)
	}

	return []string{"test-live-gitlab-build-image-capability", archives["build-archive"]}, nil
}

func campaignScenarioCheckout(settings []string) (string, []string, error) {
	checkout := ""

	var remaining []string

	for _, setting := range settings {
		key, value, _ := strings.Cut(setting, "=")
		if key != "checkout" {
			remaining = append(remaining, setting)

			continue
		}

		if checkout != "" || !filepath.IsAbs(value) || !isDir(value) {
			return "", nil, fmt.Errorf("checkout= must name one absolute reviewed consumer directory: %w", errs.ErrValidation)
		}

		checkout = value
	}

	if checkout == "" {
		return "", nil, fmt.Errorf("scenario requires checkout=<absolute reviewed consumer>: %w", errs.ErrValidation)
	}

	return checkout, remaining, nil
}

func campaignConsumerScenarioArgs(env map[string]string, words []string, release string, protection bool, archives map[string]string, settings []string) ([]string, error) {
	checkout, remaining, err := campaignScenarioCheckout(settings)
	if err != nil {
		return nil, err
	}

	if protection {
		if len(archives) != 0 || len(remaining) != 0 {
			return nil, fmt.Errorf("branch protection takes checkout= alone: %w", errs.ErrValidation)
		}

		return []string{"test-live-gitlab-consumer-branch-protection", checkout}, nil
	}

	remaining = append(remaining, "scenario="+release)
	if err := campaignAdmitGitLabConsumer(checkout, TrialRelease, words[2:], remaining, env); err != nil {
		return nil, err
	}

	return consumerRecipe(campaignGitLab, TrialRelease, checkout, archives, remaining)
}
