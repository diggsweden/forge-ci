// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabbehavior

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/diggsweden/forge-ci/internal/testutil/gitlabrender"
)

func TestReleaseArtifactsAreReadableWithoutOpeningPrivateFiles(t *testing.T) {
	t.Parallel()

	// Each producer job and the hand-off directory it leaves for the next.
	for job, dir := range map[string]string{
		"Prepare: changelog":     "_release-render",
		"Prepare: version":       "_release-bump",
		"Build: release files":   "_release-build",
		"Images: plan":           "_release-images-plan",
		"Images: build amd64":    "_release-images-amd64",
		"Images: build arm64":    "_release-images-arm64",
		"Images: publish":        "_release-images",
		"Sign: files and images": "_release-sign",
		"Publish: images":        "_release-promote",
	} {
		t.Run(job, func(t *testing.T) {
			t.Parallel()

			workspace := t.TempDir()
			handoff := filepath.Join(workspace, dir)
			private := filepath.Join(workspace, "private")
			incoming := filepath.Join(workspace, "incoming")

			for _, dir := range []string{handoff, private, incoming} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			for _, file := range []string{filepath.Join(handoff, "payload"), filepath.Join(private, "key"), filepath.Join(incoming, "payload")} {
				if err := os.WriteFile(file, []byte("fixture bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if err := os.Symlink(private, filepath.Join(handoff, "private-link")); err != nil {
				t.Fatal(err)
			}

			runReleaseAfterScript(t, workspace, job)
			requireMode(t, handoff, 0o755)
			requireMode(t, filepath.Join(handoff, "payload"), 0o644)
			requireMode(t, private, 0o700)
			requireMode(t, filepath.Join(private, "key"), 0o600)
			requireMode(t, incoming, 0o700)
			requireMode(t, filepath.Join(incoming, "payload"), 0o600)

			if body, err := os.ReadFile(filepath.Join(handoff, "payload")); err != nil || string(body) != "fixture bytes" {
				t.Fatal("artifact publication changed its bytes")
			}
		})
	}
}

func TestReleaseArtifactsNeverFollowASymlinkedOutputRoot(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	private := filepath.Join(t.TempDir(), "private")

	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(private, filepath.Join(workspace, "_release-sign")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(private, "key"), []byte("private fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	runReleaseAfterScript(t, workspace, "Sign: files and images")
	requireMode(t, private, 0o700)
	requireMode(t, filepath.Join(private, "key"), 0o600)
}

// runReleaseAfterScript runs a job's after_script as GitLab runs it, named
// another job by a project variable, which must not move its hand-off.
func runReleaseAfterScript(t *testing.T, workspace, job string) {
	t.Helper()

	body := releaseJobsBodyFor(t, "buildah")

	script := joinedScript(gitlabrender.ResolveExtends(t, body, body[job].(map[string]any))["after_script"])
	if script == "" {
		t.Fatal("release artifact publication hook is missing")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, componentBash(t), "--noprofile", "--norc", "-c", script)
	command.Dir = workspace
	command.Env = []string{"PATH=" + componentTools(t), "HOME=" + workspace, "CI_PROJECT_DIR=" + workspace,
		"CI_JOB_NAME=Publish: release", "rc_handoff=private", "CI_JOB_STATUS=failed"}

	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("artifact hook failed: %v\n%s", err, output)
	}
}

func requireMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != mode {
		t.Fatalf("%s mode=%#o, want %#o", path, info.Mode().Perm(), mode)
	}
}
