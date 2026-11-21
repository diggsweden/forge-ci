// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package git_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/adapters/git"
)

// Against a real repository: the range is what the change adds, newest
// first, with trailers read from real commit objects; an end git could take
// for an option is refused before git runs.
func TestCommitRangeReadsTheCommitsOfAChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", append([]string{
			"-C", dir, "-c", "user.name=Ada Lind", "-c", "user.email=ada@example.org", "-c", "commit.gpgsign=false",
		}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	run("init", "--quiet", "--initial-branch=main")
	run("commit", "--quiet", "--allow-empty", "-m", "base")
	run("checkout", "--quiet", "-b", "change")
	run("commit", "--quiet", "--allow-empty", "-m", "first\n\nBody.\n\nSigned-off-by: Ada Lind <ada@example.org>")
	run("commit", "--quiet", "--allow-empty", "-m", "second")

	repo := &git.Repo{Dir: dir}

	commits, err := repo.CommitRange(t.Context(), "main", "change")
	require.NoError(t, err)
	require.Len(t, commits, 2)
	require.Equal(t, "second", commits[0].Subject)
	require.Equal(t, "first", commits[1].Subject)
	require.NotEmpty(t, commits[1].Author)
	require.Equal(t, "Signed-off-by", commits[1].Trailers[0].Key)
	require.Empty(t, commits[1].Signature)

	for _, end := range []string{"--output=/tmp/x", "main..change", "", "a b"} {
		_, err = repo.CommitRange(t.Context(), end, "change")
		require.Error(t, err, end)
	}
}
