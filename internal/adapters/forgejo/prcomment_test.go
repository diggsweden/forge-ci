// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejo_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/diggsweden/forge-ci/internal/adapters/forgejo"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

const (
	fjComments = "/api/v1/repos/owner/repo/issues/7/comments"
	fjMarker   = "<!-- marker -->"
)

func prComment() provider.PRComment {
	return provider.PRComment{Repository: "owner/repo", Number: 7, Marker: fjMarker, Body: "## Result"}
}

type prCommentRequest struct {
	method, path, body string
}

// prCommentForge answers the account and the comment list, and records every
// request. Only the comment token is accepted: the provider's own token in
// the environment must not be the one sent.
type prCommentForge struct {
	mu       sync.Mutex
	comments string
	// deleteStatus answers a comment's deletion; zero is the forge's 204.
	deleteStatus int
	requests     []prCommentRequest
}

func (f *prCommentForge) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	// The in-memory client hands over the outgoing request: no body on a GET.
	var raw []byte
	if request.Body != nil {
		raw, _ = io.ReadAll(request.Body)
	}

	f.mu.Lock()
	f.requests = append(f.requests, prCommentRequest{request.Method, request.URL.Path, string(raw)})
	f.mu.Unlock()

	status, body := http.StatusOK, "{}"

	switch {
	case request.Header.Get("Authorization") != "token comment-token":
		status = http.StatusUnauthorized
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/user":
		body = `{"id":5}`
	case request.Method == http.MethodGet && request.URL.Path == fjComments:
		body = f.comments
	case request.Method == http.MethodPost && request.URL.Path == fjComments:
		status = http.StatusCreated
	case request.Method == http.MethodPatch:
	case request.Method == http.MethodDelete:
		status = http.StatusNoContent
		if f.deleteStatus != 0 {
			status = f.deleteStatus
		}
	default:
		status = http.StatusNotFound
	}

	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)

	_, _ = response.Write([]byte(body))
}

func (f *prCommentForge) sent(method string) []prCommentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	var sent []prCommentRequest

	for _, request := range f.requests {
		if request.method == method {
			sent = append(sent, request)
		}
	}

	return sent
}

func newPRCommentProvider(forge *prCommentForge) *forgejo.Provider {
	return &forgejo.Provider{
		Env:             envMap(map[string]string{"FORGEJO_TOKEN": "release-token"}),
		HTTPClient:      inMemoryClient(forge),
		APIBaseOverride: "https://forgejo.invalid",
	}
}

func TestUpsertPRComment_CreatesTheFirstComment(t *testing.T) {
	t.Parallel()

	forge := &prCommentForge{comments: `[{"id":1,"body":"a review remark","user":{"id":9}}]`}

	if err := newPRCommentProvider(forge).UpsertPRComment(context.Background(), "comment-token", prComment()); err != nil {
		t.Fatalf("UpsertPRComment = %v", err)
	}

	posted := forge.sent(http.MethodPost)
	if len(posted) != 1 || posted[0].path != fjComments {
		t.Fatalf("posted %+v, want one comment on the pull request", posted)
	}

	var sent struct {
		Body string `json:"body"`
	}

	if err := json.Unmarshal([]byte(posted[0].body), &sent); err != nil || sent.Body != fjMarker+"\n## Result" {
		t.Fatalf("posted %s (%v), want the marker line and the result", posted[0].body, err)
	}

	if changed := append(forge.sent(http.MethodPatch), forge.sent(http.MethodDelete)...); len(changed) != 0 {
		t.Fatalf("a comment was edited or deleted: %+v", changed)
	}
}

// Two runs that listed at the same moment left two results. The list is
// oldest first.
const fjDuplicateResults = `[
	{"id":2,"body":"<!-- marker -->\nolder result","user":{"id":5}},
	{"id":3,"body":"<!-- marker -->\nold result","user":{"id":5}},
	{"id":4,"body":"<!-- marker -->\nforged","user":{"id":9}},
	{"id":6,"body":"quoting <!-- marker -->","user":{"id":5}}]`

