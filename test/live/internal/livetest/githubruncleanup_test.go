// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build live

package livetest

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-github/v91/github"
)

// settleOwnedRuns cancels the runs this test started on the head commit that
// are still active at cleanup, and waits for them to reach a terminal state,
// so the scratch repository can be deleted without orphaning a run.
func settleOwnedRuns(t *testing.T, trial *GitHubTrial, repo, head, workflow, event string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 2*time.Minute)
	defer cancel()

	requested := make(map[int64]bool)

	for {
		runs, _, err := trial.api.Actions.ListWorkflowRunsByFileName(ctx, trial.org, repo, workflow, &github.ListWorkflowRunsOptions{HeadSHA: head, Event: event})
		if err != nil {
			t.Errorf("run cleanup cannot observe owned runs: %v", err)

			return
		}

		active := false

		for _, run := range runs.WorkflowRuns {
			if run.GetStatus() == "completed" {
				continue
			}

			active = true

			if !requested[run.GetID()] {
				if _, err := trial.api.Actions.CancelWorkflowRunByID(ctx, trial.org, repo, run.GetID()); err != nil {
					t.Errorf("cancel owned run %d: %v", run.GetID(), err)

					return
				}

				requested[run.GetID()] = true
			}
		}

		if !active {
			t.Log("run cleanup: all observed owned runs terminal")

			return
		}

		select {
		case <-ctx.Done():
			t.Error("run cleanup timed out waiting for owned runs")

			return
		case <-time.After(2 * time.Second):
		}
	}
}
