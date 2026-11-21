// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package summary

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/git"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	domainsummary "github.com/diggsweden/forge-ci/internal/domain/summary"
)

const (
	// maxListedCommits and maxListedTrailers keep the fold-out readable on
	// a long change; the header line still counts every commit.
	maxListedCommits  = 20
	maxListedTrailers = 5
	maxSubjectRunes   = 72

	signedOffBy  = "signed-off-by"
	coAuthoredBy = "co-authored-by"
)

// headerTrailers are the trailers the closed header counts, in its order.
var headerTrailers = []struct{ key, label string }{
	{signedOffBy, "signed-off"}, {coAuthoredBy, "co-authored"},
	{"reviewed-by", "reviewed"}, {"acked-by", "acked"}, {"tested-by", "tested"},
}

// writeCommits writes the change's commits as a fold-out: a header that
// answers the first questions closed, and one row per commit inside. The
// author chose every name, subject and trailer, so all of it is literal text.
func writeCommits(out *strings.Builder, change *PRChange) {
	commits := change.Commits
	if len(commits) == 0 {
		return
	}

	_, _ = fmt.Fprintf(out, "<details>\n<summary><b>Commits:</b> %s</summary>\n\n", commitsHeader(commits))
	out.WriteString("| Commit | Author | Signature | Trailers | Subject |\n| --- | --- | --- | --- | --- |\n")

	for _, commit := range commits[:min(len(commits), maxListedCommits)] {
		signature := commit.Signature
		if signature == "" {
			signature = "–"
		}

		_, _ = fmt.Fprintf(out, "| %s | %s | %s | %s | %s |\n",
			commitCell(change, commit.SHA), domainsummary.LiteralText(shortenSubject(commit.Author)),
			signature, trailerCell(commit.Trailers), domainsummary.LiteralText(shortenSubject(commit.Subject)))
	}

	if more := len(commits) - maxListedCommits; more > 0 {
		_, _ = fmt.Fprintf(out, "\n…and %d more.\n", more)
	}

	out.WriteString("\nSigned means a signature is present. Whether it is valid is the forge's Verified mark on the commit.\n\n</details>\n\n")
}

var commitID = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// commitCell is the short hash, linked to the commit as the request shows
// it: that view exists for a change from a fork too, and keeps the reader in
// the request.
func commitCell(change *PRChange, sha string) string {
	short := domainsummary.InlineCode(provider.ShortSHA(sha))
	if !validSummaryLink(change.URL) || !commitID.MatchString(sha) {
		return short
	}

	if change.MergeRequest {
		return fmt.Sprintf("[%s](%s/diffs?commit_id=%s)", short, change.URL, sha)
	}

	return fmt.Sprintf("[%s](%s/commits/%s)", short, change.URL, sha)
}

func commitsHeader(commits []git.Commit) string {
	total := len(commits)
	parts := []string{strconv.Itoa(total), "signed: " + signedSummary(commits)}

	for _, trailer := range headerTrailers {
		count := 0

		for _, commit := range commits {
			if hasTrailer(commit, trailer.key) {
				count++
			}
		}

		switch {
		case count == 0:
		case count == total || trailer.key != signedOffBy:
			parts = append(parts, fmt.Sprintf("%s: %d", trailer.label, count))
		default:
			// A missing sign-off is what a reviewer looks for.
			parts = append(parts, fmt.Sprintf("%s: %d of %d", trailer.label, count, total))
		}
	}

	return strings.Join(parts, " · ")
}

func signedSummary(commits []git.Commit) string {
	var kinds []string

	signed := 0

	for _, kind := range git.SignatureKinds {
		count := 0

		for _, commit := range commits {
			if commit.Signature == kind {
				count++
			}
		}

		if count > 0 {
			kinds = append(kinds, fmt.Sprintf("%d %s", count, kind))
			signed += count
		}
	}

	switch signed {
	case 0:
		return fmt.Sprintf("0 of %d", len(commits))
	case len(commits):
		return strings.Join(kinds, ", ")
	default:
		return fmt.Sprintf("%d of %d (%s)", signed, len(commits), strings.Join(kinds, ", "))
	}
}

func hasTrailer(commit git.Commit, key string) bool {
	for _, trailer := range commit.Trailers {
		if strings.EqualFold(trailer.Key, key) {
			return true
		}
	}

	return false
}

// trailerCell writes one trailer per line. A person is shown by name, without
// the address; any other value is code, so a reference such as #42 stays text.
func trailerCell(trailers []git.Trailer) string {
	if len(trailers) == 0 {
		return "–"
	}

	lines := make([]string, 0, maxListedTrailers+1)

	for _, trailer := range trailers[:min(len(trailers), maxListedTrailers)] {
		value := domainsummary.InlineCode(shortenSubject(trailer.Value))
		if name, address, found := strings.Cut(trailer.Value, " <"); found && strings.HasSuffix(address, ">") {
			value = domainsummary.LiteralText(shortenSubject(name))
		}

		lines = append(lines, domainsummary.LiteralText(shortenSubject(trailer.Key))+": "+value)
	}

	if more := len(trailers) - maxListedTrailers; more > 0 {
		lines = append(lines, fmt.Sprintf("+%d more", more))
	}

	return strings.Join(lines, "<br>")
}

func shortenSubject(subject string) string {
	runes := []rune(subject)
	if len(runes) <= maxSubjectRunes {
		return subject
	}

	return string(runes[:maxSubjectRunes-1]) + "…"
}
