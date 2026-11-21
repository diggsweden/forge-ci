// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

// prCommentPages bounds the search for the earlier comments: the newest
// notes come first, and a request with more than this many notes after the
// last result gets a new comment instead of a longer search.
const prCommentPages = 5

type mergeRequestNote struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	System bool   `json:"system"`
	Author struct {
		ID int64 `json:"id"`
	} `json:"author"`
}

// UpsertPRComment keeps one note on the merge request: it replaces the
// newest note that carries the marker and that the token's own account
// wrote, creates one when there is none, and then deletes that account's
// older marked notes, which two pipelines listing at the same moment leave
// behind. A note of another account with the same marker is left alone, so
// nobody can make the pipeline edit or delete their text or hide its result
// behind theirs.
//
// Implements provider.PRCommenter.
func (p *Provider) UpsertPRComment(ctx context.Context, token string, comment provider.PRComment) error {
	if token == "" {
		return fmt.Errorf("pull request comment token is empty: %w", errs.ErrPermissionDenied)
	}

	if comment.Number <= 0 || comment.Marker == "" || comment.Body == "" {
		return fmt.Errorf("pull request comment needs a number, a marker and a body: %w", errs.ErrUsage)
	}

	if _, err := p.ParseRepository(comment.Repository); err != nil {
		return err
	}

	apiBase, _ := p.apiContext()
	headers := map[string]string{headerPrivateToken: token, "Content-Type": contentTypeJSON}
	notes := projectEndpoint(apiBase, comment.Repository) +
		"/merge_requests/" + strconv.Itoa(comment.Number) + "/notes"

	payload, err := json.Marshal(map[string]string{"body": comment.Marker + "\n" + comment.Body})
	if err != nil {
		return fmt.Errorf("gitlab merge request comment: %w", err)
	}

	existing, err := p.ownMarkedNotes(ctx, apiBase, notes, headers, comment.Marker)
	if err != nil {
		return fmt.Errorf("gitlab merge request comment: %w", err)
	}

	var stale []int64

	if len(existing) != 0 {
		stale = existing[1:]
		err = putJSON(ctx, p.HTTPClient, notes+"/"+strconv.FormatInt(existing[0], 10), headers, payload)
	} else {
		err = postJSON(ctx, p.HTTPClient, notes, headers, payload)
	}

	if err != nil {
		return fmt.Errorf("gitlab merge request comment: %w", err)
	}

	// The current result is posted, so a duplicate that could not be deleted
	// does not fail the pipeline: the next one tries again.
	for _, id := range stale {
		_ = deleteJSON(ctx, p.HTTPClient, notes+"/"+strconv.FormatInt(id, 10), headers)
	}

	return nil
}

// ownMarkedNotes returns the ids of the notes that start with the marker and
// belong to the token's account, newest first.
func (p *Provider) ownMarkedNotes(
	ctx context.Context, apiBase, notes string, headers map[string]string, marker string,
) ([]int64, error) {
	body, err := getJSON(ctx, p.HTTPClient, strings.TrimRight(apiBase, "/")+"/api/v4/user", headers)
	if err != nil {
		return nil, err
	}

	var self struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &self); err != nil || self.ID == 0 {
		return nil, fmt.Errorf("the token's account could not be read: %w", errs.ErrMalformedInput)
	}

	var own []int64

	for page := 1; page <= prCommentPages; page++ {
		body, err := getJSON(ctx, p.HTTPClient,
			fmt.Sprintf("%s?sort=desc&order_by=created_at&per_page=%d&page=%d", notes, gitlabPageSize, page), headers)
		if err != nil {
			return nil, err
		}

		var found []mergeRequestNote
		if err := json.Unmarshal(body, &found); err != nil {
			return nil, fmt.Errorf("the merge request's notes could not be read: %w", errs.ErrMalformedInput)
		}

		for _, note := range found {
			if !note.System && note.Author.ID == self.ID && strings.HasPrefix(note.Body, marker) {
				own = append(own, note.ID)
			}
		}

		if len(found) < gitlabPageSize {
			break
		}
	}

	return own, nil
}
