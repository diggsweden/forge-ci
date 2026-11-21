// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package summary_test

import (
	"testing"

	appsummary "github.com/diggsweden/forge-ci/internal/app/summary"
	domainsummary "github.com/diggsweden/forge-ci/internal/domain/summary"
	"github.com/stretchr/testify/require"
)

func TestCompactMergeRequestSummaryDoesNotClaimABuildResult(t *testing.T) {
	t.Parallel()

	const (
		pipeline = "https://gitlab.example/group/project/-/pipelines/10"
		job      = "https://gitlab.example/group/project/-/jobs/11"
	)

	sink := &fakeSummarySink{}
	require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
		Branch: "feature", Commit: "0123456789", RunURL: pipeline, Actor: "not-the-author",
		Change: &appsummary.PRChange{
			MergeRequest: true, Number: "7", URL: "https://gitlab.example/group/project/-/merge_requests/7",
			SourceRepository: "fork/project", TargetRepository: "group/project", TargetBranch: "main",
			NanolinterURL: job,
		},
		Jobs: []domainsummary.JobRow{{Label: "Nanolinter", Result: domainsummary.ResultFailure}},
	}))
	body := sink.buf.String()
	require.Contains(t, body, "## Quality checks — Failed")
	require.Contains(t, body, "[MR !7](https://gitlab.example/group/project/-/merge_requests/7)")
	require.Contains(t, body, "**From:** `fork/project:feature` → **Into:** `group/project:main`")
	require.Contains(t, body, "[Nanolinter]("+job+") | **Failed**")
	require.Contains(t, body, "[Open pipeline]("+pipeline+") for Build and test and the overall merge gate.")
	require.NotContains(t, body, "| Build and test |")
	require.NotContains(t, body, "not-the-author")
}

func TestCompactPRSummaryLinksTheFailedCheckAndShowsBothRevisions(t *testing.T) {
	t.Parallel()

	sink := &fakeSummarySink{}
	require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
		Branch: "feat/example", Commit: "7a21c4e123456", Actor: "rerun-actor",
		Change: &appsummary.PRChange{
			Number: "42", URL: "https://github.example/org/repo/pull/42", Author: "contributor",
			SourceRepository: "contributor/repo", TargetRepository: "org/repo", TargetBranch: "main",
			SourceCommit: "a1204bc123456", NanolinterURL: "https://github.example/org/repo/actions/runs/1/job/2",
		},
		QualityStageResultJSON: stageResultJSON(t, "pr-quality", map[string]string{
			"nanolinter": "failure", "megalinter": "skipped", "swift": "skipped",
		}),
	}))
	body := sink.buf.String()
	require.Contains(t, body, "## Quality checks — Failed")
	require.Contains(t, body, "[PR #42](https://github.example/org/repo/pull/42) · Opened by `@contributor`")
	require.Contains(t, body, "**From:** `contributor/repo:feat/example` → **Into:** `org/repo:main`")
	require.Contains(t, body, "**Source commit:** `a1204bc`")
	require.Contains(t, body, "**Tested revision:** `7a21c4e`")
	require.Contains(t, body, "| [Nanolinter](https://github.example/org/repo/actions/runs/1/job/2) | **Failed**")
	require.NotContains(t, body, "MegaLinter")
	require.NotContains(t, body, "rerun-actor")
}

func TestCompactPRSummaryMissingResultsAreNotAFinding(t *testing.T) {
	t.Parallel()

	sink := &fakeSummarySink{}
	require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
		Branch: "fork`\n![fake](https://example.invalid/x)", Commit: "a1204bc123456",
		Change: &appsummary.PRChange{
			Number: "42", URL: "https://user:pass@github.example/org/repo/pull/42",
			SourceCommit: "a1204bc123456", ResultsUnavailable: true,
		},
	}))
	body := sink.buf.String()
	require.Contains(t, body, "## Quality checks — Incomplete")
	require.Contains(t, body, "A planned check may not have run")
	require.NotContains(t, body, "**Failed**")
	require.NotContains(t, body, "Source commit")
	require.NotContains(t, body, "user:pass")
	require.NotContains(t, body, "\n![fake]")
}
