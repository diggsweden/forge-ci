// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"encoding/json"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func TestDirectParentEvidenceMatchesWorkflowStepNames(t *testing.T) {
	t.Parallel()

	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(reporoot.ReadFile(t, ".forgejo/workflows/base-images.yml"), &workflow))
	// Exercise the evidence reader with the actual source names returned by
	// Forgejo, without assuming interpolation of a step's display name.
	job := parentPullJob{
		Name:   "Build: base (arm64, child)",
		Status: "success"}
	taskID := int64(42)

	job.TaskID = &taskID
	for _, step := range workflow.Jobs["build"].Steps {
		job.Steps = append(job.Steps, struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		}{Name: step.Name, Status: "success"})
	}

	require.Empty(t, directParentPullFindings([]parentPullJob{job}, nil, []pipeline.BuildRow{{Name: "child", Arch: "arm64"}}))
}

func TestDirectParentEvidenceRequiresAnExecutedPull(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, job string
		artifacts []RunArtifact
		accepted  bool
	}{
		{"successful child", `{"name":"Build: base (arm64, child)","status":"success","task_id":42,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"success"}]}`, nil, true},
		{"successful amd64 child", `{"name":"Build: base (amd64, child)","status":"success","task_id":42,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"success"}]}`, nil, true},
		{"missing build", `{"status":"success","task_id":42,"steps":[{"name":"Fetch the parent by digest","status":"success"}]}`, nil, false},
		{"skipped build", `{"status":"success","task_id":42,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"skipped"}]}`, nil, false},
		{"failed build despite successful job", `{"status":"success","task_id":42,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"failure"}]}`, nil, false},
		{"failed job despite successful steps", `{"status":"failure","task_id":42,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"success"}]}`, nil, false},
		{"reused parent", `{"status":"success","steps":[{"name":"Fetch the parent by digest","status":"skipped"}]}`, nil, false},
		{"no step evidence", `{"status":"success"}`, nil, false},
		{"pull denied", `{"status":"failure","steps":[{"name":"Fetch the parent by digest","status":"failure"}]}`, nil, false},
		{"builder killed after pull", `{"status":"failure","steps":[{"name":"Fetch the parent by digest","status":"success"}]}`, nil, false},
		{"no runner", `{"status":"success","task_id":0,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"success"}]}`, nil, false},
		{"old artifact handoff", `{"status":"success","task_id":42,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"success"}]}`, []RunArtifact{{Name: "flavors-stage-inputs-child-arm64"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var job parentPullJob
			require.NoError(t, json.Unmarshal([]byte(tc.job), &job))

			arch := "arm64"
			if tc.name == "successful amd64 child" {
				arch = "amd64"
			}

			if job.Name == "" {
				job.Name = "Build: base (arm64, child)"
			}

			findings := directParentPullFindings([]parentPullJob{job}, tc.artifacts, []pipeline.BuildRow{{Name: "child", Arch: arch}})
			require.Equal(t, tc.accepted, len(findings) == 0, findings)
		})
	}

	require.NotEmpty(t, directParentPullFindings(nil, nil, nil), "an empty run is not acceptance")
}

func TestDirectParentEvidenceRequiresEveryExpectedArchitecture(t *testing.T) {
	t.Parallel()

	var amd64, arm64 parentPullJob
	require.NoError(t, json.Unmarshal([]byte(`{"name":"Build: base (amd64, child)","status":"success","task_id":42,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"success"}]}`), &amd64))
	require.NoError(t, json.Unmarshal([]byte(`{"name":"Build: base (arm64, child)","status":"success","task_id":43,"steps":[{"name":"Fetch the parent by digest","status":"success"},{"name":"Build native layout","status":"success"}]}`), &arm64))

	expected := []pipeline.BuildRow{{Name: "child", Arch: "amd64"}, {Name: "child", Arch: "arm64"}}
	require.Empty(t, directParentPullFindings([]parentPullJob{amd64, arm64}, nil, expected))
	require.NotEmpty(t, directParentPullFindings([]parentPullJob{amd64, arm64, arm64}, nil, expected))
	arm64.Status = "failure"
	require.NotEmpty(t, directParentPullFindings([]parentPullJob{amd64, arm64}, nil, expected))
	arm64.Status = "success"

	require.NotEmpty(t, directParentPullFindings([]parentPullJob{amd64}, nil, expected))

	arm64.Steps[0].Status = "skipped"
	require.NotEmpty(t, directParentPullFindings([]parentPullJob{amd64, arm64}, nil, expected))
	arm64.Steps = arm64.Steps[1:]
	require.NotEmpty(t, directParentPullFindings([]parentPullJob{amd64, arm64}, nil, expected))
	// A successful root job is not a substitute for a missing child leg.
	arm64.Name = "Build: base (arm64, base)"
	require.NotEmpty(t, directParentPullFindings([]parentPullJob{amd64, arm64}, nil, expected))
}

func TestDirectParentExpectationsAreAdmittedBeforeTrial(t *testing.T) {
	t.Parallel()

	for _, written := range []string{"", "[]", "null", `[{"name":"child","arch":"riscv64"}]`, `[{"name":"child","arch":"amd64"},{"name":"child","arch":"amd64"}]`, `[{"name":"child","arch":"amd64","arches":["amd64","arm64"]}]`, `[{"name":"child","arch":"amd64"}] []`} {
		_, err := ParseTrialSettings(TrialRelease, []string{"REQUIRE_DIRECT_PARENT=true", "DIRECT_PARENT_BUILDS_JSON=" + written})
		require.Error(t, err)
	}

	_, err := ParseTrialSettings(TrialRelease, []string{"REQUIRE_DIRECT_PARENT=true"})
	require.Error(t, err)

	for _, value := range []string{"TRUE", "yes", "1", "ture"} {
		_, err := ParseTrialSettings(TrialRelease, []string{"REQUIRE_DIRECT_PARENT=" + value})
		require.Error(t, err, "a typo must not silently disable acceptance")
	}

	settings, err := ParseTrialSettings(TrialRelease, []string{"REQUIRE_DIRECT_PARENT=true", `DIRECT_PARENT_BUILDS_JSON=[{"name":"tools","arch":"amd64"},{"name":"debug","arch":"amd64"}]`})
	require.NoError(t, err)
	expected, err := settings.DirectParentBuilds()
	require.NoError(t, err)
	require.Len(t, expected, 2)
}
