// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// Keep the ordinary contributor commands visible in each provider's job. There
// is no separate self-build recipe, publisher or privileged release dependency.
func TestSelfBuildJobsUseTheSameExistingCommands(t *testing.T) {
	t.Parallel()

	want := []string{
		"just build", "just test-unit -timeout 15m", "just test-smoke",
		"just test-fuzz", "go vet ./...", "./bin/forge-ci --version",
	}

	for _, file := range []string{".github/workflows/self-pullrequest.yml", ".forgejo/workflows/self-pullrequest.yml"} {
		var workflow struct {
			Jobs map[string]struct {
				Steps     []step `yaml:"steps"`
				Container struct {
					Options string `yaml:"options"`
				} `yaml:"container"`
			} `yaml:"jobs"`
		}
		loadYAML(t, file, &workflow)

		if strings.HasPrefix(file, ".forgejo/") && workflow.Jobs["go"].Container.Options != "--user 65532:65532" {
			t.Fatal("the native Go gate must run contributor code without root privileges")
		}

		var got []string

		for _, s := range workflow.Jobs["go"].Steps {
			if slices.Contains(want, s.Run) {
				if s.If != "" {
					t.Fatalf("%s conditionally skips %q", file, s.Run)
				}

				got = append(got, s.Run)
			}
		}

		if !slices.Equal(got, want) {
			t.Fatalf("%s commands = %q, want %q", file, got, want)
		}
	}

	var pipeline struct {
		SelfBuild struct {
			Script    []string          `yaml:"script"`
			Variables map[string]string `yaml:"variables"`
			Image     struct {
				Name   string `yaml:"name"`
				Docker struct {
					User string `yaml:"user"`
				} `yaml:"docker"`
				Kubernetes struct {
					User string `yaml:"user"`
				} `yaml:"kubernetes"`
			} `yaml:"image"`
			AllowFailure bool `yaml:"allow_failure"`
		} `yaml:"self-build"`
		Release struct {
			Rules []struct {
				If string `yaml:"if"`
			} `yaml:"rules"`
		} `yaml:"create-release"`
	}
	loadYAML(t, ".gitlab-ci.yml", &pipeline)

	commands := pipeline.SelfBuild.Script
	if _, overridesHelperHome := pipeline.SelfBuild.Variables["HOME"]; overridesHelperHome || len(commands) == 0 || commands[0] != "export HOME=/home/nonroot" {
		t.Fatal("GitLab must select the build user's home in its script, not in the source-checkout helper's environment")
	}

	if len(commands) < len(want) || !slices.Equal(commands[len(commands)-len(want):], want) || pipeline.SelfBuild.AllowFailure {
		t.Fatalf("GitLab must run the same blocking commands: %q", commands)
	}

	if pipeline.SelfBuild.Image.Name != "$FORGE_CI_SELF_BUILD_IMAGE" || pipeline.SelfBuild.Image.Docker.User != "65532:65532" || pipeline.SelfBuild.Image.Kubernetes.User != "65532:65532" || len(pipeline.Release.Rules) != 1 || pipeline.Release.Rules[0].If != `$CI_COMMIT_TAG =~ /^\d+\.\d+\.\d+$/` {
		t.Fatal("GitLab must select its configured runtime and preserve catalog tag admission")
	}

	if strings.Contains(string(reporoot.ReadFile(t, "justfile")), "\nself-build:") {
		t.Fatal("reuse the existing recipes, not a self-build wrapper")
	}
}

func TestForgejoSelfBuildBootstrapsWithoutPublishedEngine(t *testing.T) {
	t.Parallel()

	body := string(reporoot.ReadFile(t, ".forgejo/workflows/self-pullrequest.yml"))
	if strings.Contains(body, "run: forge-ci ") || strings.Contains(body, "secrets.") {
		t.Fatal("source bootstrap must not require a published engine or release secrets")
	}

	for _, required := range []string{"persist-credentials: false", "source scripts/bootstrap/install-mise.sh", "mise install go ", "$(mktemp -d)", "PATH=/usr/bin:/bin command -v jq"} {
		if !strings.Contains(body, required) {
			t.Fatalf("source bootstrap is missing %q", required)
		}
	}
}

