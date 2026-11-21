// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// The results action writes the overview of the jobs its caller names to the
// run's summary and posts the same text as the pull request's one comment.
// Reporting is never a gate: a comment that cannot be posted is a warning.
func TestPRCheckResults(t *testing.T) {
	t.Parallel()

	action := loadAction(t, "pr-check-results")
	action.requireSteps(t, 2)

	// The runner reads the summary after the step and again after the
	// action: the last step leaves it empty, as in lint-nanolinter.
	if closing := action.Runs.Steps[1]; closing.Run != ":" || closing.If != "${{ always() }}" {
		t.Fatalf("closing step = %+v, want a no-op that always runs", closing)
	}

	if action.Inputs["comment"].Default != "true" {
		t.Fatal("the results are posted as a comment unless the caller says otherwise")
	}

	script := action.Runs.Steps[0].Run
	run := func(t *testing.T, env ...string) (result, string) {
		t.Helper()

		box := newSandbox(t, "pr-check-results")
		box.write(".engine/bin/lib-forge-ci.sh", string(reporoot.ReadFile(t, ".engine/bin/lib-forge-ci.sh")), 0o600)

		for _, arch := range []string{"amd64", "arm64"} {
			file := ".engine/bin/forge-ci-linux-" + arch + ".sha256"
			box.write(file, string(reporoot.ReadFile(t, file)), 0o600)
		}

		summary := filepath.Join(box.root, "summary.md")

		got := box.run(script, append([]string{
			"PATH=" + os.Getenv("PATH"), "FORGE_CI_BIN=" + forgeCIEngine(t),
			"GITHUB_ACTIONS=true", "FORGEJO_ACTIONS=true", "FORGEJO_EVENT_NAME=pull_request",
			"FORGEJO_STEP_SUMMARY=" + summary, "FORGEJO_REPOSITORY=example/app",
			// A closed loopback port: nothing here reaches a forge.
			"FORGEJO_SERVER_URL=https://127.0.0.1:1", "GITHUB_SERVER_URL=https://127.0.0.1:1",
			"RESULT_COMMENT_TOKEN=inert-token", "PR_COMPACT=true", "PR_TARGET_BRANCH=main",
			"PROJECT_TYPE=", "PR_NUMBER=", "PR_URL=", "PR_AUTHOR=", "PR_SOURCE_REPOSITORY=", "PR_TARGET_REPOSITORY=",
			"PR_SOURCE_COMMIT=", "CI_BRANCH=topic", "CI_RUN_URL=https://127.0.0.1:1/example/app/actions/runs/1",
		}, env...)...)

		written, err := os.ReadFile(summary)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}

		return got, string(written)
	}

	const jobs = "RESULT_JOBS=\n  Quality checks=success\n  Build and test=failure\n"

	t.Run("the summary names every job", func(t *testing.T) {
		t.Parallel()

		got, summary := run(t, jobs, "RESULT_COMMENT=false")
		if got.status != 0 || !strings.Contains(summary, "Quality checks") || !strings.Contains(summary, "Build and test") {
			t.Fatalf("status %d, summary %q, stderr %s", got.status, summary, got.stderr)
		}

		if strings.Contains(got.stdout+got.stderr, "inert-token") {
			t.Fatal("the comment token was printed")
		}
	})

	// An older Forgejo sets no summary file: the comment still has a text.
	t.Run("a runner without a step summary still has a comment to post", func(t *testing.T) {
		t.Parallel()

		got, _ := run(t, jobs, "RESULT_COMMENT=true", "PR_NUMBER=7", "FORGEJO_STEP_SUMMARY=")
		if got.status != 0 || !strings.Contains(got.stdout, "::warning::PR check results were not posted as a comment; they are in") {
			t.Fatalf("status %d, stdout %s, stderr %s", got.status, got.stdout, got.stderr)
		}
	})

	t.Run("outside a pull request nothing is posted", func(t *testing.T) {
		t.Parallel()

		got, summary := run(t, jobs, "RESULT_COMMENT=true")
		if got.status != 0 || summary == "" || strings.Contains(got.stdout, "::warning::") {
			t.Fatalf("status %d, stdout %s, stderr %s", got.status, got.stdout, got.stderr)
		}
	})

	t.Run("a comment that cannot be posted is a warning", func(t *testing.T) {
		t.Parallel()

		got, summary := run(t, jobs, "RESULT_COMMENT=true", "PR_NUMBER=7")
		if got.status != 0 || summary == "" || !strings.Contains(got.stdout, "::warning::PR check results were not posted as a comment") {
			t.Fatalf("status %d, stdout %s, stderr %s", got.status, got.stdout, got.stderr)
		}
	})

	for name, env := range map[string][]string{
		"a job without a result": {"RESULT_JOBS=Quality checks", "RESULT_COMMENT=true"},
		"no job":                 {"RESULT_JOBS=\n \n", "RESULT_COMMENT=true"},
		"an unknown comment":     {jobs, "RESULT_COMMENT=yes"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, summary := run(t, env...)
			if got.status != 2 || summary != "" {
				t.Fatalf("status %d, summary %q, stderr %s", got.status, summary, got.stderr)
			}
		})
	}
}
