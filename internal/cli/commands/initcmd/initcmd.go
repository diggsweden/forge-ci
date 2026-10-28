// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package initcmd wires `forge-ci init`: a repository's first Forge CI
// files, from the selected release's examples, pinned to a Forge CI mirror.
package initcmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/diggsweden/forge-ci/internal/adapters/git"
	appinit "github.com/diggsweden/forge-ci/internal/app/consumerinit"
	domain "github.com/diggsweden/forge-ci/internal/domain/consumerinit"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/projecttype"
	"github.com/diggsweden/forge-ci/internal/runcontext"
)

// New returns the `init` top-level command.
func New() *cli.Command {
	return newCommand(gitRemote{})
}

func newCommand(remote appinit.Remote) *cli.Command {
	return &cli.Command{
		Name:      "init",
		Usage:     "write a repository's first Forge CI files from an example",
		ArgsUsage: "<forge-ci URL> [directory]",
		Description: `Copies the example for the repository's ecosystem and forge into place,
pinned to the newest release tag of the Forge CI repository the URL names.
The examples and the values its operator records in ` + domain.DefaultsFile + `
are read from that repository at that tag, so the files match the release
they pin. github.com is GitHub; any other host is the forge that file
names. Existing files are never overwritten. The report lists what is
still left to fill in. Linter configuration is not generated.

With --dry-run, render the same proposal and report existing-file conflicts
without writing project files. Templates are still fetched into temporary
storage. Conflicts return a nonzero status; unresolved placeholders are
reported for review, not treated as proof that the workflows are ready to run.

Automatic selection accepts stable X.Y.Z and vX.Y.Z tags, excludes prereleases
and build metadata, and keeps the exact tag name. If both spellings exist for
the highest version, vX.Y.Z wins; use --ref to select the other explicitly.

EXAMPLES:
   # GitHub, from the published Forge CI:
   forge-ci init https://github.com/diggsweden/forge-ci

   # Forgejo or GitLab, from your organization's mirror:
   forge-ci init https://github.com/diggsweden/forge-ci

   # Preview without writing project files:
   forge-ci init https://github.com/diggsweden/forge-ci --example go-service --ref v0.90.0 --dry-run

   # Another directory, a chosen example and tag:
   forge-ci init https://github.com/diggsweden/forge-ci ../hello --example go-service --ref v0.90.0`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "example", Usage: "example to start from, one of " + strings.Join(domain.Examples(), ", ") + " (default: from the repository's build files)"},
			&cli.StringFlag{Name: "ref", Usage: "release tag to pin (default: the newest stable X.Y.Z or vX.Y.Z tag; v-prefixed wins ties)"},
			&cli.BoolFlag{Name: "dry-run", Usage: "preview files, conflicts and remaining placeholders without writing project files; still fetches templates"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 || cmd.NArg() > 2 {
				return fmt.Errorf("init takes <forge-ci URL> [directory]: %w", errs.ErrUsage)
			}

			dir := "."
			if cmd.NArg() == 2 {
				dir = cmd.Args().Get(1)
			}

			example, err := chooseExample(cmd.String("example"), dir, os.Stdin, cmd.Writer)
			if err != nil {
				return err
			}

			result, err := appinit.Run(ctx, appinit.Input{
				Remote: remote, URL: cmd.Args().Get(0),
				Ref: cmd.String("ref"), Dir: dir, Example: example, DryRun: cmd.Bool("dry-run"),
			})
			if err != nil {
				if result.DryRun && len(result.Conflicts) > 0 {
					report(cmd.Writer, example, result)
				}

				return err
			}

			report(cmd.Writer, example, result)

			return nil
		},
	}
}