// The suite coordinates are administrator variables, but they reach git. A
// value that is not an owner/name on this forge, or not a full commit, stops
// the job before anything is fetched.
func TestForgejoBlackboxFetchRejectsUnreviewedCoordinates(t *testing.T) {
	t.Parallel()

	fetch := workflowStep(t, "self-pullrequest.yml", "blackbox", "Fetch the pinned suite")
	if !maps.Equal(fetch.Env, map[string]string{
		"SERVER_URL":       "${{ forgejo.server_url }}",
		"SUITE_REPOSITORY": "${{ vars.FORGE_CI_TESTSUITE_REPOSITORY }}",
		"SUITE_SHA":        "${{ vars.FORGE_CI_TESTSUITE_SHA }}",
	}) || strings.Contains(fetch.Run, "${{") {
		t.Fatalf("the fetch script must read its coordinates from its environment only: %q", fetch.Env)
	}

	sha := strings.Repeat("a", 40)
	for name, testCase := range map[string]struct {
		repository, sha string
		fetched         bool
	}{
		"reviewed coordinates": {"acme/forge-ci-blackbox-tests", sha, true},
		"another host":         {"https://other.example/acme/suite", sha, false},
		"transport helper":     {"ext::sh -c id", sha, false},
		"parent directory":     {"../suite", sha, false},
		"nested path":          {"acme/team/suite", sha, false},
		"no repository":        {"", sha, false},
		"branch name":          {"acme/suite", "main", false},
		"short commit":         {"acme/suite", sha[:12], false},
		"no commit":            {"acme/suite", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			record := filepath.Join(dir, "git.log")
			if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$GIT_RECORD\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}

			cmd := exec.CommandContext(t.Context(), "bash", "-c", fetch.Run)
			cmd.Env = []string{
				"PATH=" + dir + ":/usr/bin:/bin", "RUNNER_TEMP=" + dir, "GIT_RECORD=" + record,
				"SERVER_URL=https://forge.example/", "SUITE_REPOSITORY=" + testCase.repository, "SUITE_SHA=" + testCase.sha,
			}
			output, err := cmd.CombinedOutput()
			calls, _ := os.ReadFile(record)

			if !testCase.fetched {
				if err == nil || len(calls) != 0 {
					t.Fatalf("accepted %q at %q: err=%v git=%q output=%q", testCase.repository, testCase.sha, err, calls, output)
				}

				return
			}

			if err != nil || !strings.Contains(string(calls), "fetch --quiet --depth=1 https://forge.example/"+testCase.repository+".git "+testCase.sha) {
				t.Fatalf("reviewed coordinates were not fetched from this forge: err=%v git=%q output=%q", err, calls, output)
			}
		})
	}
}

func TestSelfBuildDispatchDoesNotLaunchTheFullGitHubGate(t *testing.T) {
	t.Parallel()

	var workflow struct {
		On   map[string]any `yaml:"on"`
		Jobs map[string]struct {
			If    string `yaml:"if"`
			Needs any    `yaml:"needs"`
		} `yaml:"jobs"`
	}
	loadYAML(t, ".github/workflows/self-pullrequest.yml", &workflow)

	if _, ok := workflow.On["workflow_dispatch"]; !ok {
		t.Fatal("native Go gate needs a bounded manual dispatch entry point")
	}

	for name, job := range workflow.Jobs {
		if name == "go" {
			if job.If != "" || job.Needs != nil {
				t.Fatal("native Go dispatch must not depend on the broader PR gate")
			}
		} else if job.If != "github.event_name == 'pull_request'" {
			t.Fatalf("dispatch could launch the full %s job: %q", name, job.If)
		}
	}
}

func TestSelfBuildTrialUsesTheExistingRepositoryReadinessBarriers(t *testing.T) {
	t.Parallel()

	body := string(reporoot.ReadFile(t, "test/live/internal/livetest/selfbuild_live_test.go"))
	if !strings.Contains(body, `trial.waitWorkflows(repo, filepath.Join(root, ".github", "workflows"), file)`) {
		t.Fatal("self-build indexing must wait for its dispatched workflow, not the unrelated reusable catalog")
	}

	for _, calls := range [][]string{
		{"NewBareScratchRepo(", "head := consumer.PushSnapshot(", "EnableActions(", "SetRepoVariable(", "DispatchConsumerWorkflow("},
		{"trial.NewConsumerRepository(", "trial.gitRemote(", "trial.waitWorkflows(", "trial.api.Actions.CreateWorkflowDispatchEventByFileName("},
	} {
		rest := body
		for _, call := range calls {
			at := strings.Index(rest, call)
			if at < 0 {
				t.Fatalf("self-build setup must retain readiness order %q; missing %q", calls, call)
			}

			rest = rest[at+len(call):]
		}
	}
}
