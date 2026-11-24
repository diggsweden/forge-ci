// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package consumerinit writes a consumer's first Forge CI files: the
// examples of the commit it pins, read from a local checkout of the Forge
// CI fork at that commit, and filled with the values the fork's operator
// records there.
package consumerinit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	domain "github.com/diggsweden/forge-ci/internal/domain/consumerinit"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	"github.com/diggsweden/forge-ci/internal/domain/version"
)

var errSeveralForks = errors.New("the checkout has the builds of several forks")

// Checkout reads the committed history of a local Forge CI checkout,
// never its working tree, and never the network.
type Checkout interface {
	Tags(ctx context.Context) ([]string, error)
	// Commit is the commit a tag or a commit SHA names.
	Commit(ctx context.Context, ref string) (string, error)
	TagsAt(ctx context.Context, commit string) ([]string, error)
	// Export writes paths of the commit's tree into dest and returns those
	// the tree does not have.
	Export(ctx context.Context, commit, dest string, paths []string) (missing []string, err error)
	// Origin is the address of the checkout's origin remote.
	Origin(ctx context.Context) (string, error)
}

// Input is one `forge-ci init` run.
type Input struct {
	Checkout Checkout
	// URL is the fork's web address the workflows name; empty takes the
	// checkout's origin.
	URL string
	// Ref is a tag or a commit SHA to pin; empty takes the newest release
	// tag under the release policy.
	Ref string
	// ReleasePolicy is stable or fork:WORD; empty takes fork:WORD when the
	// checkout's fork builds name exactly one WORD, otherwise stable.
	ReleasePolicy string
	Dir           string // the consumer's root
	Example       string // example to start from, e.g. go-cli
	DryRun        bool   // render and inspect destinations without writing project files
}

// Result says what was pinned, proposed and written, and what still needs review.
type Result struct {
	Directory string
	DryRun    bool
	Forge     provider.ForgeAPI
	Source    domain.Source
	Defaults  bool // the fork has a defaults file at the pinned tag
	Written   []string
	Proposed  []string
	Kept      []string // existing files init keeps rather than refuses, such as the changelog configuration
	Conflicts []string
	Remaining map[string][]domain.Placeholder
	// Project is what init read from the consumer's build files and wrote in
	// place of the example's project placeholders.
	Project domain.Project
	// Optional names the example's workflows init did not write, such as a
	// snapshot workflow, which a person may copy from the release by hand.
	Optional []string
}

// Run pins a commit of the checkout, reads its examples and defaults at
// that commit, and writes the example's files for the consumer's forge. It
// writes nothing in dry-run mode or when any target already exists.
func Run(ctx context.Context, in Input) (Result, error) {
	tag, commit, err := pin(ctx, in.Checkout, in.Ref, in.ReleasePolicy)
	if err != nil {
		return Result{}, err
	}

	address := in.URL
	if address == "" {
		origin, err := in.Checkout.Origin(ctx)
		if err != nil {
			return Result{}, fmt.Errorf("the checkout has no origin remote to name the fork in the workflows; pass --url https://<host>/<owner>/<repository>: %w", errs.ErrUsage)
		}

		address = origin
	}

	src, err := domain.ParseForgeURL(address)
	if err != nil {
		return Result{}, err
	}

	src.Tag, src.Commit = tag, commit

	snapshot, err := os.MkdirTemp("", "forge-ci-init-")
	if err != nil {
		return Result{}, err
	}

	defer func() { _ = os.RemoveAll(snapshot) }()

	// The fork's tree may hold symbolic links; reading through the root
	// keeps every one inside the snapshot.
	root, err := os.OpenRoot(snapshot)
	if err != nil {
		return Result{}, err
	}

	defer func() { _ = root.Close() }()

	defaults, found, err := readRelease(ctx, in.Checkout, src, snapshot, root.FS())
	if err != nil {
		return Result{}, err
	}

	forge, err := domain.ResolveForge(src.Host, defaults)
	if err != nil {
		return Result{}, err
	}

	result := Result{DryRun: in.DryRun, Forge: forge, Source: src, Defaults: found, Remaining: map[string][]domain.Placeholder{}}

	return initializeFiles(root.FS(), in, defaults, result)
}