// chooseExample takes the flag, else the example the build files imply,
// else asks when a person is at the terminal.
func chooseExample(flag, dir string, in *os.File, out io.Writer) (string, error) {
	if flag != "" {
		if !slices.Contains(domain.Examples(), flag) {
			return "", fmt.Errorf("--example %q is not one of %s: %w", flag, strings.Join(domain.Examples(), ", "), errs.ErrUsage)
		}

		return flag, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if example := domain.ExampleFor(projecttype.DetectFromEntries(names)); example != "" {
		return example, nil
	}

	if !term.IsTerminal(int(in.Fd())) {
		return "", fmt.Errorf("no build file tells the ecosystem; pass --example, one of %s: %w", strings.Join(domain.Examples(), ", "), errs.ErrUsage)
	}

	_, _ = fmt.Fprintf(out, "Example (%s) [go-cli]: ", strings.Join(domain.Examples(), ", "))

	line, _ := bufio.NewReader(in).ReadString('\n')
	if answer := strings.TrimSpace(line); answer != "" {
		return chooseExample(answer, dir, in, out)
	}

	return "go-cli", nil
}

func report(out io.Writer, example string, result appinit.Result) {
	title := "Forge CI initialized"
	if result.DryRun {
		title = "Forge CI initialization preview"
	}

	_, _ = fmt.Fprintf(out, "%s\n\nProject directory: %s\nTemplate source: https://%s/%s\nRelease: %s\nResolved commit: %s\nForge: %s\nExample: %s\n",
		title, result.Directory, result.Source.Host, result.Source.Repository, result.Source.Tag, result.Source.Commit, result.Forge, example)
	if result.Defaults {
		_, _ = fmt.Fprintf(out, "Template defaults: %s\n", domain.DefaultsFile)
	}

	reportFiles(out, result)

	if len(result.Remaining) == 0 {
		_, _ = fmt.Fprintln(out, "\nNo recognized placeholders remain. Review the generated build paths, names and release settings before running.")
	} else {
		_, _ = fmt.Fprintln(out, "\nReview and customize before running")

		for _, f := range result.Proposed {
			for _, p := range result.Remaining[f] {
				_, _ = fmt.Fprintf(out, "  %s:%d  %s\n", f, p.Line, p.Token)
			}
		}
	}

	_, _ = fmt.Fprintln(out, "\nLinter configuration: no Nanolinter or MegaLinter configuration is generated.")
	if result.DryRun {
		_, _ = fmt.Fprintln(out, "\nNo project files were written. Templates were fetched into temporary storage.")
		if len(result.Conflicts) > 0 {
			_, _ = fmt.Fprintln(out, "Initialization would refuse because destinations already exist. Review those files before proceeding.")
		} else {
			_, _ = fmt.Fprintf(out, "To generate these files, repeat the command without --dry-run, using --ref %s and --example %s.\n", result.Source.Tag, example)
		}

		return
	}

	_, _ = fmt.Fprintf(out, "\nNext: in the template repository https://%s/%s at commit %s (release %s), read docs/%s.md, Getting started, for the secrets and variables. Then run forge-ci doctor in your project.\n",
		result.Source.Host, result.Source.Repository, result.Source.Commit, result.Source.Tag, result.Forge)
}

func reportFiles(out io.Writer, result appinit.Result) {
	files := result.Written

	heading := "Created"
	if result.DryRun {
		files, heading = result.Proposed, "Would create"
		if len(result.Conflicts) > 0 {
			heading = "Other proposed files"
			_, _ = fmt.Fprintln(out, "\nExisting files — generation blocked:")

			for _, file := range result.Conflicts {
				_, _ = fmt.Fprintf(out, "  %s\n", file)
			}
		}
	}

	_, _ = fmt.Fprintf(out, "\n%s:\n", heading)

	for _, file := range files {
		if !slices.Contains(result.Conflicts, file) {
			_, _ = fmt.Fprintf(out, "  %s\n", file)
		}
	}

	if len(files) == len(result.Conflicts) {
		_, _ = fmt.Fprintln(out, "  (none)")
	}
}

// gitRemote reads the mirror with git, anonymously.
type gitRemote struct{}

func (gitRemote) VersionTags(ctx context.Context, repoURL string) ([]string, error) {
	return git.New().RemoteReleaseTags(ctx, repoURL, runcontext.Credential{})
}

func (gitRemote) TagCommit(ctx context.Context, repoURL, tag string) (string, error) {
	return git.New().RemoteTagCommit(ctx, repoURL, tag, runcontext.Credential{})
}

func (gitRemote) Export(ctx context.Context, repoURL, tag, dest string, paths []string) ([]string, error) {
	return git.ExportAtTag(ctx, repoURL, tag, dest, paths)
}
