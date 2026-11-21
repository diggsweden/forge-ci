// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package summary_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diggsweden/forge-ci/internal/app/summary"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

type recordingCommenter struct {
	token   string
	comment provider.PRComment
	calls   int
}

func (c *recordingCommenter) UpsertPRComment(_ context.Context, token string, comment provider.PRComment) error {
	c.token, c.comment = token, comment
	c.calls++

	return nil
}

func TestPRCommentPostsTheResultsUnderTheMarker(t *testing.T) {
	t.Parallel()

	commenter := &recordingCommenter{}

	err := summary.PRComment(context.Background(), commenter, io.Discard, summary.PRCommentInput{
		Repository: "group/app", Number: 12, Body: "\n## Quality checks — Passed\n", Token: "t",
	})
	if err != nil {
		t.Fatalf("PRComment = %v", err)
	}

	want := provider.PRComment{Repository: "group/app", Number: 12, Marker: summary.PRCommentMarker, Body: "## Quality checks — Passed"}
	if commenter.comment != want || commenter.token != "t" {
		t.Fatalf("comment = %+v with token %q, want %+v", commenter.comment, commenter.token, want)
	}
}

// A failed earlier job leaves no results: nothing is posted, and the old
// comment is not replaced by an empty one.
func TestPRCommentPostsNothingForEmptyResults(t *testing.T) {
	t.Parallel()

	commenter := &recordingCommenter{}

	err := summary.PRComment(context.Background(), commenter, io.Discard, summary.PRCommentInput{Repository: "group/app", Number: 12, Body: " \n", Token: "t"})
	if !errors.Is(err, errs.ErrMissingInput) || commenter.calls != 0 {
		t.Fatalf("PRComment = %v after %d calls, want missing input and none", err, commenter.calls)
	}
}

func TestPRCommentShortensAtALineAndSaysSo(t *testing.T) {
	t.Parallel()

	commenter := &recordingCommenter{}
	body := strings.Repeat("| check | passed |\n", 5000)

	err := summary.PRComment(context.Background(), commenter, io.Discard, summary.PRCommentInput{Repository: "group/app", Number: 12, Body: body, Token: "t"})
	if err != nil {
		t.Fatalf("PRComment = %v", err)
	}

	got := commenter.comment.Body
	if len(got) > 61_000 || !strings.HasSuffix(got, "_Shortened. The complete results are in the job's artifact._") ||
		!strings.Contains(got, "| check | passed |\n\n_Shortened") {
		t.Fatalf("comment of %d bytes is not shortened at a line: …%q", len(got), got[len(got)-90:])
	}
}

// A text without a line to cut at is still cut between characters.
func TestPRCommentShortensBetweenCharacters(t *testing.T) {
	t.Parallel()

	commenter := &recordingCommenter{}

	err := summary.PRComment(context.Background(), commenter, io.Discard, summary.PRCommentInput{Repository: "group/app", Number: 12, Body: strings.Repeat("é", 40_000), Token: "t"})
	if err != nil {
		t.Fatalf("PRComment = %v", err)
	}

	if got := commenter.comment.Body; !utf8.ValidString(got) || !strings.Contains(got, "_Shortened") {
		t.Fatalf("comment of %d bytes is not valid shortened text", len(got))
	}
}

// The change's author chose part of the text. It is shown, and it does
// nothing. Each case is a form the lab's GitLab read as a mention or ran as
// a quick action before it was made inert; an @ inside a word never was one.
func TestPRCommentTakesMentionsAndQuickActionsOutOfTheText(t *testing.T) {
	t.Parallel()

	const z = "\u200b"

	for _, tc := range []struct{ name, in, want string }{
		{"mention", "@all see (@group/team) @a @b", "@" + z + "all see (@" + z + "group/team) @" + z + "a @" + z + "b"},
		{"after a dot, dash or slash", "x.@all x-@all dir/@all", "x.@" + z + "all x-@" + z + "all dir/@" + z + "all"},
		{"after a stray backtick", "`@all", "`@" + z + "all"},
		{"in a table cell", "|@all|", "|@" + z + "all|"},
		{"doubled", "@@all", "@" + z + "@" + z + "all"},
		{"as a character reference", "&#64;all &#x40;all &commat;all &#064;all", "&#64;" + z + "all &#x40;" + z + "all &commat;" + z + "all &#064;" + z + "all"},
		{"after a letter or digit GitLab does not count as one", "é@root ß@all ١@all", "é@" + z + "root ß@" + z + "all ١@" + z + "all"},
		{"inside a word", "uses actions/checkout@v4, mail a@example.com", "uses actions/checkout@v4, mail a@example.com"},
		{"quick action", "/close", z + "/close"},
		{"indented quick action", "  \t/approve", z + "  \t/approve"},
		{"quick action behind a carriage return", "ok\n\r/close", "ok\n" + z + "/close"},
		{"quick action behind other controls", "ok\n\x00\x0b/close", "ok\n" + z + "/close"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			commenter := &recordingCommenter{}

			err := summary.PRComment(context.Background(), commenter, io.Discard, summary.PRCommentInput{Repository: "group/app", Number: 12, Body: "## Result\n" + tc.in, Token: "t"})
			if err != nil {
				t.Fatalf("PRComment = %v", err)
			}

			if got, want := commenter.comment.Body, "## Result\n"+tc.want; got != want {
				t.Fatalf("comment = %q\nwant      %q", got, want)
			}
		})
	}
}
