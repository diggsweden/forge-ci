// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package summary

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

// PRCommentMarker identifies the one results comment of a pull request. It
// is an HTML comment, so the forge renders nothing for it.
const PRCommentMarker = "<!-- forge-ci:pr-check-results -->"

// maxPRCommentBytes keeps the comment readable and far below the forge's
// own limit; the full text stays in the job's artifact.
const maxPRCommentBytes = 60_000

// PRCommentInput is what the comment job knows: the request, the text an
// earlier job wrote, and the token that may comment and nothing else.
type PRCommentInput struct {
	Repository string
	Number     int
	Body       string
	Token      string
}

// PRComment publishes the PR check results as the request's one comment,
// replacing the comment of the previous pipeline.
//
// The text comes from a job that read the change, so its author chose part of
// it. It must not act on the forge: a mention would notify people and a
// quick action would run with the comment token. References to issues and
// requests are left: the forge notes them on what they name, as it does for
// any comment.
func PRComment(ctx context.Context, commenter provider.PRCommenter, out io.Writer, in PRCommentInput) error {
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return fmt.Errorf("the PR check results are empty; nothing to comment: %w", errs.ErrMissingInput)
	}

	body = shortenPRComment(inertPRComment(body))

	err := commenter.UpsertPRComment(ctx, in.Token, provider.PRComment{
		Repository: in.Repository, Number: in.Number, Marker: PRCommentMarker, Body: body,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "PR check results posted as a comment on request %d of %s\n", in.Number, in.Repository)

	return nil
}

// zeroWidthSpace separates a character from what would make it a command
// without changing what a reader sees.
const zeroWidthSpace = "\u200b"

// GitLab reads a mention wherever an @ does not follow a word character, in
// the rendered text: after a dot, a slash, a stray backtick, and written as
// a character reference too. Every such @ is separated from the name. An @
// inside a word, as in action@v4 or an address, is no mention and stays.
// GitLab's word characters are ASCII: after é or ß an @ is a mention.
var mentionEntity = regexp.MustCompile(`(?i)&(#0*64|#x0*40|commat);`)

func inertMentions(line string) string {
	var out strings.Builder

	previous := ' '

	for _, r := range line {
		out.WriteRune(r)

		if r == '@' && !isASCIIWord(previous) {
			out.WriteString(zeroWidthSpace)
		}

		previous = r
	}

	return mentionEntity.ReplaceAllString(out.String(), "$0"+zeroWidthSpace)
}

func isASCIIWord(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// inertPRComment keeps the text and takes its commands away: no mention
// notifies anyone, and no line is a quick action. GitLab drops carriage
// returns before it looks for a quick action, so they and the other control
// characters go first.
func inertPRComment(body string) string {
	body = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}

		return r
	}, body)

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		line = inertMentions(line)

		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "/") {
			line = zeroWidthSpace + line
		}

		lines[i] = line
	}

	return strings.Join(lines, "\n")
}

// shortenPRComment cuts an overlong comment at a line. The forge renders
// each comment on its own, so what the cut leaves open ends with it.
func shortenPRComment(body string) string {
	if len(body) <= maxPRCommentBytes {
		return body
	}

	cut := strings.LastIndexByte(body[:maxPRCommentBytes], '\n')
	if cut <= 0 {
		// No line to cut at: back up to the start of a character.
		for cut = maxPRCommentBytes; cut > 0 && !utf8.RuneStart(body[cut]); cut-- {
		}
	}

	return body[:cut] + "\n\n_Shortened. The complete results are in the job's artifact._"
}
