// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package ci

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
)

// These tests drive campaign.sh lane and demo-checkout.sh offline. A fake go
// plays the preflight and hands over a plan; a fake just records each case's
// argv and the environment it saw, prints a verdict path and exits as told.
// What a plan holds and refuses is tested in internal/livetest; these pin
// what the script does with one: run it in order, past failures, and record.

const fakeCampaignGo = `#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_GO_ARGV"
[[ -z "${FAKE_PLAN_REFUSAL:-}" ]] || { printf 'campaign: %s\n' "$FAKE_PLAN_REFUSAL" >&2; exit 2; }
cat -- "$FAKE_PLAN"
`

const fakeCampaignJust = `#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_JUST_ARGV"
printf 'provider=%s signer=%s lint=%s\n' "$FORGE_CI_LIVE_PROVIDER" "${FORGE_CI_LIVE_SIGNER_IMAGE:-}" "${FORGE_CI_LIVE_LINT_IMAGE_ARCHIVE-unset}" >>"$FAKE_JUST_ENV"
calls=$(wc -l <"$FAKE_JUST_ARGV")
printf 'suite output\nLive verdict: /evidence/%s/verdict.json\n' "${calls// /}"
case "$*" in
*TestFails*) exit 1 ;;
*TestInterrupted*) exit 130 ;;
esac
`

// campaignLane is one campaign directory, a plan for it and fakes to run it.
type campaignLane struct {
	dir, workDir, argvFile, goArgvFile, envFile string
	env                                         []string
}