func initializeFiles(release fs.FS, in Input, defaults domain.Defaults, result Result) (Result, error) {
	plan, optional, err := planFiles(release, result.Forge, in.Example)
	if err != nil {
		return Result{}, err
	}

	result.Optional = optional

	directory, err := filepath.Abs(in.Dir)
	if err != nil {
		return Result{}, err
	}

	result.Directory = directory
	result.Project = DetectProject(os.DirFS(directory))

	plan, result.Kept, err = keepExisting(directory, plan)
	if err != nil {
		return Result{}, err
	}

	files, err := render(release, plan, domain.Replacements(result.Forge, in.Example, result.Source, defaults), domain.ProjectReplacements(result.Project), &result)
	if err != nil {
		return Result{}, err
	}

	result.Conflicts, err = existingDestinations(directory, result.Proposed)
	if err != nil {
		return Result{}, err
	}

	if len(result.Conflicts) > 0 {
		return result, fmt.Errorf("existing destinations prevent initialization: %s; init writes only new files: %w", strings.Join(result.Conflicts, ", "), errs.ErrUsage)
	}

	if in.DryRun {
		return result, nil
	}

	err = write(files, directory, &result)

	return result, err
}

// keepExisting takes the copies the consumer may already have, and has,
// out of the plan.
func keepExisting(dir string, plan []domain.FileCopy) ([]domain.FileCopy, []string, error) {
	var kept []string

	remaining := plan[:0:0]
	for _, c := range plan {
		if c.KeepExisting {
			_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(c.To)))
			if err == nil {
				kept = append(kept, c.To)

				continue
			}

			if !errors.Is(err, fs.ErrNotExist) {
				return nil, nil, err
			}
		}

		remaining = append(remaining, c)
	}

	return remaining, kept, nil
}

func existingDestinations(dir string, proposed []string) ([]string, error) {
	var conflicts []string

	for _, file := range proposed {
		_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(file)))
		if err == nil {
			conflicts = append(conflicts, file)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}

	return conflicts, nil
}

// pin returns the release tag and the commit to pin: the newest release
// under the policy, the tag given, or the commit given and the release tag
// on it, if any.
func pin(ctx context.Context, checkout Checkout, ref, policy string) (string, string, error) {
	tags, err := checkout.Tags(ctx)
	if err != nil {
		return "", "", err
	}

	policy, err = releasePolicy(tags, policy)
	if ref != "" && errors.Is(err, errSeveralForks) {
		err = nil // a given ref needs no choice among the forks
	}

	if err != nil {
		return "", "", err
	}

	if ref == "" {
		if ref, err = latestTag(tags, policy); err != nil {
			return "", "", err
		}
	}

	commit, err := checkout.Commit(ctx, ref)
	if err != nil {
		return "", "", fmt.Errorf("--ref: %w: %w", err, errs.ErrUsage)
	}

	if slices.Contains(tags, ref) {
		return ref, commit, nil
	}

	// A commit pins as itself; a release tag on it still names the release.
	on, err := checkout.TagsAt(ctx, commit)
	if err != nil {
		return "", "", err
	}

	tag, _ := latestTag(on, policy)

	return tag, commit, nil
}

// releasePolicy is the given policy, else the one the checkout's tags show:
// fork:WORD when its fork builds name one WORD, stable when they name none.
func releasePolicy(tags []string, policy string) (string, error) {
	switch {
	case policy == version.ReleasePolicyStable:
		return "", nil
	case policy != "":
		if !strings.HasPrefix(policy, "fork:") || version.ValidateReleasePolicy(policy) != nil {
			return "", fmt.Errorf("--release-policy is stable or fork:WORD, the policy of the fork's own builds (got %q): %w", policy, errs.ErrUsage)
		}

		return policy, nil
	}

	switch forks := version.ForkPolicies(tags); len(forks) {
	case 0:
		return "", nil
	case 1:
		return forks[0], nil
	default:
		return "", fmt.Errorf("%w, %s; choose one with --release-policy: %w", errSeveralForks, strings.Join(forks, ", "), errs.ErrUsage)
	}
}

