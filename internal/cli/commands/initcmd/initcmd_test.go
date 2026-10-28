// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package initcmd

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/diggsweden/forge-ci/internal/domain/consumerinit"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

type fixtureRemote struct{}

func (fixtureRemote) VersionTags(context.Context, string) ([]string, error) {
	return []string{"v1.2.3"}, nil
}

func (fixtureRemote) TagCommit(context.Context, string, string) (string, error) {
	return strings.Repeat("a", 40), nil
}

func (fixtureRemote) Export(_ context.Context, _, _, dest string, _ []string) ([]string, error) {
	dir := filepath.Join(dest, "examples", "go-cli", "github")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}

	if err := os.WriteFile(filepath.Join(dir, "pullrequest-workflow.yml"), []byte("name: my-cli\nref: v0.90.0\n"), 0o600); err != nil {
		return nil, err
	}

	if err := os.WriteFile(filepath.Join(dir, "release-workflow.yml"), []byte("name: my-cli\n"), 0o600); err != nil {
		return nil, err
	}

	return []string{domain.DefaultsFile}, nil
}

func TestInitCommandDryRun(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "new_files"
		if conflict {
			name = "existing_workflow"
		}

		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()

			existing := filepath.Join(dir, ".github", "workflows", "pullrequest-workflow.yml")
			if conflict {
				if err := os.MkdirAll(filepath.Dir(existing), 0o750); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(existing, []byte("keep this workflow\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var output bytes.Buffer

			cmd := newCommand(fixtureRemote{})
			cmd.Writer, cmd.ErrWriter = &output, &output

			err := cmd.Run(t.Context(), []string{"init", "https://github.com/fixture/forge-ci", dir, "--example", "go-cli", "--ref", "v1.2.3", "--dry-run"})
			if conflict != errors.Is(err, errs.ErrUsage) || (!conflict && err != nil) {
				t.Fatalf("conflict=%t: unexpected error: %v", conflict, err)
			}

			for _, expected := range []string{
				"Forge CI initialization preview", "Project directory: " + dir,
				"Template source: https://github.com/fixture/forge-ci", "Release: v1.2.3",
				"Resolved commit: " + strings.Repeat("a", 40), "Forge: github", "Example: go-cli",
				".github/workflows/release-workflow.yml", "my-cli", "No project files were written",
				"temporary storage", "no Nanolinter or MegaLinter configuration is generated",
			} {
				if !strings.Contains(output.String(), expected) {
					t.Errorf("missing %q in preview:\n%s", expected, output.String())
				}
			}

			if conflict {
				if !strings.Contains(output.String(), "generation blocked") || !strings.Contains(output.String(), "Other proposed files") || strings.Contains(output.String(), "repeat the command") {
					t.Fatalf("misleading conflict output: %s", output.String())
				}

				body, readErr := os.ReadFile(existing)
				if readErr != nil || string(body) != "keep this workflow\n" {
					t.Fatalf("existing workflow changed: %q, %v", body, readErr)
				}
			} else if !strings.Contains(output.String(), "Would create") || !strings.Contains(output.String(), "repeat the command without --dry-run") {
				t.Fatalf("missing next step: %s", output.String())
			}

			if _, statErr := os.Lstat(filepath.Join(dir, ".github", "workflows", "release-workflow.yml")); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("preview wrote a proposed file: %v", statErr)
			}
		})
	}
}

func TestInitCommandWithoutDryRunCreatesFiles(t *testing.T) {
	dir := t.TempDir()

	var output bytes.Buffer

	cmd := newCommand(fixtureRemote{})
	cmd.Writer, cmd.ErrWriter = &output, &output

	err := cmd.Run(t.Context(), []string{"init", "https://github.com/fixture/forge-ci", dir, "--example", "go-cli", "--ref", "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "pullrequest-workflow.yml"))
	if err != nil || string(body) != "name: my-cli\nref: v1.2.3\n" {
		t.Fatalf("rendered workflow: %q, %v", body, err)
	}

	if !strings.Contains(output.String(), "Created:") || strings.Contains(output.String(), "No project files were written") {
		t.Fatalf("misleading generation report: %s", output.String())
	}

	for _, expected := range []string{
		"in the template repository https://github.com/fixture/forge-ci at commit " + strings.Repeat("a", 40),
		"(release v1.2.3), read docs/github.md", "Then run forge-ci doctor in your project",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("missing documentation handoff %q: %s", expected, output.String())
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "docs")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("initialization should not imply a local documentation tree: %v", err)
	}
}
