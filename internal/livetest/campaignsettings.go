// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

type campaignLine struct {
	number int
	text   string
	words  []string
}

type campaignCases struct {
	settings map[string]string
	cases    []campaignLine
}

// CampaignRun keeps effective public facts separate from the optional selector.
// An unset selector is not an explicit Buildah requirement on consumer cases.
type CampaignRun struct {
	Settings        map[string]string
	BuilderSelector string
}

// Setting declarations precede all cases, are literal data, and describe the
// expected run rather than overriding it. Prepare and lane admission share this
// reader so a malformed declaration is refused before image preparation too.
func readCampaignCases(path string) (campaignCases, error) {
	file := campaignCases{settings: map[string]string{}}

	err := eachLine(path, func(number int, line string) error {
		text := strings.TrimSpace(line)
		words := strings.Fields(text)

		if len(words) == 0 || strings.HasPrefix(words[0], "#") {
			return nil
		}

		if words[0] != "setting" {
			file.cases = append(file.cases, campaignLine{number: number, text: text, words: words})

			return nil
		}

		if len(file.cases) != 0 {
			return fmt.Errorf("%s:%d: settings must precede every case: %w", path, number, errs.ErrValidation)
		}

		if len(words) != 2 {
			return fmt.Errorf("%s:%d: want setting <name>=<literal>: %w", path, number, errs.ErrValidation)
		}

		key, value, found := strings.Cut(words[1], "=")
		if !found || !campaignSetting.MatchString(key) || value == "" || strings.HasPrefix(value, "@") {
			return fmt.Errorf("%s:%d: want setting <name>=<literal>: %w", path, number, errs.ErrValidation)
		}

		if _, duplicate := file.settings[key]; duplicate {
			return fmt.Errorf("%s:%d: setting %s is declared twice: %w", path, number, key, errs.ErrValidation)
		}

		file.settings[key] = value

		return nil
	})
	if err != nil {
		return campaignCases{}, err
	}

	if len(file.cases) == 0 {
		return campaignCases{}, fmt.Errorf("%s names no case: %w", path, errs.ErrValidation)
	}

	return file, nil
}

func (file campaignCases) checkSettings(run map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(file.settings)) {
		actual, known := run[key]
		if !known || actual != file.settings[key] {
			return fmt.Errorf("declared setting %s does not match the effective run; select the declared setting before launching: %w", key, errs.ErrValidation)
		}
	}

	return nil
}

// CampaignRunSettings resolves credential-free run facts for declaration
// admission. It does not change the environment or choose settings for a lane.
func CampaignRunSettings(provider string, getenv func(string) string) (CampaignRun, error) {
	visibility := getenv(consumerVisibilityEnv)
	if _, err := parseConsumerVisibilities(visibility); err != nil {
		return CampaignRun{}, err
	}

	if visibility == "" {
		visibility = "both"
	}

	var builderEnv string

	switch provider {
	case campaignGitLab:
		builderEnv = GitLabImageBuilderEnv
	case campaignForgejo:
		builderEnv = ForgejoImageBuilderEnv
	default:
		return CampaignRun{}, fmt.Errorf("the provider is forgejo or gitlab: %w", errs.ErrValidation)
	}

	selector := getenv(builderEnv)

	builder, valid := imageBuilderValue(selector)
	if !valid {
		return CampaignRun{}, fmt.Errorf("%s must be buildah or kaniko: %w", builderEnv, errs.ErrValidation)
	}

	return CampaignRun{Settings: map[string]string{"visibility": visibility, "image-builder": builder}, BuilderSelector: selector}, nil
}

func (run CampaignRun) caseSettings(args []string) (map[string]string, error) {
	if len(args) < 3 || args[0] != "test-live-forgejo-consumer" {
		return run.Settings, nil
	}

	trial, err := ParseTrialSettings(args[2], args[3:])
	if err != nil {
		return nil, err
	}

	builder, err := ForgejoConsumerImageBuilder(trial.Variables, run.BuilderSelector)
	if err != nil {
		return nil, err
	}

	facts := maps.Clone(run.Settings)
	if facts == nil {
		facts = map[string]string{}
	}

	facts["image-builder"] = builder

	return facts, nil
}
