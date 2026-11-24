// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build integration

package initcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/adapters/git"
	"github.com/diggsweden/forge-ci/internal/testutil/isolatedgit"
)

func TestMain(m *testing.M) { os.Exit(isolatedgit.Main(m.Run)) }

// The checkout init reads with git: its tags, the tags on a commit, and its
// origin, which git answers with the name itself when there is none.
func TestGitCheckoutReadsARealCheckout(t *testing.T) {
	t.Parallel()

	repo := isolatedgit.NewRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo.Dir, "README.md"), []byte("forge-ci\n"), 0o600))
	repo.Git("add", "--all")
	repo.Git("commit", "-m", "release")
	repo.Git("tag", "-a", "-m", "v1.0.0", "v1.0.0")
	commit := repo.Git("rev-parse", "HEAD")

	checkout := gitCheckout{repo: &git.Repo{Dir: repo.Dir}}

	tags, err := checkout.Tags(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"v1.0.0"}, tags)

	on, err := checkout.TagsAt(t.Context(), commit)
	require.NoError(t, err)
	require.Equal(t, []string{"v1.0.0"}, on)

	_, err = checkout.Origin(t.Context())
	require.ErrorIs(t, err, errNoOrigin)

	repo.Git("remote", "add", "origin", "git@forge.example:org/forge-ci.git")

	origin, err := checkout.Origin(t.Context())
	require.NoError(t, err)
	require.Equal(t, "git@forge.example:org/forge-ci.git", origin)
}
