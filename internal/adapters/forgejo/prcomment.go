// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejo

import (
	"context"
	"fmt"
	"strings"

	"code.gitea.io/sdk/gitea"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

var _ provider.PRCommenter = (*Provider)(nil)

// prCommentPages and prCommentPageSize bound the search for the earlier
// comment. Forgejo lists a pull request's comments oldest first, takes no
// sort order and today answers with all of them at once; a server that
// pages is followed this far, and a request whose own comment lies further
// on gets a new comment instead of a longer search.
const (
	prCommentPages    = 5
	prCommentPageSize = 50
)

// UpsertPRComment keeps one comment on the pull request: it replaces the
// newest comment that carries the marker and that the token's own account
// wrote, creates one when there is none, and then deletes that account's
// older marked comments, which two runs listing at the same moment leave
// behind. A comment of another account with the same marker is left alone,
// so nobody can make the workflow edit or delete their text or hide its
// result behind theirs.
//
// Implements provider.PRCommenter.
func (p *Provider) UpsertPRComment(ctx context.Context, token string, comment provider.PRComment) error {
	if token == "" {
		return fmt.Errorf("pull request comment token is empty: %w", errs.ErrPermissionDenied)
	}

	if comment.Number <= 0 || comment.Marker == "" || comment.Body == "" {
		return fmt.Errorf("pull request comment needs a number, a marker and a body: %w", errs.ErrUsage)
	}

	owner, name, err := splitRepo(comment.Repository)
	if err != nil {
		return err
	}

	client, err := p.clientWithToken(ctx, token)
	if err != nil {
		return err
	}

	number := int64(comment.Number)

	existing, err := ownMarkedComments(client, owner, name, number, comment.Marker)
	if err != nil {
		return fmt.Errorf("forgejo pull request comment: %w", err)
	}

	var (
		resp  *gitea.Response
		stale []int64
	)

	body := comment.Marker + "\n" + comment.Body

	if len(existing) != 0 {
		newest := len(existing) - 1
		stale = existing[:newest]
		_, resp, err = client.EditIssueComment(owner, name, existing[newest], gitea.EditIssueCommentOption{Body: body})
	} else {
		_, resp, err = client.CreateIssueComment(owner, name, number, gitea.CreateIssueCommentOption{Body: body})
	}

	if err != nil {
		return fmt.Errorf("forgejo pull request comment: %w", classifyErr(resp, err))
	}

	// The current result is posted, so a duplicate that could not be deleted
	// does not fail the run: the next one tries again.
	for _, id := range stale {
		_, _ = client.DeleteIssueComment(owner, name, id)
	}

	return nil
}

// ownMarkedComments returns the ids of the comments that start with the
// marker and belong to the token's account, oldest first.
func ownMarkedComments(client *gitea.Client, owner, name string, number int64, marker string) ([]int64, error) {
	self, resp, err := client.GetMyUserInfo()
	if err != nil {
		return nil, classifyErr(resp, err)
	}

	if self == nil || self.ID == 0 {
		return nil, fmt.Errorf("the token's account could not be read: %w", errs.ErrMalformedInput)
	}

	var own []int64

	opt := gitea.ListIssueCommentOptions{Page: 1, PageSize: prCommentPageSize}

	for range prCommentPages {
		found, resp, err := client.ListIssueComments(owner, name, number, opt)
		if err != nil {
			return nil, classifyErr(resp, err)
		}

		for _, c := range found {
			if c != nil && c.Poster != nil && c.Poster.ID == self.ID && strings.HasPrefix(c.Body, marker) {
				own = append(own, c.ID)
			}
		}

		// No next page is also what a server that does not page answers.
		if resp == nil || resp.NextPage <= opt.Page {
			break
		}

		opt.Page = resp.NextPage
	}

	return own, nil
}