// latestTag is the newest tag init pins: the fork's own vX.Y.Z-WORD.N
// build under its fork:WORD policy, otherwise a stable X.Y.Z or vX.Y.Z.
func latestTag(tags []string, policy string) (string, error) {
	if policy != "" {
		if tag := version.LatestReleaseTag(tags, policy); tag != "" {
			return tag, nil
		}

		return "", fmt.Errorf("the checkout has no vX.Y.Z-WORD.N release tag under release-policy %s; pass --ref: %w", policy, errs.ErrUsage)
	}

	if tag := domain.LatestReleaseTag(tags); tag != "" {
		return tag, nil
	}

	return "", fmt.Errorf("the checkout has no stable X.Y.Z or vX.Y.Z release tag; fetch its tags, or pass --ref: %w", errs.ErrUsage)
}

// readRelease exports the commit's examples, changelog configurations and
// defaults file into snapshot and parses the defaults, when there are any,
// read through release, the snapshot's file system.
func readRelease(ctx context.Context, checkout Checkout, src domain.Source, snapshot string, release fs.FS) (domain.Defaults, bool, error) {
	missing, err := checkout.Export(ctx, src.Commit, snapshot, []string{domain.ExamplesDir, domain.ChangelogDir, domain.DefaultsFile})
	if err != nil {
		return domain.Defaults{}, false, err
	}

	if slices.Contains(missing, domain.ExamplesDir) {
		return domain.Defaults{}, false, fmt.Errorf("the checkout at %s has no %s/: it is not a Forge CI repository: %w", src.Release(), domain.ExamplesDir, errs.ErrUsage)
	}

	if slices.Contains(missing, domain.DefaultsFile) {
		return domain.Defaults{}, false, nil
	}

	data, err := fs.ReadFile(release, domain.DefaultsFile)
	if err != nil {
		return domain.Defaults{}, false, err
	}

	defaults, err := domain.ParseDefaults(data)

	return defaults, err == nil, err
}

// planFiles lists the available initialization files of the selected example.
func planFiles(release fs.FS, forge provider.ForgeAPI, example string) ([]domain.FileCopy, []string, error) {
	entries, err := fs.ReadDir(release, path.Join(domain.ExamplesDir, example, domain.ForgeDir(forge)))
	if err != nil {
		return nil, nil, fmt.Errorf("this release has no %s files for example %s; choose another with --example: %w", forge, example, errs.ErrUsage)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}

	// An older release may lack files newer examples have;
	// init writes what the pinned release holds.
	var plan []domain.FileCopy

	for _, c := range domain.Plan(forge, example, names) {
		if _, statErr := fs.Stat(release, c.From); statErr == nil {
			plan = append(plan, c)
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return nil, nil, statErr
		}
	}

	if !slices.ContainsFunc(plan, func(c domain.FileCopy) bool { return strings.HasPrefix(c.From, domain.ExamplesDir+"/") }) {
		return nil, nil, fmt.Errorf("this release has no initialization files for %s example %s: %w", forge, example, errs.ErrUsage)
	}

	return plan, domain.OptionalWorkflows(forge, names), nil
}

type renderedFile struct {
	path    string
	content []byte
}

// render is shared by previews and writes, including placeholder reporting.
func render(release fs.FS, plan []domain.FileCopy, replacements, project []domain.Replacement, result *Result) ([]renderedFile, error) {
	files := make([]renderedFile, 0, len(plan))
	for _, file := range plan {
		body, err := fs.ReadFile(release, file.From)
		if err != nil {
			return nil, err
		}

		content := domain.ApplyProject(domain.Apply(string(body), replacements), project)
		files = append(files, renderedFile{path: file.To, content: []byte(content)})
		result.Proposed = append(result.Proposed, file.To)

		if left := domain.Remaining(content); len(left) > 0 {
			result.Remaining[file.To] = left
		}
	}

	return files, nil
}

func write(files []renderedFile, dir string, result *Result) error {
	for _, file := range files {
		target := filepath.Join(dir, filepath.FromSlash(file.path))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}

		if err := os.WriteFile(target, file.content, 0o644); err != nil { //nolint:gosec // repository files, world-readable by design.
			return err
		}

		result.Written = append(result.Written, file.path)
	}

	return nil
}
