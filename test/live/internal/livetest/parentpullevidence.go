// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
)

// DirectParentBuilds admits the caller's expected child/architecture set before
// the trial creates resources. Expectations must not be inferred from jobs that
// happened to run: that would hide missing matrix legs. Root bases are omitted.
func (settings *TrialSettings) DirectParentBuilds() ([]pipeline.BuildRow, error) {
	written := settings.Variables["DIRECT_PARENT_BUILDS_JSON"]

	required := settings.Variables["REQUIRE_DIRECT_PARENT"]
	if required != "" && required != variableTrue && required != "false" {
		return nil, fmt.Errorf("REQUIRE_DIRECT_PARENT must be true or false: %w", errs.ErrValidation)
	}

	if required != variableTrue {
		if written != "" {
			return nil, fmt.Errorf("DIRECT_PARENT_BUILDS_JSON requires REQUIRE_DIRECT_PARENT=true: %w", errs.ErrValidation)
		}

		return nil, nil
	}

	var expected []pipeline.BuildRow

	decoder := json.NewDecoder(strings.NewReader(written))
	decoder.DisallowUnknownFields()

	if settings.Event != TrialRelease || decoder.Decode(&expected) != nil || len(expected) == 0 || decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("direct parent release acceptance requires nonempty DIRECT_PARENT_BUILDS_JSON with child name/arch entries: %w", errs.ErrValidation)
	}

	if err := requireUniqueParentBuilds(expected); err != nil {
		return nil, err
	}

	return expected, nil
}

func requireUniqueParentBuilds(expected []pipeline.BuildRow) error {
	seen := map[pipeline.BuildRow]bool{}
	for _, row := range expected {
		if row.Name == "" || strings.ContainsAny(row.Name, " ,()\r\n\t") || (row.Arch != "amd64" && row.Arch != "arm64") || seen[row] {
			return fmt.Errorf("DIRECT_PARENT_BUILDS_JSON requires unique named children with amd64 or arm64 architectures: %w", errs.ErrValidation)
		}

		seen[row] = true
	}

	return nil
}

type parentPullJob struct {
	RunJob

	Steps []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"steps"`
}

// pulledAndBuilt reports a started, successful job whose parent pull and
// native build steps both succeeded.
func (job parentPullJob) pulledAndBuilt() bool {
	pulled, built := false, false

	for _, step := range job.Steps {
		if step.Name == "Fetch the parent by digest" && step.Status == runSuccess {
			pulled = true
		}

		if step.Name == "Build native layout" && step.Status == runSuccess {
			built = true
		}
	}

	return pulled && built && job.Status == runSuccess && job.Started()
}

// RequireDirectParentPull requires positive runner evidence as well as the
// absence of the old layout handoff. A reuse-only run cannot prove a pull.
func RequireDirectParentPull(tb TB, target Target, repo string, runID int64, artifacts []RunArtifact, expected []pipeline.BuildRow) {
	tb.Helper()

	var builds []parentPullJob

	for _, job := range RunJobs(tb, target, repo, runID) {
		if !strings.HasPrefix(job.Name, "Build: base (") {
			continue
		}

		var detail parentPullJob
		readActions(tb, target, repo, "/actions/jobs/"+strconv.FormatInt(job.ID, 10), &detail)
		builds = append(builds, detail)
	}

	for _, finding := range directParentPullFindings(builds, artifacts, expected) {
		tb.Errorf("direct parent acceptance: %s", finding)
	}
}

func directParentPullFindings(jobs []parentPullJob, artifacts []RunArtifact, expected []pipeline.BuildRow) []string {
	var findings []string
	if len(expected) == 0 {
		findings = append(findings, "no expected child builds; an unspecified matrix is not acceptance")
	}

	for _, row := range expected {
		name := fmt.Sprintf("Build: base (%s, %s)", row.Arch, row.Name)
		matches := 0

		for _, job := range jobs {
			if job.Name != name {
				continue
			}

			matches++

			if !job.pulledAndBuilt() {
				findings = append(findings, fmt.Sprintf("job %q did not complete a successful native parent pull and build", job.Name))
			}
		}

		if matches != 1 {
			findings = append(findings, fmt.Sprintf("expected exactly one job %q, found %d", name, matches))
		}
	}

	for _, artifact := range artifacts {
		if strings.HasPrefix(artifact.Name, "flavors-stage-inputs-") {
			findings = append(findings, "unexpected parent layout handoff artifact: "+artifact.Name)
		}
	}

	return findings
}
