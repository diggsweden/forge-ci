// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlab_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/adapters/gitlab"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	"github.com/diggsweden/forge-ci/internal/testutil/fakegitserver"
)

const (
	glNotes  = "/api/v4/projects/" + glProject + "/merge_requests/7/notes"
	glMarker = "<!-- marker -->"
)

func prComment() provider.PRComment {
	return provider.PRComment{Repository: "examplescope/repo", Number: 7, Marker: glMarker, Body: "## Result"}
}

// The comment job's provider has no token in its environment: the one it
// uses is the one passed in.
func newPRCommentProvider(srv *fakegitserver.Server) *gitlab.Provider {
	return &gitlab.Provider{APIBaseOverride: srv.URL(), HTTPClient: srv.Client(), Env: envFunc(nil)}
}

func serveSelf(srv *fakegitserver.Server) {
	srv.OnGet("/api/v4/user", func(req fakegitserver.Request) fakegitserver.Response {
		if req.Header.Get("PRIVATE-TOKEN") != "comment-token" {
			return fakegitserver.Response{Status: 401, Body: `{}`}
		}

		return fakegitserver.Response{Status: 200, Body: `{"id":5}`}
	})
}

func TestUpsertPRComment_CreatesTheFirstComment(t *testing.T) {
	t.Parallel()

	srv := fakegitserver.NewGitLab(t)
	serveSelf(srv)
	srv.OnGet(glNotes, func(fakegitserver.Request) fakegitserver.Response {
		return fakegitserver.Response{Status: 200, Body: `[{"id":1,"body":"a review remark","author":{"id":9}}]`}
	})

	var posted string

	srv.OnPost(glNotes, func(req fakegitserver.Request) fakegitserver.Response {
		posted = string(req.Body)

		return fakegitserver.Response{Status: 201, Body: `{}`}
	})

	if err := newPRCommentProvider(srv).UpsertPRComment(context.Background(), "comment-token", prComment()); err != nil {
		t.Fatalf("UpsertPRComment = %v", err)
	}

	var sent struct {
		Body string `json:"body"`
	}

	if err := json.Unmarshal([]byte(posted), &sent); err != nil || sent.Body != glMarker+"\n## Result" {
		t.Fatalf("posted %s (%v), want the marker line and the result", posted, err)
	}

	if changed := append(sentTo(srv, http.MethodPut), sentTo(srv, http.MethodDelete)...); len(changed) != 0 {
		t.Fatalf("a note was edited or deleted: %v", changed)
	}
}

// sentTo returns the paths of the requests sent with method, in order.
func sentTo(srv *fakegitserver.Server, method string) []string {
	var paths []string

	for _, req := range srv.Requests() {
		if req.Method == method {
			paths = append(paths, req.Path)
		}
	}

	return paths
}

// serveDuplicateResults lists what two pipelines that listed at the same
// moment left, newest first, and accepts the edit of the newest result.
func serveDuplicateResults(srv *fakegitserver.Server, updated *bool) {
	serveSelf(srv)
	srv.OnGet(glNotes, func(fakegitserver.Request) fakegitserver.Response {
		return fakegitserver.Response{Status: 200, Body: `[
			{"id":7,"body":"quoting <!-- marker -->","author":{"id":5}},
			{"id":4,"body":"<!-- marker -->\nforged","author":{"id":9}},
			{"id":3,"body":"<!-- marker -->\nsystem","system":true,"author":{"id":5}},
			{"id":2,"body":"<!-- marker -->\nold result","author":{"id":5}},
			{"id":1,"body":"<!-- marker -->\nolder result","author":{"id":5}}]`}
	})
	srv.OnPut(glNotes+"/2", func(req fakegitserver.Request) fakegitserver.Response {
		*updated = strings.Contains(string(req.Body), "## Result")

		return fakegitserver.Response{Status: 200, Body: `{}`}
	})
}

// The newest of the pipeline's results is replaced and its older one
// deleted. A note with the marker that another account wrote, a system note,
// and the account's own note that only quotes the marker are not the
// pipeline's to edit or delete.
func TestUpsertPRComment_ReplacesItsNewestCommentAndDeletesItsOlderOnes(t *testing.T) {
	t.Parallel()

	srv := fakegitserver.NewGitLab(t)

	var updated bool

	serveDuplicateResults(srv, &updated)
	srv.On(http.MethodDelete, glNotes+"/1", func(fakegitserver.Request) fakegitserver.Response {
		// Write first: the result is in place before a duplicate goes.
		if !updated {
			return fakegitserver.Response{Status: 409, Body: `{}`}
		}

		return fakegitserver.Response{Status: 204}
	})

	if err := newPRCommentProvider(srv).UpsertPRComment(context.Background(), "comment-token", prComment()); err != nil {
		t.Fatalf("UpsertPRComment = %v", err)
	}

	if !updated {
		t.Fatal("the pipeline's newest comment was not replaced")
	}

	if edited := sentTo(srv, http.MethodPut); len(edited) != 1 {
		t.Fatalf("edited %v, want only the pipeline's newest result", edited)
	}

	if deleted := sentTo(srv, http.MethodDelete); len(deleted) != 1 || deleted[0] != glNotes+"/1" {
		t.Fatalf("deleted %v, want only the pipeline's older result", deleted)
	}

	if requests := srv.Requests(); requests[len(requests)-1].Method != http.MethodDelete {
		t.Fatal("the deletion was not the last request, after the edit")
	}

	if posted := sentTo(srv, http.MethodPost); len(posted) != 0 {
		t.Fatalf("a second comment was created: %v", posted)
	}
}

// The result is posted when the deletion is refused, so the pipeline does
// not fail over a duplicate.
func TestUpsertPRComment_SucceedsWhenADuplicateCannotBeDeleted(t *testing.T) {
	t.Parallel()

	srv := fakegitserver.NewGitLab(t)

	var updated bool

	serveDuplicateResults(srv, &updated)
	srv.On(http.MethodDelete, glNotes+"/1", func(fakegitserver.Request) fakegitserver.Response {
		return fakegitserver.Response{Status: 403, Body: `{}`}
	})

	if err := newPRCommentProvider(srv).UpsertPRComment(context.Background(), "comment-token", prComment()); err != nil {
		t.Fatalf("UpsertPRComment = %v, want the refused deletion ignored", err)
	}

	if !updated {
		t.Fatal("the pipeline's newest comment was not replaced")
	}

	if deleted := sentTo(srv, http.MethodDelete); len(deleted) != 1 {
		t.Fatalf("deleted %v, want one attempt", deleted)
	}
}

func TestUpsertPRComment_RefusesWithoutATokenBeforeAnyRequest(t *testing.T) {
	t.Parallel()

	srv := fakegitserver.NewGitLab(t)

	err := newPRCommentProvider(srv).UpsertPRComment(context.Background(), "", prComment())
	if !errors.Is(err, errs.ErrPermissionDenied) {
		t.Fatalf("UpsertPRComment = %v, want permission denied", err)
	}

	if len(srv.Requests()) != 0 {
		t.Fatal("a request was sent without a token")
	}
}
