// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"github.com/stretchr/testify/require"
)

// Retention deletes only when the caller passes dry-run: false; the default,
// and any other value, previews. The engine's max-delete bound stays wired.
func TestCallableRetentionDeletesOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	file := ".forgejo/workflows/package-retention.yml"
	body := string(reporoot.ReadFile(t, file))
	w := loadWorkflow(t, file)
	require.Empty(t, callableOnlyViolation([]byte(body)))
	require.Len(t, w.jobs, 1)

	root := loadDocument(t, file)
	inputs := mappingValue(mappingValue(mappingValue(root, "on"), "workflow_call"), "inputs")
	dryRun := mappingValue(inputs, "dry-run")
	require.Equal(t, "boolean", scalarString(mappingValue(dryRun, "type")))
	require.Equal(t, "true", scalarString(mappingValue(dryRun, "default")))
	require.Nil(t, mappingValue(dryRun, "required"))

	retention := w.jobs["retention"].node
	require.Nil(t, mappingValue(retention, "strategy"), "one Go pass must see the complete protected inventory, not per-pin matrix jobs")
	job := retention.String()
	require.Contains(t, job, `--dry-run="${dry_run}"`)
	require.NotContains(t, job, "--dry-run=false")
	require.Contains(t, job, "--max-delete")
	require.NotContains(t, job, "go build")
	require.NotContains(t, job, "platform checkout")
	require.Contains(t, job, "sha256sum --check --strict")
	require.Contains(t, job, "inputs['engine-sha256']")
	require.Contains(t, job, "inputs['packages-json']")
	require.NotContains(t, job, "matrix.digest")

	for _, forbidden := range []string{"itiquette", "secrets.RELEASE_COSIGN", "secrets.RELEASE_SSH", "secrets.RELEASE_GPG"} {
		require.NotContains(t, body, forbidden)
	}
}
