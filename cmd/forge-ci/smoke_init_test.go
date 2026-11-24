//go:build smoke

// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
)

// CLI-11: init reads a local checkout. A URL, or a path that is no
// directory, is a usage error naming the clone that makes one, and writes
// nothing.
func TestBinary_InitRefusesWhatIsNoCheckout(t *testing.T) {
	_ = testenv.New(t)
	bin := buildBinary(t)

	for _, source := range []string{"https://github.com/diggsweden/forge-ci", filepath.Join(t.TempDir(), "absent")} {
		project := t.TempDir()

		stdout, stderr, code := runBinary(t, bin, "init", source, project, "--example", "go-cli")
		if code != 2 || !strings.Contains(stderr, "git clone") || stdout != "" {
			t.Errorf("%s: exit = %d, stdout %q, stderr %q; want a usage error with the clone line", source, code, stdout, stderr)
		}

		if entries, _ := os.ReadDir(project); len(entries) != 0 {
			t.Errorf("%s: init wrote %d entries into the project", source, len(entries))
		}
	}
}

// CLI-12: init writes from a checkout's committed history alone, with no
// network: the newest release tag, or a commit SHA pinned in full, named by
// the checkout's origin. The checkout's HEAD and status stay as they were.
func TestBinary_InitWritesFromACheckoutOffline(t *testing.T) {
	_ = testenv.New(t)
	bin := buildBinary(t)

	checkout := t.TempDir()
	git := func(args ...string) string {
		t.Helper()

		cmd := exec.Command("git", append([]string{"-c", "user.name=Smoke", "-c", "user.email=smoke@example.invalid", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...) //nolint:gosec // Test-authored git arguments.
		cmd.Dir = checkout
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}

		return strings.TrimSpace(string(out))
	}

	example := filepath.Join(checkout, "examples", "go-cli", "github")
	if err := os.MkdirAll(example, 0o750); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"pullrequest-workflow.yml", "release-workflow.yml"} {
		body := "uses: diggsweden/forge-ci/.github/workflows/x.yml@PIN_FORGE_CI_SHA # v0.90.0\n"
		if err := os.WriteFile(filepath.Join(example, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q")
	git("add", "--all")
	git("commit", "-q", "-m", "release")
	git("tag", "-a", "-m", "v1.0.0", "v1.0.0")
	git("commit", "-q", "--allow-empty", "-m", "unreleased")
	unreleased := git("rev-parse", "HEAD")
	git("remote", "add", "origin", "https://github.com/fixture/forge-ci.git")

	if err := os.WriteFile(filepath.Join(example, "release-workflow.yml"), []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status := git("status", "--porcelain")

	for _, tc := range []struct{ ref, release, pin string }{
		{release: "Release: v1.0.0", pin: "# v1.0.0"},
		{ref: unreleased[:10], release: "Release: commit " + unreleased[:12], pin: "@" + unreleased + " # commit " + unreleased[:12]},
	} {
		project := t.TempDir()

		args := []string{"init", checkout, project, "--example", "go-cli"}
		if tc.ref != "" {
			args = append(args, "--ref", tc.ref)
		}

		stdout, stderr, code := runBinary(t, bin, args...)
		if code != 0 || !strings.Contains(stdout, tc.release) || !strings.Contains(stdout, "Template source: https://github.com/fixture/forge-ci") {
			t.Fatalf("ref %q: exit = %d\nstdout: %s\nstderr: %s", tc.ref, code, stdout, stderr)
		}

		written, err := os.ReadFile(filepath.Join(project, ".github", "workflows", "release-workflow.yml"))
		if err != nil || !strings.Contains(string(written), tc.pin) || !strings.Contains(string(written), "fixture/forge-ci/") {
			t.Fatalf("ref %q: release workflow %q, %v; want the committed file pinned %q", tc.ref, written, err, tc.pin)
		}
	}

	if after := git("status", "--porcelain"); after != status {
		t.Fatalf("init changed the checkout: status %q, was %q", after, status)
	}
}
