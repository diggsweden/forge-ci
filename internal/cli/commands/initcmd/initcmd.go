// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package initcmd wires `forge-ci init`: a repository's first Forge CI
// files, from the examples of a local Forge CI checkout at the commit it
// pins.
package initcmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/diggsweden/forge-ci/internal/adapters/git"
	appinit "github.com/diggsweden/forge-ci/internal/app/consumerinit"
	domain "github.com/diggsweden/forge-ci/internal/domain/consumerinit"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/projecttype"
)

var errNoOrigin = errors.New("the checkout has no origin remote")

// cloneHint is how a person gets the checkout init reads.
const cloneHint = "clone it first, for example git clone https://github.com/diggsweden/forge-ci, then run forge-ci init ./forge-ci"

// New returns the `init` top-level command.
func New() *cli.Command {
	return newCommand(func(dir string) appinit.Checkout { return gitCheckout{repo: &git.Repo{Dir: dir}} })
}

func newCommand(open func(dir string) appinit.Checkout) *cli.Command {
	return &cli.Command{
		Name:      "init",
		Usage:     "write a repository's first Forge CI files from an example",
		ArgsUsage: "<forge-ci checkout> [directory]",
		Description: `Copies the example for the repository's ecosystem and forge into place,
pinned to one commit of a local checkout of Forge CI or of your
organization's fork. The examples and the values its operator records in
` + domain.DefaultsFile + ` are read from that commit's history, never
from the working tree or the network, so the files match what they pin.
The workflows name the fork by the checkout's origin, or by --url.
github.com is GitHub; any other host is the forge that file names.
Existing files are never overwritten. The report lists what is still left
to fill in. On Forgejo and GitLab it also writes the changelog
configuration to .forge-ci/gitcliff/, which their releases read, keeping
any such file the repository already has.
Linter configuration is not generated.

The commit is --ref, a tag or a commit SHA, else the newest stable X.Y.Z
or vX.Y.Z tag the checkout has (fetch its tags first to see newer ones);
if both spellings exist for the highest version, vX.Y.Z
wins. A fork that releases its own vX.Y.Z-WORD.N builds passes
--release-policy fork:WORD to pin the newest of those.

With --dry-run, render the same proposal and report existing-file conflicts
without writing project files. Conflicts return a nonzero status;
unresolved placeholders are reported for review, not treated as proof that
the workflows are ready to run.

EXAMPLES:
   # GitHub, from the published Forge CI:
   git clone https://github.com/diggsweden/forge-ci
   forge-ci init ./forge-ci

   # Forgejo or GitLab, from your organization's fork:
   git clone https://forge.example/examplescope/forge-ci
   forge-ci init ./forge-ci

   # Preview a chosen example and tag without writing project files:
   forge-ci init ./forge-ci --example go-service --ref v0.90.0 --dry-run

   # Another directory, pinned to a commit:
   forge-ci init ./forge-ci ../hello --example go-service --ref 0123abcd`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "example", Usage: "example to start from, one of " + strings.Join(domain.Examples(), ", ") + " (default: from the repository's build files)"},
			&cli.StringFlag{Name: "ref", Usage: "tag or commit SHA to pin (default: the newest stable X.Y.Z or vX.Y.Z tag; v-prefixed wins ties)"},
			&cli.StringFlag{Name: "release-policy", Usage: "fork:WORD pins the fork's newest vX.Y.Z-WORD.N build instead of a stable tag"},
			&cli.StringFlag{Name: "url", Usage: "the fork's address the workflows name, https://<host>/<owner>/<repository> (default: from the checkout's origin)"},
			&cli.BoolFlag{Name: "dry-run", Usage: "preview files, conflicts and remaining placeholders without writing project files"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 || cmd.NArg() > 2 {
				return fmt.Errorf("init takes <forge-ci checkout> [directory]; the checkout is a local clone of Forge CI "+
					"or of your organization's fork: %s (see forge-ci init --help): %w", cloneHint, errs.ErrUsage)
			}

			checkout := cmd.Args().Get(0)
			if strings.Contains(checkout, "://") || strings.HasPrefix(checkout, "git@") {
				return fmt.Errorf("init reads a local checkout, not %s: %s: %w", checkout, cloneHint, errs.ErrUsage)
			}

			if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
				return fmt.Errorf("%s is not a directory; init reads a local checkout of Forge CI: %s: %w", checkout, cloneHint, errs.ErrUsage)
			}

			dir := "."
			if cmd.NArg() == 2 {
				dir = cmd.Args().Get(1)
			}

			example, err := chooseExample(cmd.String("example"), dir, os.Stdin, cmd.Writer)
			if err != nil {
				return err
			}

			// Result is a value, and a dry run reports its conflicts with the
			// error on purpose.
			// nosemgrep: trailofbits.go.invalid-usage-of-modified-variable.invalid-usage-of-modified-variable
			result, err := appinit.Run(ctx, appinit.Input{
				Checkout: open(checkout), URL: cmd.String("url"),
				Ref: cmd.String("ref"), ReleasePolicy: cmd.String("release-policy"), Dir: dir, Example: example, DryRun: cmd.Bool("dry-run"),
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

	// The examples are for one project at the root. Build files one level
	// down are a repository with several artifacts, which no example fits.
	nested := ""
	if found := nestedProjects(dir, entries); len(found) > 0 {
		nested = fmt.Sprintf("; found %s below the root: a repository with several artifacts writes "+
			".forge-ci/artifacts.yml by hand (forge-ci docs artifacts-reference#monorepo-configuration), "+
			"then takes the workflows of one example", strings.Join(found, ", "))
	}

	if !term.IsTerminal(int(in.Fd())) {
		return "", fmt.Errorf("no build file at the root tells the ecosystem%s; pass --example, one of %s: %w",
			nested, strings.Join(domain.Examples(), ", "), errs.ErrUsage)
	}

	if nested != "" {
		_, _ = fmt.Fprintf(out, "No build file at the root%s.\n", nested)
	}

	_, _ = fmt.Fprintf(out, "Example (%s) [go-cli]: ", strings.Join(domain.Examples(), ", "))

	line, _ := bufio.NewReader(in).ReadString('\n')
	if answer := strings.TrimSpace(line); answer != "" {
		return chooseExample(answer, dir, in, out)
	}

	return "go-cli", nil
}

// nestedProjects names the immediate subdirectories of dir whose build files
// an example would match, as "api (go)".
func nestedProjects(dir string, entries []os.DirEntry) []string {
	var found []string

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		sub, err := os.ReadDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		names := make([]string, 0, len(sub))
		for _, s := range sub {
			names = append(names, s.Name())
		}

		if t := projecttype.DetectFromEntries(names); domain.ExampleFor(t) != "" {
			found = append(found, fmt.Sprintf("%s (%s)", entry.Name(), t))
		}
	}

	return found
}

func report(out io.Writer, example string, result appinit.Result) {
	title := "Forge CI initialized"
	if result.DryRun {
		title = "Forge CI initialization preview"
	}

	_, _ = fmt.Fprintf(out, "%s\n\nProject directory: %s\nTemplate source: https://%s/%s\nRelease: %s\nResolved commit: %s\nForge: %s\nExample: %s\n",
		title, result.Directory, result.Source.Host, result.Source.Repository, result.Source.Release(), result.Source.Commit, result.Forge, example)
	if result.Defaults {
		_, _ = fmt.Fprintf(out, "Template defaults: %s\n", domain.DefaultsFile)
	}

	reportFiles(out, result)
	reportProject(out, result.Project)

	if len(result.Remaining) == 0 {
		_, _ = fmt.Fprintln(out, "\nNo recognized placeholders remain. Review the generated build paths, names and release settings before running.")
	} else {
		reportRemaining(out, result)
	}

	if len(result.Optional) > 0 {
		_, _ = fmt.Fprintf(out, "\nOptional workflows in this example, not written: %s. Copy one from %s/%s/%s/ at %s if you want it.\n",
			strings.Join(result.Optional, ", "), domain.ExamplesDir, example, domain.ForgeDir(result.Forge), result.Source.Release())
	}

	_, _ = fmt.Fprintln(out, "\nLinter configuration: no Nanolinter or MegaLinter configuration is generated.")
	if !result.DryRun {
		_, _ = fmt.Fprintln(out, "Release setup: run forge-ci doctor --requirements for setup guidance; remote secrets and permissions are not checked.")
	}

	if result.DryRun {
		_, _ = fmt.Fprintln(out, "\nNo project files were written.")
		if len(result.Conflicts) > 0 {
			_, _ = fmt.Fprintln(out, "Initialization would refuse because destinations already exist. Review those files before proceeding.")
		} else {
			_, _ = fmt.Fprintf(out, "To generate these files, repeat the command without --dry-run, using --ref %s and --example %s.\n", result.Source.Ref(), example)
		}

		return
	}

	_, _ = fmt.Fprintf(out, "\nNext: fill the values above, then run forge-ci setup status for the remaining steps to a first release "+
		"(signers, secrets, forge-ci doctor, the release request).\n"+
		"Guide: the Getting started section of %s (offline: forge-ci docs %s)\n",
		forgeGuideLocation(result.Source, string(result.Forge)), result.Forge)
}

// reportProject says which values init took from the project's own build
// files, so a wrong guess is visible before it is committed.
func reportProject(out io.Writer, project domain.Project) {
	if project.Name == "" && project.MainPackage == "" {
		return
	}

	_, _ = fmt.Fprintln(out, "\nFrom your build files:")

	if project.Name != "" {
		note := ""
		if !project.FillsName() {
			note = " (not filled: the example name is also the image name, which takes lowercase letters, digits, '.', '_' and '-')"
		}

		_, _ = fmt.Fprintf(out, "  project name   %s%s\n", project.Name, note)
	}

	if project.MainPackage != "" {
		_, _ = fmt.Fprintf(out, "  main package   %s\n", project.MainPackage)
	}
}

// reportRemaining lists every placeholder by file and line, then once per
// kind of placeholder says where its value comes from.
func reportRemaining(out io.Writer, result appinit.Result) {
	_, _ = fmt.Fprintln(out, "\nStill to fill in before running")

	var hints []string

	seen := map[string]bool{}

	for _, f := range result.Proposed {
		for _, p := range result.Remaining[f] {
			_, _ = fmt.Fprintf(out, "  %s:%d  %s\n", f, p.Line, p.Token)

			if hint := domain.Hint(p.Token); !seen[hint] {
				seen[hint] = true
				hints = append(hints, fmt.Sprintf("  %-20s %s", p.Token, hint))
			}
		}
	}

	_, _ = fmt.Fprintln(out, "\nHow to fill them")
	_, _ = fmt.Fprintln(out, strings.Join(hints, "\n"))
	_, _ = fmt.Fprintln(out, "forge-ci doctor fails until they are filled; values only the release workflow reads warn until then.")
}

// forgeGuideLocation says where the per-forge guide for the pinned commit
// is. On github.com that is one URL; other forges lay out file URLs
// differently, so there the repository, the ref and the path are named
// instead.
func forgeGuideLocation(src domain.Source, forge string) string {
	path := "docs/" + forge + ".md"
	if src.Host == "github.com" {
		return fmt.Sprintf("https://%s/%s/blob/%s/%s", src.Host, src.Repository, src.Ref(), path)
	}

	return fmt.Sprintf("%s in https://%s/%s at %s", path, src.Host, src.Repository, src.Release())
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

	if len(result.Kept) > 0 {
		_, _ = fmt.Fprintln(out, "\nKept existing:")

		for _, file := range result.Kept {
			_, _ = fmt.Fprintf(out, "  %s\n", file)
		}
	}
}

// gitCheckout reads a local checkout with git.
type gitCheckout struct{ repo *git.Repo }

func (c gitCheckout) Tags(ctx context.Context) ([]string, error) {
	return c.repo.ListTags(ctx, "")
}

func (c gitCheckout) Commit(ctx context.Context, ref string) (string, error) {
	return c.repo.TagOrCommit(ctx, ref)
}

func (c gitCheckout) TagsAt(ctx context.Context, commit string) ([]string, error) {
	return c.repo.TagsPointingAt(ctx, commit)
}

func (c gitCheckout) Export(ctx context.Context, commit, dest string, paths []string) ([]string, error) {
	return c.repo.ExportCommit(ctx, commit, dest, paths)
}

// Origin is the origin remote's address. Git answers a remote it does not
// know with the name itself.
func (c gitCheckout) Origin(ctx context.Context) (string, error) {
	origin, err := c.repo.RemoteURL(ctx)
	if err == nil && origin == "origin" {
		err = errNoOrigin
	}

	return origin, err
}