func newCampaignLane(t *testing.T, extraEnv ...string) campaignLane {
	t.Helper()

	fakeDir, state := t.TempDir(), t.TempDir()
	writeExecutable(t, filepath.Join(fakeDir, "go"), fakeCampaignGo)
	writeExecutable(t, filepath.Join(fakeDir, "just"), fakeCampaignJust)

	lane := campaignLane{
		dir:        filepath.Join(state, "campaign"),
		workDir:    t.TempDir(),
		argvFile:   filepath.Join(state, "just-argv"),
		goArgvFile: filepath.Join(state, "go-argv"),
		envFile:    filepath.Join(state, "just-env"),
	}
	if err := os.MkdirAll(lane.dir, 0o700); err != nil {
		t.Fatal(err)
	}

	lane.env = append([]string{
		"PATH=" + fakeDir + string(os.PathListSeparator) +
			testenv.GNUPath(t, "bash", "sed", "tail", "tee", "date", "mktemp", "rm", "dirname", "wc", "cat") +
			string(os.PathListSeparator) + testenv.ToolPath(t, "git"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
		"LC_ALL=C",
		"FAKE_GO_ARGV=" + lane.goArgvFile,
		"FAKE_JUST_ARGV=" + lane.argvFile,
		"FAKE_JUST_ENV=" + lane.envFile,
		// The caller's: the plan, not the caller, names the provider.
		"FORGE_CI_LIVE_PROVIDER=all",
	}, extraEnv...)

	return lane
}

// run hands the lane the plan of NUL-terminated fields and runs it with the
// relative cases file lane.cases; it returns stderr and the exit status.
func (lane campaignLane) run(t *testing.T, provider string, plan ...string) (string, int) {
	t.Helper()

	planFile := filepath.Join(t.TempDir(), "plan")
	if err := os.WriteFile(planFile, []byte(strings.Join(plan, "\x00")+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := append(slices.Clone(lane.env), "FAKE_PLAN="+planFile)

	return runScript(t, lane.workDir, env, "campaign.sh", "lane", lane.dir, provider, "lane.cases")
}

func runScript(t *testing.T, dir string, env []string, script string, args ...string) (string, int) {
	t.Helper()

	path, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), liveCommandBound)
	defer cancel()

	command := exec.CommandContext(ctx, "bash", append([]string{path}, args...)...)
	command.Env, command.Dir = env, dir

	var errOut strings.Builder

	command.Stderr = &errOut
	err = command.Run()

	var exit *exec.ExitError

	switch {
	case err == nil:
		return errOut.String(), 0
	case errors.As(err, &exit):
		return errOut.String(), exit.ExitCode()
	default:
		t.Fatalf("run %s: %v", script, err)
	}

	return "", 0
}

func readLines(t *testing.T, path string) []string {
	t.Helper()

	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		t.Fatal(err)
	}

	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

func TestCampaignLane_RunsThePlanInOrderAndRecordsEachCase(t *testing.T) {
	t.Parallel()

	// The caller's lint archive is not a case's: the plan clears it.
	lane := newCampaignLane(t, "FORGE_CI_LIVE_LINT_IMAGE_ARCHIVE=/caller/lint.tar")

	stderr, status := lane.run(t, "forgejo", "unset", "FORGE_CI_LIVE_LINT_IMAGE_ARCHIVE",
		"env", "FORGE_CI_LIVE_PROVIDER=forgejo", "env", "FORGE_CI_LIVE_SIGNER_IMAGE=lab/signer@sha256:aa",
		"case", "consumer hello-go-cli release A=b c", "4", "test-live-forgejo-consumer", "/c/demos/hello-go-cli", "release", "A=b c",
		"case", "scenario TestFails", "2", "test-live", "^TestFails$",
		"case", "scenario TestThird", "2", "test-live", "^TestThird$")
	if status != 1 {
		t.Fatalf("status %d, want 1 for a failed case\n%s", status, stderr)
	}

	// The preflight is asked from the repository about the caller's cases file.
	if got := readLines(t, lane.goArgvFile); len(got) != 1 ||
		got[0] != "run ./internal/livetest/preflight campaign-plan "+lane.dir+" forgejo "+filepath.Join(lane.workDir, "lane.cases") {
		t.Fatalf("go calls %q", got)
	}

	justfile := "--justfile " + filepath.Join(reporoot.Path(t), "justfile")

	want := []string{
		justfile + " test-live-forgejo-consumer /c/demos/hello-go-cli release A=b c",
		justfile + " test-live ^TestFails$",
		justfile + " test-live ^TestThird$",
	}
	if got := readLines(t, lane.argvFile); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("just calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	for _, seen := range readLines(t, lane.envFile) {
		if seen != "provider=forgejo signer=lab/signer@sha256:aa lint=unset" {
			t.Errorf("a case saw %q", seen)
		}
	}

	results := readLines(t, filepath.Join(lane.dir, "forgejo.results"))
	if len(results) != 3 {
		t.Fatalf("results:\n%s", strings.Join(results, "\n"))
	}

	for i, wantCase := range []string{
		"consumer hello-go-cli release A=b c\texit=0\tverdict=/evidence/1/verdict.json",
		"scenario TestFails\texit=1\tverdict=/evidence/2/verdict.json",
		"scenario TestThird\texit=0\tverdict=/evidence/3/verdict.json",
	} {
		interval, rest, _ := strings.Cut(results[i], "\t")
		if rest != wantCase || !strings.Contains(interval, "Z/20") {
			t.Errorf("result %d: %q, want <start>/<end>\\t%q", i, results[i], wantCase)
		}
	}
}

func TestCampaignLane_StopsAtAnInterruptedCase(t *testing.T) {
	t.Parallel()

	lane := newCampaignLane(t)

	stderr, status := lane.run(t, "gitlab", "env", "FORGE_CI_LIVE_PROVIDER=gitlab",
		"case", "scenario TestInterrupted", "2", "test-live", "^TestInterrupted$",
		"case", "scenario TestNext", "2", "test-live", "^TestNext$")
	if status != 130 || !strings.Contains(stderr, "interrupted") {
		t.Fatalf("status %d, want 130 and the interruption named\n%s", status, stderr)
	}

	if calls := readLines(t, lane.argvFile); len(calls) != 1 {
		t.Fatalf("an interrupted lane went on: %q", calls)
	}

	if results := readLines(t, filepath.Join(lane.dir, "gitlab.results")); len(results) != 1 || !strings.Contains(results[0], "\texit=130\t") {
		t.Fatalf("results %q", results)
	}
}

func TestCampaignLane_RunsNothingThePreflightRefuses(t *testing.T) {
	t.Parallel()

	lane := newCampaignLane(t, "FAKE_PLAN_REFUSAL=the campaign was prepared for another commit")

	stderr, status := lane.run(t, "forgejo", "case", "scenario TestA", "2", "test-live", "^TestA$")
	if status != 2 || !strings.Contains(stderr, "another commit") {
		t.Fatalf("status %d, want the refusal (2)\n%s", status, stderr)
	}

	if calls := readLines(t, lane.argvFile); calls != nil {
		t.Fatalf("a refused lane ran %q", calls)
	}

	// Nor a plan the script cannot read as one: an export that is no variable.
	lane = newCampaignLane(t)
	if stderr, status := lane.run(t, "forgejo", "env", "-f x", "case", "scenario TestA", "2", "test-live", "^TestA$"); status != 2 || readLines(t, lane.argvFile) != nil {
		t.Fatalf("status %d, want a refusal (2) and no case run\n%s", status, stderr)
	}

	// Nor a clear that is no variable name.
	lane = newCampaignLane(t)
	if stderr, status := lane.run(t, "forgejo", "unset", "-f x", "case", "scenario TestA", "2", "test-live", "^TestA$"); status != 2 || readLines(t, lane.argvFile) != nil {
		t.Fatalf("status %d, want a refusal (2) and no case run\n%s", status, stderr)
	}
}

func TestCampaignPrepare_RefusesAnArgumentItCannotName(t *testing.T) {
	t.Parallel()

	forgeLab := t.TempDir()
	if err := os.WriteFile(filepath.Join(forgeLab, "justfile"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	lane := newCampaignLane(t)
	for _, images := range [][]string{
		{"job-image=Go:localhost/nanolinter-go:dev"},
		{"job-image=go/x:localhost/nanolinter-go:dev"},
		{"job-image=go:"},
		{"job-image=go:--rm"},
		{"job-image=go:localhost/a b"},
		{"image=go:localhost/a"},
		{"job-image=go:localhost/a", "job-image=go:localhost/b"},
		{"github=lane.cases"},
		{"gitlab="},
	} {
		stderr, status := runScript(t, lane.workDir, lane.env, "campaign.sh", append([]string{"prepare", forgeLab, filepath.Join(t.TempDir(), "c"), "gitlab=lane.cases"}, images...)...)
		if status != 2 || !strings.Contains(stderr, "campaign: ") {
			t.Errorf("%q: status %d, want a refusal (2)\n%s", images, status, stderr)
		}
	}

	// A campaign prepares for the lanes it names, so it names one.
	if stderr, status := runScript(t, lane.workDir, lane.env, "campaign.sh", "prepare", forgeLab, filepath.Join(t.TempDir(), "c"), "job-image=go:localhost/a"); status != 2 ||
		!strings.Contains(stderr, "campaign: name the lanes") {
		t.Errorf("status %d, want the lanes asked for (2)\n%s", status, stderr)
	}

	if calls := readLines(t, lane.argvFile); calls != nil {
		t.Fatalf("a refused prepare ran %q", calls)
	}
}

func TestDemoCheckout_IsOneCommitOfTheDemo(t *testing.T) {
	t.Parallel()

	env := []string{
		"PATH=" + testenv.GNUPath(t, "bash", "ls", "cp", "mkdir", "dirname") + string(os.PathListSeparator) + testenv.ToolPath(t, "git"),
		"HOME=" + t.TempDir(),
		"LC_ALL=C",
	}
	checkout := filepath.Join(t.TempDir(), "hello-go-cli")

	if stderr, status := runScript(t, "", env, "demo-checkout.sh", "hello-go-cli", checkout); status != 0 {
		t.Fatalf("status %d\n%s", status, stderr)
	}

	git := func(args ...string) string {
		out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", checkout}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}

		return strings.TrimSpace(string(out))
	}
	if count, dirty := git("rev-list", "--count", "HEAD"), git("status", "--porcelain", "--untracked-files=all"); count != "1" || dirty != "" {
		t.Fatalf("commits %s, status %q; want one commit and a clean tree", count, dirty)
	}

	if _, err := os.Stat(filepath.Join(checkout, "go.mod")); err != nil {
		t.Fatalf("the demo's files are not checked out: %v", err)
	}

	for _, args := range [][]string{{"hello-go-cli", checkout}, {"../demos", t.TempDir()}, {"hello-nothing", t.TempDir()}} {
		if stderr, status := runScript(t, "", env, "demo-checkout.sh", args...); status != 2 {
			t.Errorf("%q: status %d, want a refusal (2)\n%s", args, status, stderr)
		}
	}
}
