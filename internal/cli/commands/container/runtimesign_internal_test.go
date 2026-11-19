// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
)

func TestRuntimeSigningUsesFullRunnerRef(t *testing.T) {
	for _, tc := range []struct {
		name, forgejo, nativeRef, aliasRef, branch string
		admitted                                   bool
	}{
		{name: "Forgejo main", forgejo: "true", nativeRef: "refs/heads/main", admitted: true},
		{name: "Forgejo compatibility alias", forgejo: "true", aliasRef: "refs/heads/main", admitted: true},
		{name: "GitHub main", aliasRef: "refs/heads/main", admitted: true},
		{name: "Forgejo master", forgejo: "true", nativeRef: "refs/heads/master", branch: "master", admitted: true},
		{name: "master policy refuses main", forgejo: "true", nativeRef: "refs/heads/main", branch: "master"},
		{name: "Forgejo tag outranks alias", forgejo: "true", nativeRef: "refs/tags/main", aliasRef: "refs/heads/main"},
		{name: "GitHub ignores foreign ref", nativeRef: "refs/heads/main", aliasRef: "refs/heads/topic"},
		{name: "short name is insufficient", forgejo: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testenv.New(t)
			env.Setenv("GITHUB_ACTIONS", "true")
			env.Setenv("FORGEJO_ACTIONS", tc.forgejo)
			env.Setenv("FORGEJO_REF", tc.nativeRef)
			env.Setenv("FORGEJO_REF_NAME", "main")
			env.Setenv("GITHUB_REF", tc.aliasRef)
			env.Setenv("GITHUB_REF_NAME", "main")
			env.Setenv("REF", "refs/heads/main")
			env.Setenv("PUBLICATION_REF", "refs/heads/main")
			env.Setenv("RUNTIME_PUBLICATION_BRANCH", tc.branch)
			authFile := env.Path("auth.json")
			require.NoError(t, os.WriteFile(authFile, []byte(`{"auths":{}}`), 0o600))

			// An invalid SHA stops the command before registry or signing calls.
			err := runtimePublicationSignCmd().Run(t.Context(), []string{"sign", "--flavor", "go", "--method", "kms",
				"--key", "env://COSIGN_KEY", "--auth-file", authFile, "--source-sha", "invalid"})
			require.ErrorIs(t, err, errs.ErrValidation)
			if tc.admitted {
				require.ErrorContains(t, err, "SOURCE_SHA must be a git commit hex digest")
			} else {
				require.ErrorContains(t, err, "runtime signing requires the ")
			}
		})
	}
}
