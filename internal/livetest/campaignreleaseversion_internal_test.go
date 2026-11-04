// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"cmp"
	"encoding/xml"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/sbom"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// workflowVersionBump is a Forgejo release workflow's version-bump: input.
var workflowVersionBump = regexp.MustCompile(`(?m)^[ \t]+version-bump:[ \t]*\S`)

// A release case must not request a version its demo neither declares nor
// bumps to: the release refuses a commit that does not carry the version it
// is asked for, and that costs a live run to learn. So every release case of
// the checked-in campaigns either bumps or requests, explicitly or by
// default, the version its demo's manifest declares.
func TestCampaignReleaseCases_RequestWhatTheirDemoDeclaresOrBump(t *testing.T) {
	t.Parallel()

	repo := reporoot.Path(t)

	lanes, err := filepath.Glob(filepath.Join(repo, "test", "live", "campaigns", "*.cases"))
	if err != nil || len(lanes) == 0 {
		t.Fatalf("no campaign cases files: %v", err)
	}

	for _, lane := range lanes {
		// A lane file is named <campaign>-<provider>.cases.
		provider := strings.TrimSuffix(filepath.Base(lane), ".cases")
		provider = provider[strings.LastIndex(provider, "-")+1:]

		if !isCampaignProvider(provider) {
			t.Fatalf("%s: the name does not end in a provider", lane)
		}

		err := eachLine(lane, func(number int, line string) error {
			words := strings.Fields(line)
			if len(words) < 3 || words[0] != "consumer" || words[2] != TrialRelease {
				return nil
			}

			consumer := filepath.Join(repo, "test", "live", "demos", words[1], "consumer")
			requested, bumps := releaseCaseVersion(t, provider, consumer, words[3:])

			if declared := declaredDemoVersion(t, consumer); declared != "" && !bumps && requested != declared {
				t.Errorf("%s:%d: the %s release requests %s, but the demo declares %s and its release does not bump; give the case version=%s",
					filepath.Base(lane), number, words[1], requested, declared, declared)
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// releaseCaseVersion returns the version a release case's settings request
// and whether its release bumps the version, read by the trial's own parsers.
func releaseCaseVersion(t *testing.T, provider, consumer string, words []string) (string, bool) {
	t.Helper()

	// Each @NAME stands for a value campaign.env would give.
	env := map[string]string{}

	for _, word := range words {
		if _, value, _ := strings.Cut(word, "="); strings.HasPrefix(value, "@") {
			env[value[1:]] = "/campaign/" + value[1:]
		}
	}

	_, settings, err := campaignSettings(provider, env, words)
	if err != nil {
		t.Fatal(err)
	}

	// Both forges' trials request 0.0.1 when no version is named.
	defaults, err := ParseTrialSettings(TrialRelease, nil)
	if err != nil {
		t.Fatal(err)
	}

	if provider == campaignGitLab {
		parsed, err := ParseGitLabReleaseSettings(strings.Join(settings, "\n"))
		if err != nil {
			t.Fatal(err)
		}

		return cmp.Or(parsed.Version, defaults.Version), parsed.VersionBump != ""
	}

	parsed, err := ParseTrialSettings(TrialRelease, settings)
	if err != nil {
		t.Fatal(err)
	}

	workflow, err := os.ReadFile(filepath.Join(consumer, ".forgejo", "workflows", parsed.Workflow))
	if err != nil {
		t.Fatal(err)
	}

	return parsed.Version, workflowVersionBump.Match(workflow)
}

// declaredDemoVersion returns the version the demo's package.json, Cargo.toml
// or pom.xml (the project's own <version>) declares, or "" for none.
func declaredDemoVersion(t *testing.T, consumer string) string {
	t.Helper()

	read := func(name string) []byte {
		body, err := os.ReadFile(filepath.Join(consumer, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}

		return body
	}

	cargo, err := sbom.CargoTOMLVersion(read("Cargo.toml"), nil)
	if err != nil {
		t.Fatal(err)
	}

	var pom struct {
		Version string `xml:"version"`
	}

	if body := read("pom.xml"); body != nil {
		if err := xml.Unmarshal(body, &pom); err != nil {
			t.Fatal(err)
		}
	}

	return cmp.Or(sbom.PackageJSONVersion(read("package.json")), cargo, pom.Version)
}
