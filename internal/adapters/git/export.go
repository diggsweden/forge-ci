// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// commitPrefix is an abbreviated or full commit SHA.
var commitPrefix = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

var (
	errNotAPin        = errors.New("is neither a tag nor a commit SHA of the checkout; a branch moves, so it is no pin")
	errNoSingleCommit = errors.New("names no single commit of the checkout")
)

// noLazyFetch keeps a partial clone from fetching a missing object from its
// promisor remote: the export reads the checkout, never the network.
const noLazyFetch = "GIT_NO_LAZY_FETCH=1"

// TagOrCommit resolves a tag, or a commit SHA, to its commit; a branch
// name, which moves, is not a pin and is refused.
func (r *Repo) TagOrCommit(ctx context.Context, ref string) (string, error) {
	if commit, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", refsTagsPrefix+ref+"^{commit}"); err == nil {
		return commit, nil
	}

	if !commitPrefix.MatchString(ref) {
		return "", fmt.Errorf("%q %w", ref, errNotAPin)
	}

	commit, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil || !strings.HasPrefix(commit, ref) {
		return "", fmt.Errorf("%q %w", ref, errNoSingleCommit)
	}

	return commit, nil
}

// ExportCommit writes the given paths of commit's tree into dest and returns
// those the tree lacks. It reads committed objects only, through an index of
// its own with hooks off, so the checkout's index, work tree and HEAD stay as
// they are, and never fetches: a partial clone that lacks the commit's files
// fails.
func (r *Repo) ExportCommit(ctx context.Context, commit, dest string, paths []string) ([]string, error) {
	scratch, err := os.MkdirTemp("", "forge-ci-export-")
	if err != nil {
		return nil, err
	}

	defer func() { _ = os.RemoveAll(scratch) }()

	var present, missing []string

	for _, p := range paths {
		if statErr := r.runEnv(ctx, []string{noLazyFetch}, "cat-file", "-e", "--end-of-options", commit+":"+p); statErr != nil {
			missing = append(missing, p)
		} else {
			present = append(present, p)
		}
	}

	if len(present) > 0 {
		args := append([]string{"-c", hooksDisabledConfig, "--work-tree=" + dest, "checkout", commit, "--"}, present...)
		if err := r.runEnv(ctx, []string{noLazyFetch, "GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}, args...); err != nil {
			return nil, err
		}
	}

	return missing, nil
}
