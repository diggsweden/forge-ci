// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package summary_test

import (
	"fmt"
	"strings"
	"testing"

	appsummary "github.com/diggsweden/forge-ci/internal/app/summary"
	domaingit "github.com/diggsweden/forge-ci/internal/domain/git"
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
	// One check: the heading is its result, so no table repeats it.
	require.Contains(t, body, "[Open the Nanolinter job log]("+job+")")
	require.NotContains(t, body, "| Check | Result |")
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
	require.Contains(t, body, "[Open the Nanolinter job log](https://github.example/org/repo/actions/runs/1/job/2)")
	require.NotContains(t, body, "| Check | Result |")
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

// The commits of the change fold out under the context: the closed header
// answers whether everything is signed and signed off, and the author's own
// text inside is literal.
func TestCompactPRSummaryListsTheCommitsOfTheChange(t *testing.T) {
	t.Parallel()

	commits := []domaingit.Commit{
		{
			SHA: "a1b2c3d4e5f6", Author: "Ada Lind", Subject: "fix(ci): show #7 once @all", Signature: "SSH",
			Trailers: []domaingit.Trailer{
				{Key: "Signed-off-by", Value: "Ada Lind <ada@example.org>"},
				{Key: "Fixes", Value: "#42"},
				{Key: "Co-authored-by", Value: "Bo Ek <bo@example.org>"},
			},
		},
		{SHA: "d4e5f6a7b8c9", Author: "Bo Ek", Subject: "docs: explain", Signature: "GPG"},
		{SHA: "0718293a4b5c", Author: "Cy | Berg", Subject: "test: cover"},
	}

	sink := &fakeSummarySink{}
	require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
		Branch: "feature", Commit: "0123456789",
		Change: &appsummary.PRChange{Number: "7", Commits: commits},
		Jobs:   []domainsummary.JobRow{{Label: "Nanolinter", Result: domainsummary.ResultSuccess}},
	}))
	body := sink.buf.String()
	require.Contains(t, body, "<summary><b>Commits:</b> 3 · signed: 2 of 3 (1 SSH, 1 GPG) · signed-off: 1 of 3 · co-authored: 1</summary>")
	require.Contains(t, body, "| `a1b2c3d` | Ada Lind | SSH | Signed-off-by: Ada Lind<br>Fixes: `#42`<br>Co-authored-by: Bo Ek | fix(ci)&#58; show &#35;7 once &#64;all |")
	require.Contains(t, body, "| `d4e5f6a` | Bo Ek | GPG | – | docs&#58; explain |")
	require.Contains(t, body, "| `0718293` | Cy &#124; Berg | – | – | test&#58; cover |")
	require.NotContains(t, body, "example.org")
	require.Less(t, strings.Index(body, "**Tested revision:**"), strings.Index(body, "<details>"))

	// With the request's address the hash opens that commit in the request,
	// in the form each forge uses.
	for _, tc := range []struct {
		mergeRequest bool
		url, want    string
	}{
		{false, "https://forge.example/org/repo/pulls/7", "| [`a1b2c3d`](https://forge.example/org/repo/pulls/7/commits/a1b2c3d4e5f6) |"},
		{true, "https://gitlab.example/g/p/-/merge_requests/7", "| [`a1b2c3d`](https://gitlab.example/g/p/-/merge_requests/7/diffs?commit_id=a1b2c3d4e5f6) |"},
	} {
		sink = &fakeSummarySink{}
		require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
			Commit: "0123456789",
			Change: &appsummary.PRChange{Number: "7", URL: tc.url, MergeRequest: tc.mergeRequest, Commits: commits},
		}))
		require.Contains(t, sink.buf.String(), tc.want)
	}

	// No readable range: no fold-out, and nothing else changes.
	sink = &fakeSummarySink{}
	require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
		Branch: "feature", Commit: "0123456789", Change: &appsummary.PRChange{Number: "7"},
	}))
	require.NotContains(t, sink.buf.String(), "Commits:")
}

func TestCompactPRSummaryShortensALongCommitList(t *testing.T) {
	t.Parallel()

	commits := make([]domaingit.Commit, 23)
	for i := range commits {
		commits[i] = domaingit.Commit{SHA: fmt.Sprintf("%07d", i), Author: "Ada", Subject: strings.Repeat("s", 80), Signature: "SSH"}
	}

	sink := &fakeSummarySink{}
	require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
		Commit: "0123456789", Change: &appsummary.PRChange{Number: "7", Commits: commits},
	}))
	body := sink.buf.String()
	require.Contains(t, body, "<b>Commits:</b> 23 · signed: 23 SSH</summary>")

	// Unsigned work says so in the closed line.
	sink = &fakeSummarySink{}
	require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
		Commit: "0123456789", Change: &appsummary.PRChange{Number: "7", Commits: []domaingit.Commit{{SHA: "abcdef0"}, {SHA: "abcdef1"}}},
	}))
	require.Contains(t, sink.buf.String(), "<b>Commits:</b> 2 · signed: 0 of 2</summary>")
	require.Equal(t, 20, strings.Count(body, "| Ada | SSH |"))
	require.Contains(t, body, "…and 3 more.")
	require.Contains(t, body, strings.Repeat("s", 71)+"… |")

	// The author and the trailers are the change's text too: an overlong
	// one is shortened, so the results below the list stay in a comment.
	long := strings.Repeat("x", 4000)
	sink = &fakeSummarySink{}
	require.NoError(t, appsummary.PRSummary(t.Context(), sink, appsummary.PRSummaryInput{
		Commit: "0123456789", Change: &appsummary.PRChange{Number: "7", Commits: []domaingit.Commit{{
			SHA: "abcdef0", Author: long, Subject: "s",
			Trailers: []domaingit.Trailer{{Key: "Signed-off-by", Value: long + " <a@example.com>"}, {Key: "Refs", Value: long}},
		}}},
	}))
	require.Less(t, sink.buf.Len(), 2000)
}
