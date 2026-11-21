// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// fakeNanolinter records its argv, one argument per line, and exits with
// FAKE_NANOLINTER_STATUS.
const fakeNanolinter = `#!/usr/bin/env bash
test "${LANG:-}" = C.UTF-8 && test "${LC_ALL:-}" = C.UTF-8 || exit 97
printf '%s\n' "$@" >"${FAKE_NANOLINTER_LOG:?}"
if [ "${FAKE_EXPECT_SUMMARY:-}" = absent ]; then
  # No summary the run shows; the matrix goes to a file of the step alone.
  test -z "${FORGEJO_STEP_SUMMARY:-}${GITEA_STEP_SUMMARY:-}${GITHUB_STEP_SUMMARY:-}" || exit 98
  case "${CI_JOB_SUMMARY:-}" in */results/nanolinter-matrix.md) ;; *) exit 98 ;; esac
elif [ "${FAKE_EXPECT_SUMMARY:-}" = present ]; then
  test "${FORGEJO_STEP_SUMMARY:-}" = /example-summary || exit 98
fi
if [ -n "${FAKE_MATRIX:-}" ]; then printf '%s\n' "$FAKE_MATRIX" >"${CI_JOB_SUMMARY:?}"; fi
results='[]'
if [ "${FAKE_NANOLINTER_STATUS:-0}" = 1 ]; then
  results='[{"ruleId":"format","properties":{"source":"curated","check_id":"format","class":"formatter","status":"fail"}}]'
fi
printf '{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":%s}]}\n' "$results"
exit "${FAKE_NANOLINTER_STATUS:-0}"
`

