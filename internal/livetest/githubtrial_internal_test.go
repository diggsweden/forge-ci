// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v91/github"
)

func TestReuseGitHubEngineOnlyReadsPreparedSessionEngine(t *testing.T) {
	t.Parallel()
	trial := &GitHubTrial{tb: t, org: "example-org", prefix: "fc-"}
	root := t.TempDir()
	trial.git(root, "init", "--quiet", "--object-format=sha1", "--initial-branch=main", ".")
	trial.git(root, "-c", "user.name=Trial", "-c", "user.email=trial@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "engine")
	commit := strings.TrimSpace(trial.git(root, "rev-parse", "HEAD"))
	reads := map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("engine reuse attempted mutation: %s %s", request.Method, request.URL)
			response.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		reads[request.URL.Path]++

		var body any

		switch request.URL.Path {
		case "/repos/example-org/fc-forge-ci/git/ref/heads/main", "/repos/example-org/fc-forge-ci/git/ref/tags/" + GitHubPrerelease:
			body = map[string]any{"object": map[string]string{"sha": commit}}
		case "/repos/example-org/fc-forge-ci/actions/workflows/" + githubEngineWorkflow + "/runs":
			if request.URL.Query().Get("head_sha") != commit || request.URL.Query().Get("event") != "push" {
				t.Errorf("engine build lookup not pinned: %s", request.URL)
			}

			body = map[string]any{"workflow_runs": []map[string]string{{"status": "completed", "conclusion": "success"}}}
		default:
			t.Errorf("unexpected engine request: %s", request.URL)
			response.WriteHeader(http.StatusNotFound)

			return
		}

		if err := json.NewEncoder(response).Encode(body); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	client, err := github.NewClient(github.WithHTTPClient(server.Client()), github.WithURLs(new(server.URL+"/"), nil))
	if err != nil {
		t.Fatal(err)
	}

	trial.api = client

	got := trial.ReuseEngine(root)
	if got != (GitHubEngine{Owner: trial.org, Repository: "fc-forge-ci", Commit: commit, Namespace: "ghcr.io/example-org/fc-forge-ci"}) || len(reads) != 3 {
		t.Fatalf("reuse = %+v; reads = %v", got, reads)
	}
}

func TestGitHubWaitRunReturnsCancelledTerminalRun(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/actions/workflows/pullrequest.yml/runs") {
			t.Errorf("unexpected run lookup: %s %s", r.Method, r.URL.Path)
			response.WriteHeader(http.StatusNotFound)

			return
		}

		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"workflow_runs":[{"id":42,"status":"completed","conclusion":"cancelled"}]}`))
	}))
	defer server.Close()

	client, err := github.NewClient(github.WithHTTPClient(server.Client()), github.WithURLs(new(server.URL+"/"), nil))
	if err != nil {
		t.Fatal(err)
	}

	trial := &GitHubTrial{tb: t, api: client, org: "example-org"}

	run := trial.WaitRun("fc-cancelled", "pullrequest.yml", "pull_request", strings.Repeat("a", 40), time.Second)
	if run.GetID() != 42 || run.GetConclusion() != "cancelled" {
		t.Fatalf("cancelled terminal run was not returned: %+v", run)
	}
}

func TestGitHubConsumerPreservesTrackedIgnoredSource(t *testing.T) {
	t.Parallel()
	trial := &GitHubTrial{tb: t}
	root := t.TempDir()
	trial.git(root, "init", "--quiet", "--initial-branch=main", ".")

	for name, body := range map[string]string{
		".gitignore":      "bin/\n*.local\n",
		"src/bin/tool.rs": "fn main() {}\n",
		"test/live/github/workflows/pullrequest.yml": fixturePullRequest,
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	trial.git(root, "add", "--force", "--all")
	trial.commit(root, consumerBaseMessage, "")

	if err := os.WriteFile(filepath.Join(root, "secret.local"), []byte("not committed"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, _ := trial.stageConsumer(root, "test/live/github", trialEngine, nil)
	if got := trial.git(dir, "show", "HEAD:src/bin/tool.rs"); got != "fn main() {}\n" {
		t.Fatalf("tracked ignored source changed: %q", got)
	}

	if got := trial.git(dir, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(got, "secret.local") {
		t.Fatal("uncommitted ignored file entered the consumer snapshot")
	}
}

// A file the trial makes replaces the fixture's at that path in the pushed
// commit, and the source checkout is left alone.
func TestGitHubConsumerCommitsTrialFiles(t *testing.T) {
	t.Parallel()
	trial := &GitHubTrial{tb: t}
	root := t.TempDir()
	trial.git(root, "init", "--quiet", "--initial-branch=main", ".")

	for name, body := range map[string]string{
		".forge-ci/allowed_signers":                  "fixture\n",
		"test/live/github/workflows/pullrequest.yml": fixturePullRequest,
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	trial.git(root, "add", "--all")
	trial.commit(root, consumerBaseMessage, "")

	signers := trial.WithFile(".forge-ci/allowed_signers", "bot@example.com ssh-ed25519 AAAA\n")
	if trial.files != nil {
		t.Fatal("WithFile changed the trial it was called on")
	}

	dir, _ := signers.WithFile("extra/notes.txt", "made by the trial\n").stageConsumer(root, "test/live/github", trialEngine, nil)
	if got := trial.git(dir, "show", "HEAD:.forge-ci/allowed_signers"); got != "bot@example.com ssh-ed25519 AAAA\n" {
		t.Fatalf("allowed_signers = %q", got)
	}

	if got := trial.git(dir, "show", "HEAD:extra/notes.txt"); got != "made by the trial\n" {
		t.Fatalf("notes = %q", got)
	}

	if got := trial.git(root, "show", "HEAD:.forge-ci/allowed_signers"); got != "fixture\n" {
		t.Fatalf("source changed: %q", got)
	}
}

// The release-request tag is pushed only once GitHub lists every workflow just
// pushed as active: an event before that starts no run.
func TestGitHubListsAllRequiresEveryPushedWorkflowActive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"pullrequest.yml", "release.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	workflow := func(path, state string) *github.Workflow {
		return &github.Workflow{Path: new(".github/workflows/" + path), State: new(state)}
	}

	for name, test := range map[string]struct {
		listed []*github.Workflow
		want   bool
	}{
		"none listed yet": {listed: nil, want: false},
		"one missing":     {listed: []*github.Workflow{workflow("release.yml", "active")}, want: false},
		"one disabled": {listed: []*github.Workflow{
			workflow("release.yml", "active"), workflow("pullrequest.yml", "disabled_manually"),
		}, want: false},
		"all active": {listed: []*github.Workflow{
			workflow("release.yml", "active"), workflow("pullrequest.yml", "active"),
		}, want: true},
	} {
		if got := githubListsAll(&github.Workflows{Workflows: test.listed}, entries); got != test.want {
			t.Errorf("%s: githubListsAll() = %v, want %v", name, got, test.want)
		}
	}
}

func TestGPGSignatureMustBeTheTrialKeys(t *testing.T) {
	t.Parallel()

	trialKey := strings.Repeat("A1", 20)
	otherKey := strings.Repeat("B2", 20)

	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"the trial key", "[GNUPG:] GOODSIG A1A1A1A1A1A1A1A1 Trial\n[GNUPG:] VALIDSIG " + trialKey + " 2026-10-31 1793450000 0 4 0 22 10 00 " + trialKey + "\n", true},
		{"a subkey of the trial key", "[GNUPG:] VALIDSIG " + otherKey + " 2026-10-31 1793450000 0 4 0 22 10 00 " + strings.ToLower(trialKey) + "\n", true},
		{"another key", "[GNUPG:] VALIDSIG " + otherKey + " 2026-10-31 1793450000 0 4 0 22 10 00 " + otherKey + "\n", false},
		{"a bad signature", "[GNUPG:] BADSIG A1A1A1A1A1A1A1A1 Trial\n", false},
		{"the fingerprint outside a status line", "gpg: VALIDSIG " + trialKey + "\n", false},
	}

	for _, test := range tests {
		if got := signedBy(test.status, trialKey); got != test.want {
			t.Errorf("%s: signedBy = %v, want %v", test.name, got, test.want)
		}
	}
}

// A job with no log says whether a runner ever took it: a job the forge
// fails before dispatch has no runner trace to look for.
func TestNoLogReasonTellsAForgeDecisionFromAMissingLog(t *testing.T) {
	t.Parallel()

	none, task := int64(0), int64(3883)

	undispatched := RunJob{Name: "Build", RunsOn: []string{"ubuntu-latest"}, Needs: []string{"prepare"}, TaskID: &none}
	if got := undispatched.NoLogReason(); !strings.Contains(got, "no runner ever took it") ||
		!strings.Contains(got, "[ubuntu-latest]") || !strings.Contains(got, "[prepare]") {
		t.Errorf("an undispatched job: %q", got)
	}

	ran := RunJob{Name: "Build", TaskID: &task}
	if got := ran.NoLogReason(); !strings.Contains(got, "task 3883") || strings.Contains(got, "no runner") {
		t.Errorf("a job that ran: %q", got)
	}

	// A forge that does not report the task says nothing either way.
	if got := (RunJob{Name: "Build"}).NoLogReason(); !strings.Contains(got, "does not say") || strings.Contains(got, "before dispatch") {
		t.Errorf("a job of a forge without task_id: %q", got)
	}
}

// A signed release signs every subject: one signature beside unsigned files
// is reported, and signature files are not subjects of their own.
func TestUnsignedSubjectsListsEveryAssetWithoutAVerifiedSignature(t *testing.T) {
	t.Parallel()

	assets := map[string]int64{
		"app.tar.gz": 1, "app.tar.gz.bundle": 2,
		"app.jar": 3, "app.jar.asc": 4,
		"checksums.txt": 5,
		"sbom.cdx.json": 6, "sbom.cdx.json.sig": 7,
	}
	bundles := map[string]bool{"app.tar.gz": true}
	signatures := map[string]bool{"app.jar": true}

	got := unsignedSubjects(assets, bundles, signatures)
	want := []string{"checksums.txt", "sbom.cdx.json"}

	if !slices.Equal(got, want) {
		t.Errorf("unsignedSubjects = %v, want %v", got, want)
	}
}
