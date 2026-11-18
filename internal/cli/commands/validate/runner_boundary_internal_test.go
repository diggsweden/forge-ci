// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"github.com/diggsweden/forge-ci/internal/adapters/platform"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"testing"
)

func TestKeylessRunnerBoundary_DoesNotSelectTheAPITarget(t *testing.T) {
	for _, runner := range []string{"github", "gitlab", "local"} {
		t.Run(runner, func(t *testing.T) {
			testenv.New(t)
			t.Setenv("FORGE_CI_PROVIDER", "forgejo")
			t.Setenv("FORGEJO_SERVER_URL", "https://target.invalid")
			t.Setenv("FORGEJO_REPOSITORY", "target/repo")

			var want certIdentity

			switch runner {
			case "github":
				t.Setenv("GITHUB_ACTIONS", "true")
				t.Setenv("GITHUB_SERVER_URL", "https://github.com")
				t.Setenv("GITHUB_REPOSITORY", "owner/repo")

				want = certIdentity{Regexp: `^https://github\.com/`, Issuer: "https://token.actions.githubusercontent.com", GitHubWorkflowRepository: "owner/repo"}
			case "gitlab":
				t.Setenv("GITLAB_CI", "true")
				t.Setenv("CI_SERVER_URL", "https://gitlab.invalid")
				t.Setenv("CI_PROJECT_URL", "https://gitlab.invalid/owner/repo")

				want = certIdentity{Regexp: `^https://gitlab\.invalid/owner/repo/`, Issuer: "https://gitlab.invalid"}
			}

			require.Equal(t, want, keylessVerifyIdentity(&cli.Command{}, certIdentity{}))
			require.Equal(t, provider.ForgeForgejo, platform.Detect(""))

			want.Regexp = "explicit-regexp"
			require.Equal(t, want, keylessVerifyIdentity(&cli.Command{}, certIdentity{Regexp: "explicit-regexp"}))
		})
	}
}
