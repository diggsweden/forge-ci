// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabbehavior

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pinnedBuilderImage = "registry.example/kaniko-job@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// The request check refuses, before Prepare pushes anything, an export the
// export jobs would not carry out, and export commands without export-files,
// which would have no effect.
func TestReleaseExportCheckRefusesWhatTheJobsWouldNotCarryOut(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		args, commands, refusal string
	}{
		"off, no commands":          {args: "false '' '' '[]'", commands: "[]"},
		"commands without the flag": {args: "false '' '' '[]'", commands: `["bash .gitlab/export.sh"]`, refusal: "export-files is false"},
		"no builder job image":      {args: "true '' '' '[]'", commands: `["x"]`, refusal: "builder-job-image"},
		"amd64 by default":          {args: "true '" + pinnedBuilderImage + "' '' '[]'", commands: `["x"]`},
		"arm64 without its runner":  {args: "true '" + pinnedBuilderImage + "' 'linux/amd64,linux/arm64' '[]'", commands: `["x"]`, refusal: "build-runner-tags-arm64"},
		"arm64 with its runner":     {args: "true '" + pinnedBuilderImage + "' 'linux/arm64' '[\"arm64\"]'", commands: `["x"]`},
		"an unknown export-files":   {args: "maybe '' '' '[]'", commands: "[]", refusal: "true or false"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out, err := runReleaseFunction(t, t.TempDir(), "rc_check_exports "+test.args+" <<'EOF'\n"+test.commands+"\nEOF\n")
			if test.refusal == "" {
				if err != nil {
					t.Fatalf("refused: %v\n%s", err, out)
				}

				return
			}

			if err == nil || !strings.Contains(out, test.refusal) {
				t.Fatalf("err=%v, want a refusal naming %q:\n%s", err, test.refusal, out)
			}
		})
	}
}

// An export job of an architecture release-platforms leaves out exports
// nothing; every other runs.
func TestReleaseExportJobRunsForTheSelectedArchitectures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		arch, platforms string
		selected        bool
	}{
		{"amd64", "", true},
		{"arm64", "", true},
		{"amd64", "linux/arm64", false},
		{"arm64", "linux/amd64,linux/arm64", true},
		{"arm64", "linux/amd64", false},
	} {
		_, err := runReleaseFunction(t, t.TempDir(), "rc_export_selected "+test.arch+" '"+test.platforms+"'")
		if (err == nil) != test.selected {
			t.Errorf("%s with release-platforms %q: selected=%v, want %v", test.arch, test.platforms, err == nil, test.selected)
		}
	}
}

// The build job takes every export's dist/ and refuses a path two of them
// both hold, or one that is not a regular file or directory.
func TestReleaseBuildGathersTheExportsWithoutOverwriting(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	work := filepath.Join(project, "consumer")

	for _, dir := range []string{work, filepath.Join(project, "_release-export-amd64/dist/linux-amd64"), filepath.Join(project, "_release-export-arm64/dist/linux-arm64")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, arch := range []string{"amd64", "arm64"} {
		if err := os.WriteFile(filepath.Join(project, "_release-export-"+arch, "dist", "linux-"+arch, "hello-linux-"+arch), []byte(arch), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if out, err := runReleaseFunction(t, work, "rc_gather_exports true", "CI_PROJECT_DIR="+project); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, arch := range []string{"amd64", "arm64"} {
		if body, err := os.ReadFile(filepath.Join(work, "dist", "linux-"+arch, "hello-linux-"+arch)); err != nil || string(body) != arch {
			t.Fatalf("%s binary not gathered: %q %v", arch, body, err)
		}
	}

	if out, err := runReleaseFunction(t, work, "rc_gather_exports true", "CI_PROJECT_DIR="+project); err == nil || !strings.Contains(out, "must not exist yet") {
		t.Fatalf("a second export of the same path was merged: %v\n%s", err, out)
	}

	link := filepath.Join(project, "_release-export-arm64", "dist", "linux-arm64", "escape")
	if err := os.Symlink("/etc", link); err != nil {
		t.Fatal(err)
	}

	fresh := filepath.Join(project, "fresh")
	if err := os.Mkdir(fresh, 0o755); err != nil {
		t.Fatal(err)
	}

	if out, err := runReleaseFunction(t, fresh, "rc_gather_exports true", "CI_PROJECT_DIR="+project); err == nil || !strings.Contains(out, "regular files") {
		t.Fatalf("an export holding a link was gathered: %v\n%s", err, out)
	}

	if out, err := runReleaseFunction(t, fresh, "rc_gather_exports false", "CI_PROJECT_DIR="+project); err != nil {
		t.Fatalf("without export-files nothing is gathered: %v\n%s", err, out)
	}
}
