// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build integration

package git_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/adapters/git"
	"github.com/diggsweden/forge-ci/internal/testutil/isolatedgit"
)

// A tag or a commit SHA names a commit, a branch does not; the export writes
// exactly the requested paths that commit has, reports the ones it lacks,
// and leaves the checkout's index, HEAD and uncommitted work alone.
func TestExportCommitReadsOnlyCommittedObjects(t *testing.T) {
	t.Parallel()

	repo := isolatedgit.NewRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo.Dir, "examples", "go-cli"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repo.Dir, "examples", "go-cli", "nanolinter.toml"), []byte("tagged\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo.Dir, "README.md"), []byte("not exported\n"), 0o600))
	repo.Git("add", "--all")
	repo.Git("commit", "-m", "release")
	repo.Git("tag", "-a", "-m", "v1.0.0", "v1.0.0")
	repo.Git("tag", "v1.0.0-lightweight")
	tagged := repo.Git("rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(repo.Dir, "examples", "go-cli", "nanolinter.toml"), []byte("after the tag\n"), 0o600))
	repo.Git("commit", "-am", "later")
	repo.Git("branch", "moving")
	require.NoError(t, os.WriteFile(filepath.Join(repo.Dir, "examples", "go-cli", "nanolinter.toml"), []byte("uncommitted\n"), 0o600))

	head, status := repo.Git("rev-parse", "HEAD"), repo.Git("status", "--porcelain")
	adapter := &git.Repo{Dir: repo.Dir}

	for _, ref := range []string{"v1.0.0", "v1.0.0-lightweight", tagged[:10], tagged} {
		commit, err := adapter.TagOrCommit(t.Context(), ref)
		require.NoError(t, err, ref)
		require.Equal(t, tagged, commit, ref)
	}

	for _, ref := range []string{"moving", "main", "HEAD", "v9.9.9", "ffffffffff"} {
		_, err := adapter.TagOrCommit(t.Context(), ref)
		require.Error(t, err, ref)
	}

	dest := t.TempDir()
	missing, err := adapter.ExportCommit(t.Context(), tagged, dest, []string{"examples", ".forge-ci/consumer-defaults.yml"})
	require.NoError(t, err)
	require.Equal(t, []string{".forge-ci/consumer-defaults.yml"}, missing)

	got, err := os.ReadFile(filepath.Join(dest, "examples", "go-cli", "nanolinter.toml"))
	require.NoError(t, err)
	require.Equal(t, "tagged\n", string(got))

	_, err = os.Stat(filepath.Join(dest, "README.md"))
	require.ErrorIs(t, err, os.ErrNotExist)

	require.Equal(t, head, repo.Git("rev-parse", "HEAD"))
	require.Equal(t, status, repo.Git("status", "--porcelain"))
	require.True(t, strings.Contains(status, "nanolinter.toml"), "the uncommitted change stays")
}

// A partial clone lacks the files of commits it did not check out. The
// export fails on them rather than fetching from the clone's origin, which
// stays reachable here, so a fetch would have succeeded.
func TestExportCommitNeverFetchesForAPartialClone(t *testing.T) {
	t.Parallel()

	source := isolatedgit.NewRepo(t)
	source.Git("config", "uploadpack.allowFilter", "true")
	require.NoError(t, os.MkdirAll(filepath.Join(source.Dir, "examples"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source.Dir, "examples", "file"), []byte("old\n"), 0o600))
	source.Git("add", "--all")
	source.Git("commit", "-m", "old")
	old := source.Git("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(source.Dir, "examples", "file"), []byte("new\n"), 0o600))
	source.Git("commit", "-am", "new")

	clone := filepath.Join(t.TempDir(), "clone")
	source.Git("clone", "--quiet", "--filter=blob:none", "file://"+source.Dir, clone)

	_, err := (&git.Repo{Dir: clone}).ExportCommit(t.Context(), old, t.TempDir(), []string{"examples"})
	require.Error(t, err, "the export fetched the missing file")
}
