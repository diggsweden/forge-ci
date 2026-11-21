// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package summary

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diggsweden/forge-ci/internal/domain/ci"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	domainsummary "github.com/diggsweden/forge-ci/internal/domain/summary"
)

// PRSummaryInput drives `summary pr`. The QualityStageResultJSON is the
// quality stage result-json output; the use case extracts each linter's result
// by name.
type PRSummaryInput struct {
	Change                 *PRChange // Opt in to the compact view.
	ProjectType            string
	Branch                 string
	Commit                 string // full SHA — first 7 chars rendered
	Actor                  string
	RunURL                 string
	QualityStageResultJSON string
	// Jobs, when set, are the status table's rows in place of the quality-stage
	// targets: GitLab and Forgejo name their own jobs.
	Jobs []domainsummary.JobRow
	// Now is baked in for deterministic testing. Empty → time.Now().
	Now time.Time
}

// PRSummary appends the PR summary block to the step summary.
func PRSummary(ctx context.Context, sink ci.SummarySink, in PRSummaryInput) error {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	short := provider.ShortSHA(in.Commit)

	quality, err := stageResultFor(in.QualityStageResultJSON, "pr-quality")
	if err != nil {
		return fmt.Errorf("quality-stage result-json: %w", err)
	}

	if in.Change != nil {
		return sink.Append(ctx, compactPRSummary(in, quality))
	}

	var builder strings.Builder

	_, _ = fmt.Fprintf(&builder, "# Pull Request Summary\n\n")
	_, _ = fmt.Fprintf(&builder, "## Overview\n")
	_, _ = fmt.Fprintf(&builder, "| Property | Value |\n")
	_, _ = fmt.Fprintf(&builder, "|----------|-------|\n")
	_, _ = fmt.Fprintf(&builder, "| **Project Type** | `%s` |\n", domainsummary.SanitizeCell(in.ProjectType))
	_, _ = fmt.Fprintf(&builder, "| **Branch** | `%s` |\n", domainsummary.SanitizeCell(in.Branch))
	_, _ = fmt.Fprintf(&builder, "| **Commit** | `%s` |\n", domainsummary.SanitizeCell(short))
	_, _ = fmt.Fprintf(&builder, "| **Checked By** | @%s |\n", domainsummary.SanitizeCell(in.Actor))
	_, _ = fmt.Fprintf(&builder, "| **Checked At** | %s |\n", now.UTC().Format("2006-01-02 15:04:05 UTC"))
	_, _ = fmt.Fprintf(&builder, "\n## Quality Check Status\n")
	_, _ = fmt.Fprintf(&builder, "| Check | Status |\n")

	_, _ = fmt.Fprintf(&builder, "|-------|--------|\n")

	switch {
	case len(in.Jobs) > 0:
		writeJobRows(&builder, in.Jobs)
	case strings.TrimSpace(in.QualityStageResultJSON) != "":
		writeQualityRows(&builder, quality)
	default:
		builder.WriteString("| Lint | − |\n| Swift Lint | − |\n")
	}

	_, _ = fmt.Fprintf(&builder, "\n## Resources\n")
	builder.WriteString(resourceLine("Workflow Run", in.RunURL))

	return sink.Append(ctx, builder.String())
}

// StageResult refuses a selected target that was skipped. An emitted skipped
// result therefore represents a check that was not selected.
func writeQualityRows(b *strings.Builder, quality domainsummary.StageResultEnvelope) {
	wrote := false

	for _, row := range []struct{ key, label string }{
		{pipeline.TargetNanolinter, nanolinterLabel},
		{pipeline.TargetMegalinter, "MegaLinter"},
		{pipeline.TargetSwift, "Swift Lint"},
	} {
		if result := quality.TargetResult(row.key); result != domainsummary.ResultSkipped {
			_, _ = fmt.Fprintf(b, "| %s | %s |\n", row.label, domainsummary.StatusIcon(string(result)))
			wrote = true
		}
	}

	if !wrote {
		b.WriteString("| Quality checks | None selected |\n")
	}
}

// writeJobRows writes one status-table row per named job.
func writeJobRows(b *strings.Builder, jobs []domainsummary.JobRow) {
	for _, job := range jobs {
		_, _ = fmt.Fprintf(b, "| `%s` | %s |\n", domainsummary.SanitizeCell(job.Label), domainsummary.StatusIcon(string(job.Result)))
	}
}
