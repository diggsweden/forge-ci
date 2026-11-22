// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// demoRecipeRecorder stands in for the live launcher and records what the
// `just demo` recipe hands it.
const demoRecipeRecorder = `#!/usr/bin/env bash
printf 'args=%s\n' "$*" >"$RECEIPT"
env | grep -E '^FORGE_CI_LIVE_(DEMO|CONSUMER|CONSUMER_EVENT|CONSUMER_VISIBILITY|CONSUMER_IMAGE_ARCHIVE|JOB_IMAGE_ARCHIVE|JOB_IMAGE_ARCHIVE_NODE|BUILD_IMAGE_ARCHIVE|LINT_IMAGE_ARCHIVE)=' >>"$RECEIPT"
`

// runDemoRecipe runs `just demo gitlab myapp` from a copy of the justfile, with
// the recorder as the launcher, and returns the receipt, or the output of a
// refusal.
func runDemoRecipe(t *testing.T, just string, shell []string, words ...string) (string, error) {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts/ci"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "justfile"), reporoot.ReadFile(t, "justfile"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "scripts/ci/run-live-tests.sh"), []byte(demoRecipeRecorder), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	receipt := filepath.Join(root, "receipt")
	args := append([]string{"--justfile", filepath.Join(root, "justfile"), "demo", "gitlab", "myapp", "checkout=/abs/myapp"}, words...)
	command := exec.CommandContext(ctx, just, args...)

	command.Env = append(append(os.Environ(), "RECEIPT="+receipt), shell...)

	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), err
	}

	body, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatalf("the recipe did not reach the launcher: %v\n%s", err, output)
	}

	return string(body), nil
}

// A GitLab demo's event picks its scenario, and a merge request takes its
// archives from this command line alone, whatever the shell left set.
func TestDemoRecipe_GitLabMergeRequest(t *testing.T) {
	just, err := exec.LookPath("just")
	if err != nil {
		t.Skip("requires Just")
	}

	t.Run("merge request", func(t *testing.T) {
		got, err := runDemoRecipe(t, just, []string{"FORGE_CI_LIVE_BUILD_IMAGE_ARCHIVE=/stale/build.tar"},
			"event=pull-request", "job-archive=/abs/job.tar", "lint-archive=/abs/lint.tar", "job-archive-node=/abs/node.tar")
		if err != nil {
			t.Fatalf("%v\n%s", err, got)
		}

		for _, want := range []string{
			"args=focused ^TestGitLabLayer_ConsumerTrial$\n", "FORGE_CI_LIVE_DEMO=myapp\n", "FORGE_CI_LIVE_CONSUMER=/abs/myapp\n",
			"FORGE_CI_LIVE_CONSUMER_EVENT=pull-request\n", "FORGE_CI_LIVE_CONSUMER_VISIBILITY=public\n",
			"FORGE_CI_LIVE_CONSUMER_IMAGE_ARCHIVE=/abs/job.tar\n", "FORGE_CI_LIVE_LINT_IMAGE_ARCHIVE=/abs/lint.tar\n",
			"FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE_NODE=/abs/node.tar\n",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("receipt lacks %q:\n%s", want, got)
			}
		}

		for _, stale := range []string{"FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE=", "FORGE_CI_LIVE_BUILD_IMAGE_ARCHIVE="} {
			if strings.Contains(got, stale) {
				t.Errorf("receipt keeps %q:\n%s", stale, got)
			}
		}
	})

	t.Run("release keeps its scenario", func(t *testing.T) {
		got, err := runDemoRecipe(t, just, nil, "job-archive=/abs/job.tar")
		if err != nil {
			t.Fatalf("%v\n%s", err, got)
		}

		for _, want := range []string{"args=focused ^TestGitLabRelease_ConsumerTrial$\n", "FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE=/abs/job.tar\n"} {
			if !strings.Contains(got, want) {
				t.Errorf("receipt lacks %q:\n%s", want, got)
			}
		}
	})

	for name, refused := range map[string]struct{ shell, words []string }{
		"no job archive":               {words: []string{"lint-archive=/abs/lint.tar"}},
		"a job archive the shell left": {shell: []string{"FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE=/stale/job.tar"}},
		"a release archive":            {words: []string{"job-archive=/abs/job.tar", "signer-archive=/abs/signer.tar"}},
		"a release setting":            {words: []string{"job-archive=/abs/job.tar", "version=0.2.0"}},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			got, err := runDemoRecipe(t, just, refused.shell, append([]string{"event=pull-request"}, refused.words...)...)
			if err == nil || !strings.Contains(got, "a GitLab merge request demo takes") {
				t.Fatalf("not refused: %v\n%s", err, got)
			}
		})
	}
}

