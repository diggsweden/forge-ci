// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"

	"github.com/stretchr/testify/require"
)

// A consumer file trusts its own checkout, and nothing else. safe.directory
// set to `*` turns Git's ownership check off for every repository in the job,
// a planted one included, where the layer's own lint templates and checkout
// trust exactly the checkout. A consumer job names the checkout as the forge
// exposes it: ${{ forgejo.workspace }} on Forgejo, $CI_PROJECT_DIR on GitLab.
// Copies of the examples spread whatever they show, so the examples and the
// live consumers are held to it.

// trustsEveryDirectory matches safe.directory set to `*`: through the
// GIT_CONFIG_* environment, whose value is the only one `*` is given to, or
// through git -c and git config.
var trustsEveryDirectory = regexp.MustCompile(`GIT_CONFIG_VALUE_[0-9]+["']?\s*[:=]\s*["']?\*["']?\s*$|safe\.directory["']?\s*[= ]\s*["']?\*`)

func TestTrustsEveryDirectory(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		line string
		want bool
	}{
		{`  GIT_CONFIG_VALUE_0: "*"`, true},
		{`  GIT_CONFIG_VALUE_0: '*'`, true},
		{`  GIT_CONFIG_VALUE_1: *`, true},
		{`export GIT_CONFIG_VALUE_0='*'`, true},
		{`git config --global --add safe.directory '*'`, true},
		{`git -c safe.directory=* status`, true},
		{`  GIT_CONFIG_VALUE_0: ${{ forgejo.workspace }}`, false},
		{`  GIT_CONFIG_VALUE_0: $CI_PROJECT_DIR`, false},
		{`git -c safe.directory="${CI_PROJECT_DIR}" status`, false},
		{`  GIT_CONFIG_VALUE_0: "*.example"`, false},
	} {
		if got := trustsEveryDirectory.MatchString(tc.line); got != tc.want {
			t.Errorf("trustsEveryDirectory(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestConsumerFilesTrustOnlyTheirCheckout(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)

	var violations []string

	files := 0

	for _, dir := range []string{"examples", filepath.Join("test", "live")} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || !slices.Contains([]string{".yml", ".yaml", ".sh"}, filepath.Ext(path)) {
				return walkErr
			}

			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			files++
			relative, _ := filepath.Rel(root, path)

			for number, line := range strings.Split(string(body), "\n") {
				if trustsEveryDirectory.MatchString(line) {
					violations = append(violations, relative+":"+strconv.Itoa(number+1)+": "+strings.TrimSpace(line))
				}
			}

			return nil
		})
		require.NoError(t, err)
	}

	require.NotZero(t, files, "no consumer file was read")
	require.Emptyf(t, violations, "safe.directory '*' trusts every repository in the job; trust the checkout alone, "+
		"${{ forgejo.workspace }} on Forgejo or $CI_PROJECT_DIR on GitLab:\n  %s", strings.Join(violations, "\n  "))
}
