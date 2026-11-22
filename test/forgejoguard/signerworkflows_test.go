// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// check-l3-isolation lets a signing secret reach a job only when it is the
// prepare job, the sign job or a call of one of the layer's signer workflows.
// That list is the set of workflows whose secrets contract names a signing
// secret: a new signer workflow left off it would have its callers refused,
// and a workflow kept on it after it stopped taking keys would let any caller
// hand it one.
func TestIsolationSignerWorkflowsAreTheOnesThatTakeSigningSecrets(t *testing.T) {
	t.Parallel()

	signing := validate.DefaultSigningSecrets()

	var takers []string

	for _, workflow := range allWorkflows(t) {
		for line := range strings.Lines(string(reporoot.ReadFile(t, workflow))) {
			contract, found := strings.CutPrefix(strings.TrimSpace(line), "# workflow-call-secrets-contract:")
			if found && slices.ContainsFunc(strings.Fields(contract), func(secret string) bool { return slices.Contains(signing, secret) }) {
				takers = append(takers, workflow)
			}
		}
	}

	slices.Sort(takers)

	listed := slices.DeleteFunc(validate.SignerWorkflows(), func(workflow string) bool { return !strings.HasPrefix(workflow, ".forgejo/") })
	slices.Sort(listed)

	if !slices.Equal(takers, listed) {
		t.Errorf("the isolation check's signer workflows are %q; the workflows whose secrets contract names a signing secret are %q", listed, takers)
	}
}
