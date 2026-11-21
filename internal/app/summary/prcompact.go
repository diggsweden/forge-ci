// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package summary

import (
	"fmt"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	domainsummary "github.com/diggsweden/forge-ci/internal/domain/summary"
)

// PRChange describes the submitting change, independently of the run actor and
// the revision actually tested. Missing fields are not rendered.
type PRChange struct {
	Number, URL, Author                                            string
	SourceRepository, TargetRepository, TargetBranch, SourceCommit string
	NanolinterURL                                                  string
	ResultsUnavailable                                             bool
	MergeRequest                                                   bool
}

// nanolinterLabel is the row the Nanolinter job link belongs to.
const nanolinterLabel = "Nanolinter"

func compactPRSummary(in PRSummaryInput, quality domainsummary.StageResultEnvelope) string {
	change := in.Change

	rows := in.Jobs
	if len(rows) == 0 {
		for _, target := range []struct{ key, name string }{
			{pipeline.TargetNanolinter, nanolinterLabel}, {pipeline.TargetMegalinter, "MegaLinter"}, {pipeline.TargetSwift, "Swift checks"},
		} {
			if result := quality.TargetResult(target.key); result != domainsummary.ResultSkipped {
				rows = append(rows, domainsummary.JobRow{Label: target.name, Result: result})
			}
		}
	}

	var out strings.Builder

	_, _ = fmt.Fprintf(&out, "## Quality checks — %s\n\n", compactVerdict(change.ResultsUnavailable, rows))
	writeChangeContext(&out, in)

	if change.ResultsUnavailable {
		out.WriteString("A planned check may not have run. Open **Normalize PR quality stage result** for the exact error.\n")
	} else if len(rows) > 0 {
		out.WriteString("| Check | Result |\n| --- | --- |\n")

		for _, row := range rows {
			label := domainsummary.LiteralText(row.Label)
			if row.Label == nanolinterLabel && validSummaryLink(change.NanolinterURL) {
				label = fmt.Sprintf("[%s](%s)", label, change.NanolinterURL)
			}

			_, _ = fmt.Fprintf(&out, "| %s | %s |\n", label, compactCheckResult(row.Result))
		}
	}

	if validSummaryLink(in.RunURL) {
		if change.MergeRequest {
			_, _ = fmt.Fprintf(&out, "\n[Open pipeline](%s) for Build and test and the overall merge gate.\n", in.RunURL)
		} else {
			_, _ = fmt.Fprintf(&out, "\n[Open workflow run](%s)\n", in.RunURL)
		}
	}

	return out.String()
}

// compactVerdict is the one word of the heading: a failure outranks a check
// that did not finish, and an unreadable result is never a pass.
func compactVerdict(unavailable bool, rows []domainsummary.JobRow) string {
	switch {
	case unavailable:
		return "Incomplete"
	case len(rows) == 0:
		return "No checks selected"
	}

	verdict := "Passed"

	for _, row := range rows {
		if row.Result == domainsummary.ResultFailure {
			return "Failed"
		}

		if row.Result != domainsummary.ResultSuccess {
			verdict = "Incomplete"
		}
	}

	return verdict
}

func writeChangeContext(out *strings.Builder, in PRSummaryInput) {
	change := in.Change

	var context []string

	if change.Number != "" {
		prefix := "PR #"
		if change.MergeRequest {
			prefix = "MR !"
		}

		label := prefix + domainsummary.LiteralText(change.Number)
		if validSummaryLink(change.URL) {
			label = fmt.Sprintf("[%s](%s)", label, change.URL)
		}

		context = append(context, label)
	}

	if change.Author != "" {
		context = append(context, "Opened by "+prCode("@"+change.Author))
	}

	if len(context) > 0 {
		out.WriteString(strings.Join(context, " · ") + "\n\n")
	}

	from, to := prRef(change.SourceRepository, in.Branch), prRef(change.TargetRepository, change.TargetBranch)
	if from != "" && to != "" {
		_, _ = fmt.Fprintf(out, "**From:** %s → **Into:** %s\n\n", from, to)
	} else if from != "" {
		_, _ = fmt.Fprintf(out, "**From:** %s\n\n", from)
	}

	if change.SourceCommit != "" && !strings.EqualFold(change.SourceCommit, in.Commit) {
		_, _ = fmt.Fprintf(out, "**Source commit:** %s\n\n", prCode(provider.ShortSHA(change.SourceCommit)))
	}

	if in.Commit != "" {
		_, _ = fmt.Fprintf(out, "**Tested revision:** %s\n\n", prCode(provider.ShortSHA(in.Commit)))
	}
}

func prRef(repo, branch string) string {
	if repo != "" && branch != "" {
		return prCode(repo + ":" + branch)
	}

	if branch != "" {
		return prCode(branch)
	}

	if repo != "" {
		return prCode(repo)
	}

	return ""
}

func prCode(value string) string { return "`" + domainsummary.SanitizeCell(value) + "`" }

func compactCheckResult(result domainsummary.Result) string {
	switch result {
	case domainsummary.ResultSuccess:
		return "Passed"
	case domainsummary.ResultSkipped:
		return "Not run"
	case domainsummary.ResultCancelled:
		return "Cancelled"
	default:
		return "**Failed** — open the job log for the cause"
	}
}
