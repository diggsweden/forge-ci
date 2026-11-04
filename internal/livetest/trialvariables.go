// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
	"mvdan.cc/sh/v3/syntax"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// ParseTrialVariables accepts literal consumer variables, one KEY=value per
// line. Provider-specific recipe options and token bindings are not variables.
func ParseTrialVariables(input string) (map[string]string, error) {
	variables := make(map[string]string)

	for word := range strings.SplitSeq(input, "\n") {
		if word == "" {
			continue
		}

		key, value, found := strings.Cut(word, "=")
		if !found || !trialName.MatchString(key) {
			return nil, fmt.Errorf("consumer variables must be KEY=value with an uppercase name: %w", errs.ErrValidation)
		}

		if _, duplicate := variables[key]; duplicate {
			return nil, fmt.Errorf("consumer variable %s was given twice: %w", key, errs.ErrValidation)
		}

		variables[key] = value
	}

	return variables, nil
}

// GitLabReleaseSettings are what the caller of a GitLab consumer release trial
// decides: the release policy's version bump, and literal variables for the
// consumer's build.
type GitLabReleaseSettings struct {
	// VersionBump is the release component's version-bump input: maven/npm/cargo, or ""
	// for none.
	VersionBump string
	// Version is the X.Y.Z the release requests, or "" for the trial's default.
	Version string
	// NextVersion requests a second release from the signed main left by the first.
	NextVersion string
	// JobUser is the release component's job-user input, a UID or UID:GID the
	// job-image jobs run as, or "" for the image's own user.
	JobUser   string
	Variables map[string]string
}

// settingVersionBump is a lowercase setting of a GitLab release trial, as
// version is; every uppercase KEY=value is a consumer variable.
const settingVersionBump = "version-bump"

// settingJobUser is the GitLab release trial's job-user, the release policy's
// input of that name.
const settingJobUser = "job-user"

// jobUserValue is UID[:GID] in canonical decimal: a leading zero would pass
// the policy's input check and then never equal what `id -u` prints.
var jobUserValue = regexp.MustCompile(`^(0|[1-9][0-9]*)(:(0|[1-9][0-9]*))?$`)

// ParseGitLabReleaseSettings reads one setting per line: version-bump=maven|npm|cargo
// and job-user=UID[:GID], which the trial writes into the release policy,
// version=X.Y.Z, the release it requests, or a literal KEY=value consumer
// variable (ParseTrialVariables).
func ParseGitLabReleaseSettings(input string) (GitLabReleaseSettings, error) {
	var settings GitLabReleaseSettings

	variables := make([]string, 0)

	for word := range strings.SplitSeq(input, "\n") {
		key, value, _ := strings.Cut(word, "=")

		switch key {
		case settingVersionBump:
			if settings.VersionBump != "" || !slices.Contains([]string{"maven", "npm", "cargo"}, value) {
				return GitLabReleaseSettings{}, fmt.Errorf("%s must be given once, as maven, npm or cargo: %w", settingVersionBump, errs.ErrValidation)
			}

			settings.VersionBump = value
		case settingJobUser:
			if err := setTrialJobUser(&settings.JobUser, value); err != nil {
				return GitLabReleaseSettings{}, err
			}
		case settingVersion:
			if err := setTrialVersion(&settings.Version, key, value); err != nil {
				return GitLabReleaseSettings{}, err
			}
		case "next-version":
			if err := setTrialVersion(&settings.NextVersion, key, value); err != nil {
				return GitLabReleaseSettings{}, err
			}
		default:
			variables = append(variables, word)
		}
	}

	parsed, err := ParseTrialVariables(strings.Join(variables, "\n"))
	if err != nil {
		return GitLabReleaseSettings{}, err
	}

	if err := refusePolicyBuilderVariables(parsed); err != nil {
		return GitLabReleaseSettings{}, err
	}

	settings.Variables = parsed
	if !validNextTrialVersion(settings) {
		return GitLabReleaseSettings{}, fmt.Errorf("next-version requires an increasing explicit version and isolated version-bump mode: %w", errs.ErrValidation)
	}

	return settings, nil
}

// refusePolicyBuilderVariables refuses build variables that name what the
// release policy's inputs set: the image builder, which a build variable
// would override inside the build alone, and the job user, which the jobs
// refuse when a variable differs from job-user.
func refusePolicyBuilderVariables(variables map[string]string) error {
	for name, instead := range map[string]string{
		"CI_IMAGE_BUILDER":           "select kaniko with " + GitLabImageBuilderEnv + "=kaniko",
		"CI_IMAGE_BUILDER_JOB_IMAGE": "select kaniko with " + GitLabImageBuilderEnv + "=kaniko",
		"RC_JOB_USER":                "give " + settingJobUser + "=UID[:GID]",
	} {
		if _, set := variables[name]; set {
			return fmt.Errorf("%s is the GitLab release policy's to set, not a build variable; %s: %w", name, instead, errs.ErrValidation)
		}
	}

	return nil
}

func setTrialJobUser(destination *string, value string) error {
	if *destination != "" || !jobUserValue.MatchString(value) {
		return fmt.Errorf("%s must be given once, as UID or UID:GID: %w", settingJobUser, errs.ErrValidation)
	}

	*destination = value

	return nil
}

func setTrialVersion(destination *string, key, value string) error {
	version, valid := trialVersionValue(value)
	if *destination != "" || !valid {
		return fmt.Errorf("%s must be given once, as X.Y.Z: %w", key, errs.ErrValidation)
	}

	*destination = version

	return nil
}

func validNextTrialVersion(settings GitLabReleaseSettings) bool {
	return settings.NextVersion == "" || (settings.Version != "" && settings.VersionBump != "" && semver.Compare("v"+settings.NextVersion, "v"+settings.Version) > 0)
}

// GitLabConsumerBuildScript and GitLabConsumerExportScript are the consumer's
// release build, and its export of files from a Containerfile stage, which
// the policy's export jobs run with the selected image builder.
const (
	GitLabConsumerBuildScript  = ".gitlab/build-release.sh"
	GitLabConsumerExportScript = ".gitlab/export-release.sh"
)

// GitLabConsumerBuildCommand scopes literal settings to the consumer's build
// subprocess, without adding variables to the independent signing policy jobs.
func GitLabConsumerBuildCommand(variables map[string]string) (string, error) {
	return GitLabConsumerCommand(GitLabConsumerBuildScript, variables)
}

// GitLabConsumerCommand runs the consumer's script with the literal settings
// scoped to it.
func GitLabConsumerCommand(script string, variables map[string]string) (string, error) {
	command := "bash " + script
	if len(variables) == 0 {
		return command, nil
	}

	words := []string{"env", "--"}

	for _, key := range slices.Sorted(maps.Keys(variables)) {
		word, err := syntax.Quote(key+"="+variables[key], syntax.LangBash)
		if err != nil {
			return "", fmt.Errorf("quote consumer variable %s: %w", key, err)
		}

		words = append(words, word)
	}

	return strings.Join(words, " ") + " " + command, nil
}