// The lint step runs the job's own nanolinter with the mandated floor, the
// caller's exclusions and the offline switch, and its status is nanolinter's.
// It installs no nanolinter: without one on PATH it stops, and says what the
// job is missing. The pinned, checksum-verified engine applies the immutable
// policy after execution.
func TestLintNanolinter(t *testing.T) {
	t.Parallel()
	action := loadAction(t, "lint-nanolinter")
	action.requireSteps(t, 2)

	// Forgejo Runner reads the summary file after the lint step and again
	// after the action: the last step must leave it empty, on failure too.
	if closing := action.Runs.Steps[1]; closing.Run != ":" || closing.If != "${{ always() }}" {
		t.Fatalf("closing step = %+v, want a no-op that always runs", closing)
	}

	action.requireStepEnv(t, 0, map[string]string{
		"LINT_REQUIRED_LINTS":        "${{ inputs.required-lints }}",
		"LINT_DISABLE_CHECKS":        "${{ inputs.disable-checks }}",
		"LINT_OFFLINE":               "${{ inputs.offline }}",
		"LINT_WRITE_STEP_SUMMARY":    "${{ inputs.write-step-summary }}",
		"LINT_ADMIN_LINT":            "${{ inputs.enforce-lint }}",
		"LINT_ADMIN_VULNERABILITIES": "${{ inputs.enforce-vulnerabilities }}",
	})

	if floor := action.Inputs["required-lints"].Default; floor != "secrets" {
		t.Fatalf("default lint floor = %v, want secrets as on the other forges", floor)
	}

	if action.Inputs["write-step-summary"].Default != "false" {
		t.Fatal("lint details should stay in the job log by default")
	}

	script := action.Runs.Steps[0].Run
	run := func(t *testing.T, env ...string) (result, []string) {
		t.Helper()

		box := newSandbox(t, "lint-nanolinter")
		box.write("tools/nanolinter", fakeNanolinter, 0o700)
		box.write(".engine/bin/lib-forge-ci.sh", string(reporoot.ReadFile(t, ".engine/bin/lib-forge-ci.sh")), 0o600)

		for _, arch := range []string{"amd64", "arm64"} {
			file := ".engine/bin/forge-ci-linux-" + arch + ".sha256"
			box.write(file, string(reporoot.ReadFile(t, file)), 0o600)
		}

		log := filepath.Join(box.root, "nanolinter-argv")

		got := box.run(script, append([]string{
			"PATH=" + filepath.Join(box.root, "tools") + ":" + os.Getenv("PATH"), "FAKE_NANOLINTER_LOG=" + log,
			"FORGE_CI_BIN=" + forgeCIEngine(t), "LINT_ADMIN_LINT=true", "LINT_ADMIN_VULNERABILITIES=true",
		}, env...)...)
		if box.argv() != nil {
			t.Fatalf("the lint step ran the layer's binary: %q", box.argv())
		}

		data, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return got, nil
		}

		if err != nil {
			t.Fatal(err)
		}

		return got, strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}

	for _, test := range []struct {
		name string
		env  []string
		argv []string
	}{
		{
			"the default floor",
			[]string{"LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false"},
			[]string{"verify", "--require", "secrets", "--report", "sarif", "--out", "-"},
		},
		{
			"exclusions and offline",
			[]string{"LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=osv,trivy-fs", "LINT_OFFLINE=true"},
			[]string{"verify", "--require", "secrets", "--disable-check", "osv,trivy-fs", "--offline", "--report", "sarif", "--out", "-"},
		},
		// A pull request is judged against its target branch; a push reports
		// everything, whatever base variable its environment carries. Both
		// carry the markers a Forgejo runner sets.
		{
			"a pull request",
			[]string{"LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false", "GITHUB_ACTIONS=true", "FORGEJO_ACTIONS=true", "FORGEJO_EVENT_NAME=pull_request", "FORGEJO_BASE_REF=main"},
			[]string{"verify", "--require", "secrets", "--report", "sarif", "--out", "-", "--baseline-ref", "origin/main"},
		},
		{
			"a push",
			[]string{"LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false", "GITHUB_ACTIONS=true", "FORGEJO_ACTIONS=true", "FORGEJO_EVENT_NAME=push", "FORGEJO_BASE_REF=main", "FORGEJO_REF_TYPE=branch"},
			[]string{"verify", "--require", "secrets", "--report", "sarif", "--out", "-"},
		},
		{
			"no floor",
			[]string{"LINT_REQUIRED_LINTS=", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false"},
			[]string{"verify", "--report", "sarif", "--out", "-"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, argv := run(t, test.env...)
			if got.status != 0 || !slices.Equal(argv, test.argv) {
				t.Fatalf("status %d argv %q, want %q; stderr %s", got.status, argv, test.argv, got.stderr)
			}
		})
	}

	for _, tc := range []struct{ input, expect string }{{"false", "absent"}, {"true", "present"}} {
		t.Run("step summary "+tc.input, func(t *testing.T) {
			t.Parallel()

			got, _ := run(t, "LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false",
				"FORGEJO_STEP_SUMMARY=/example-summary", "GITEA_STEP_SUMMARY=/example-summary",
				"GITHUB_STEP_SUMMARY=/example-summary", "CI_JOB_SUMMARY=/example-summary",
				"LINT_WRITE_STEP_SUMMARY="+tc.input, "FAKE_EXPECT_SUMMARY="+tc.expect)
			if got.status != 0 {
				t.Fatalf("status %d, stderr %s", got.status, got.stderr)
			}
		})
	}

	// The matrix is handed to the PR check results job as a run artifact.
	// Outside a run that upload fails: a warning, and the lint result stands.
	t.Run("a matrix that cannot be handed on is a warning", func(t *testing.T) {
		t.Parallel()

		got, argv := run(t, "LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false",
			"GITHUB_ACTIONS=true", "FORGEJO_ACTIONS=true", "FORGEJO_EVENT_NAME=pull_request", "FORGEJO_BASE_REF=main",
			"FAKE_MATRIX=| Check | Result |")
		if got.status != 0 || argv == nil || !strings.Contains(got.stdout, "::warning::The lint results could not be handed") {
			t.Fatalf("status %d, stdout %s, stderr %s", got.status, got.stdout, got.stderr)
		}
	})

	// A pull request without a matrix still replaces an earlier attempt's:
	// the upload is tried, and here, outside a run, ends as the warning.
	t.Run("a pull request without a matrix says so", func(t *testing.T) {
		t.Parallel()

		got, argv := run(t, "LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false",
			"GITHUB_ACTIONS=true", "FORGEJO_ACTIONS=true", "FORGEJO_EVENT_NAME=pull_request", "FORGEJO_BASE_REF=main")
		if got.status != 0 || argv == nil || !strings.Contains(got.stdout, "::warning::The lint results could not be handed") {
			t.Fatalf("status %d, stdout %s, stderr %s", got.status, got.stdout, got.stderr)
		}
	})

	t.Run("a blocking finding fails the policy gate", func(t *testing.T) {
		t.Parallel()

		got, argv := run(t, "LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false", "FAKE_NANOLINTER_STATUS=1")
		if got.status != 1 || argv == nil || !strings.Contains(got.stdout, "result=findings") {
			t.Fatalf("status %d argv %q", got.status, argv)
		}
	})

	// The administrator's setting reaches the engine: the same finding is
	// reported, does not block, and the receipt names the admin input.
	t.Run("the administrator's setting reaches the policy gate", func(t *testing.T) {
		t.Parallel()

		got, argv := run(t, "LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false", "FAKE_NANOLINTER_STATUS=1", "LINT_ADMIN_LINT=false")
		if got.status != 0 || argv == nil || !strings.Contains(got.stdout, "result=findings count=1 enforcement=false source=flag") {
			t.Fatalf("status %d argv %q stdout %s stderr %s", got.status, argv, got.stdout, got.stderr)
		}

		got, _ = run(t, "LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false", "LINT_ADMIN_LINT=yes")
		if got.status != 2 || !strings.Contains(got.stderr, "enforce-lint must be exactly true or false") {
			t.Fatalf("a non-boolean setting: status %d stderr %s", got.status, got.stderr)
		}
	})

	t.Run("an offline value that is not a boolean is refused before nanolinter", func(t *testing.T) {
		t.Parallel()

		got, argv := run(t, "LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=yes")
		if got.status == 0 || argv != nil || !strings.Contains(got.stderr, "offline must be true or false") {
			t.Fatalf("status %d argv %q stderr %q", got.status, argv, got.stderr)
		}
	})

	t.Run("a job without nanolinter is refused, not skipped", func(t *testing.T) {
		t.Parallel()

		box := newSandbox(t, "lint-nanolinter")

		// The step uses shell builtins only until it calls nanolinter.
		got := box.run(script, "PATH="+filepath.Join(box.root, "empty"),
			"LINT_REQUIRED_LINTS=secrets", "LINT_DISABLE_CHECKS=", "LINT_OFFLINE=false")
		if got.status != 78 || !strings.Contains(got.stderr, "nanolinter is not on PATH") {
			t.Fatalf("status %d stderr %q", got.status, got.stderr)
		}
	})
}