// Each account is out of the other's reach: a demo never acts as the trial
// account, whose repositories trials reset, and a trial never acts as the demo
// account, whose repositories are kept.
func TestRequireAccountMode_KeepsTrialsAndDemosApart(t *testing.T) {
	for name, test := range map[string]struct {
		owner, demo string
		refused     bool
	}{
		"trial as the trial account": {owner: labAccount},
		"demo as the demo account":   {owner: demoAccount, demo: "hello-maven-service"},
		"trial as the demo account":  {owner: demoAccount, refused: true},
		"demo as the trial account":  {owner: labAccount, demo: "hello-maven-service", refused: true},
		"demo as another account":    {owner: "releaseci", demo: "hello-maven-service", refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := requireAccountMode(test.owner, test.demo)
			if test.refused != (err != nil) || (err != nil && !errors.Is(err, errs.ErrValidation)) {
				t.Fatalf("requireAccountMode(%q, %q) = %v, want refused %v", test.owner, test.demo, err, test.refused)
			}
		})
	}
}

// The demo name selects the account whose contract is read, and a name that
// could not be a demo repository's is refused before any contract is.
func TestLiveAccount_FollowsDemoMode(t *testing.T) {
	for name, test := range map[string]struct {
		demo, want string
		refused    bool
	}{
		"no demo":             {want: labAccount},
		"demo":                {demo: "hello-maven-service", want: demoAccount},
		"uppercase":           {demo: "App", refused: true},
		"path":                {demo: "a/b", refused: true},
		"trailing hyphen":     {demo: "app-", refused: true},
		"one character":       {demo: "w", refused: true},
		"a layer's name":      {demo: "app-forge-ci", refused: true},
		"a helper's name":     {demo: "app-release-engine", refused: true},
		"over the length cap": {demo: "a1234567890123456789012345678901234567890", refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(demoEnv, test.demo)

			got, err := liveAccount()
			if test.refused {
				if !errors.Is(err, errs.ErrValidation) {
					t.Fatalf("liveAccount() = %q, %v, want a refusal", got, err)
				}

				return
			}

			if err != nil || got != test.want {
				t.Fatalf("liveAccount() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestDemoLayerRepo_NamesTheDemosOwnLayer(t *testing.T) {
	if got := DemoLayerRepo("hello-maven-service"); got != "demo-hello-maven-service-forge-ci" {
		t.Fatalf("DemoLayerRepo() = %q", got)
	}

	if got := DemoHelperRepo("hello-maven-service", "engine"); got != "demo-hello-maven-service-release-engine" {
		t.Fatalf("DemoHelperRepo() = %q", got)
	}
}

// Nothing deletes or resets in the demo account, whichever helper a scenario
// reaches a deletion through.
func TestRefuseDemoDeletion_HoldsForTheDemoAccountAlone(t *testing.T) {
	if err := refuseDemoDeletion(Target{Owner: demoAccount}); !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("refuseDemoDeletion(demo) = %v, want a refusal", err)
	}

	if err := refuseDemoDeletion(Target{Owner: labAccount}); err != nil {
		t.Fatalf("refuseDemoDeletion(%s) = %v", labAccount, err)
	}

	if _, err := scratchRepos(Target{Owner: demoAccount, accepted: true}); !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("scratchRepos(demo) = %v, want the scratch lifecycle refused", err)
	}
}

// A run writes only in its own namespace: fci- for a trial, demo- in the demo
// account, and never the bare prefix or the other account's names.
func TestInRunNamespace_FollowsTheAccount(t *testing.T) {
	trial, demo := Target{Owner: labAccount}, Target{Owner: demoAccount}

	for name, test := range map[string]struct {
		target Target
		repo   string
		inside bool
	}{
		"trial repository":       {target: trial, repo: "fci-consumer-trial", inside: true},
		"demo repository":        {target: demo, repo: "demo-hello-release-job-image", inside: true},
		"demo name in a trial":   {target: trial, repo: "demo-hello", inside: false},
		"trial name in the demo": {target: demo, repo: "fci-consumer-trial", inside: false},
		"bare demo prefix":       {target: demo, repo: "demo-", inside: false},
	} {
		if got := inRunNamespace(test.target, test.repo); got != test.inside {
			t.Errorf("%s: inRunNamespace(%s, %q) = %v", name, test.target.Owner, test.repo, got)
		}
	}
}
