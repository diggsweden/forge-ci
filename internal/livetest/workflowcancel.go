// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

var errObservedRun = errors.New("observed workflow run refused")

// Cancel only the exact run already observed by this wait, before its caller's
// keep-scratch cleanup archives the repository. No run listing or prefix sweep.
func cancelObservedWorkflowRun(ctx context.Context, target Target, repo string, observed WorkflowRun) error {
	return stopObservedWorkflowRun(ctx, observed,
		func(ctx context.Context, id int64) (WorkflowRun, error) {
			var run WorkflowRun

			_, err := decodeQuiet(ctx, target, actionsURL(target, repo, "/actions/runs/"+strconv.FormatInt(id, 10)), &run)

			return run, err
		},
		func(ctx context.Context, id int64) error {
			return discard(ctx, target, http.MethodPost, actionsURL(target, repo, "/actions/runs/"+strconv.FormatInt(id, 10)+"/cancel"), nil, http.StatusOK, http.StatusNoContent)
		},
		func(ctx context.Context, id int64) ([]RunJob, error) {
			var jobs []RunJob

			_, err := decodeQuiet(ctx, target, actionsURL(target, repo, "/actions/runs/"+strconv.FormatInt(id, 10)+"/jobs"), &jobs)

			return jobs, err
		})
}

func stopObservedWorkflowRun(ctx context.Context, observed WorkflowRun,
	read func(context.Context, int64) (WorkflowRun, error),
	cancel func(context.Context, int64) error,
	jobs func(context.Context, int64) ([]RunJob, error),
) error {
	if observed.ID == 0 {
		return nil
	}

	cancelled := false

	for {
		current, err := read(ctx, observed.ID)
		if err != nil {
			return err
		}

		if current.ID != observed.ID || current.WorkflowID != observed.WorkflowID || current.Event != observed.Event {
			return fmt.Errorf("observed workflow run identity changed: %w", errObservedRun)
		}

		entries, err := jobs(ctx, observed.ID)
		if err != nil {
			return err
		}

		settled, err := runSettled(current, entries)
		if err != nil {
			return err
		}

		if settled {
			return nil
		}

		if !cancelled {
			// A failed top-level run can still have an independent running job.
			if err := cancel(ctx, observed.ID); err != nil {
				return err
			}

			cancelled = true

			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(workflowPollInterval):
		}
	}
}

// runSettled reports whether the observed run and every one of its jobs has
// stopped doing work.
func runSettled(run WorkflowRun, jobs []RunJob) (bool, error) {
	settled := runIsOver(run.Status)

	for _, job := range jobs {
		if job.RunID != run.ID {
			return false, fmt.Errorf("observed workflow job belongs to another run: %w", errObservedRun)
		}

		switch job.Status {
		case "running", "waiting":
			settled = false
		case "blocked":
			// Unscheduled descendants of a failed run are not active work.
			if !runIsOver(run.Status) {
				settled = false
			}
		default:
			if !runIsOver(job.Status) {
				return false, fmt.Errorf("observed workflow job has an unknown status: %w", errObservedRun)
			}
		}
	}

	return settled, nil
}