// The newest of the workflow's results is replaced and its older one
// deleted. A comment with the marker that another account wrote, and the
// account's own comment that only quotes the marker, are not the workflow's
// to edit or delete.
func TestUpsertPRComment_ReplacesItsNewestCommentAndDeletesItsOlderOnes(t *testing.T) {
	t.Parallel()

	forge := &prCommentForge{comments: fjDuplicateResults}

	if err := newPRCommentProvider(forge).UpsertPRComment(context.Background(), "comment-token", prComment()); err != nil {
		t.Fatalf("UpsertPRComment = %v", err)
	}

	deleted := forge.sent(http.MethodDelete)
	if len(deleted) != 1 || deleted[0].path != "/api/v1/repos/owner/repo/issues/comments/2" {
		t.Fatalf("deleted %+v, want only the workflow's older result", deleted)
	}

	// Write first: the result is in place before a duplicate goes.
	if last := forge.requests[len(forge.requests)-1]; last.method != http.MethodDelete {
		t.Fatalf("last request was %s %s, want the deletion after the edit", last.method, last.path)
	}

	patched := forge.sent(http.MethodPatch)
	if len(patched) != 1 || patched[0].path != "/api/v1/repos/owner/repo/issues/comments/3" {
		t.Fatalf("edited %+v, want only the workflow's newest comment", patched)
	}

	var sent struct {
		Body string `json:"body"`
	}

	if err := json.Unmarshal([]byte(patched[0].body), &sent); err != nil || sent.Body != fjMarker+"\n## Result" {
		t.Fatalf("edited to %s (%v), want the marker line and the result", patched[0].body, err)
	}

	if posted := forge.sent(http.MethodPost); len(posted) != 0 {
		t.Fatalf("a second comment was created: %+v", posted)
	}
}

// The result is posted when the deletion is refused, so the run does not
// fail over a duplicate.
func TestUpsertPRComment_SucceedsWhenADuplicateCannotBeDeleted(t *testing.T) {
	t.Parallel()

	forge := &prCommentForge{comments: fjDuplicateResults, deleteStatus: http.StatusForbidden}

	if err := newPRCommentProvider(forge).UpsertPRComment(context.Background(), "comment-token", prComment()); err != nil {
		t.Fatalf("UpsertPRComment = %v, want the refused deletion ignored", err)
	}

	patched := forge.sent(http.MethodPatch)
	if len(patched) != 1 || patched[0].path != "/api/v1/repos/owner/repo/issues/comments/3" {
		t.Fatalf("edited %+v, want the workflow's newest comment", patched)
	}

	if deleted := forge.sent(http.MethodDelete); len(deleted) != 1 {
		t.Fatalf("deleted %+v, want one attempt", deleted)
	}
}

func TestUpsertPRComment_RefusesBadInputBeforeAnyRequest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		token   string
		change  func(*provider.PRComment)
		wantErr error
	}{
		{"no token", "", func(*provider.PRComment) {}, errs.ErrPermissionDenied},
		{"no number", "comment-token", func(c *provider.PRComment) { c.Number = 0 }, errs.ErrUsage},
		{"no marker", "comment-token", func(c *provider.PRComment) { c.Marker = "" }, errs.ErrUsage},
		{"no body", "comment-token", func(c *provider.PRComment) { c.Body = "" }, errs.ErrUsage},
		{"malformed repository", "comment-token", func(c *provider.PRComment) { c.Repository = "owner/" }, errs.ErrUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			forge := &prCommentForge{comments: `[]`}
			comment := prComment()
			tc.change(&comment)

			err := newPRCommentProvider(forge).UpsertPRComment(context.Background(), tc.token, comment)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("UpsertPRComment = %v, want %v", err, tc.wantErr)
			}

			if len(forge.requests) != 0 {
				t.Fatalf("requests were sent: %+v", forge.requests)
			}
		})
	}
}

// A token the forge refuses is a permission answer, and nothing is written.
func TestUpsertPRComment_ReportsARefusedToken(t *testing.T) {
	t.Parallel()

	forge := &prCommentForge{comments: `[]`}

	err := newPRCommentProvider(forge).UpsertPRComment(context.Background(), "other-token", prComment())
	if !errors.Is(err, errs.ErrPermissionDenied) {
		t.Fatalf("UpsertPRComment = %v, want permission denied", err)
	}

	if written := append(forge.sent(http.MethodPost), forge.sent(http.MethodPatch)...); len(written) != 0 {
		t.Fatalf("a comment was written: %+v", written)
	}
}
