// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// Every GitHub release example is a release workflow to validate isolation
// (it calls the release orchestrator) and passes it as shipped: named
// secrets to the orchestrator, never secrets: inherit, no signing key in a
// job of its own.
func TestGitHubReleaseExamplesPassReleaseIsolation(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)

	files, err := filepath.Glob(filepath.Join(root, "examples", "*", "github", "release-workflow.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)

		relative, err := filepath.Rel(root, file)
		require.NoError(t, err)

		require.Containsf(t, string(body), "/.github/workflows/release-orchestrator.yml@", "%s calls no release orchestrator", relative)
		require.NotContainsf(t, string(body), "secrets: inherit", "%s passes secrets: inherit", relative)

		violations, err := validate.CheckIsolation(
			[]byte(strings.ReplaceAll(string(body), "PIN_FORGE_CI_SHA", strings.Repeat("a", 40))),
			validate.IsolationConfig{SigningSecrets: validate.DefaultSigningSecrets()})
		require.NoErrorf(t, err, "%s", relative)
		require.Emptyf(t, violations, "%s", relative)
	}
}
