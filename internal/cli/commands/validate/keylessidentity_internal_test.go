// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"testing"

	"github.com/urfave/cli/v3"

	domainrelease "github.com/diggsweden/forge-ci/internal/domain/release"
)

// clearForgejoSignals neutralises the Forgejo probe so a GITHUB_ACTIONS=true
// environment detects as GitHub, not Forgejo.
func clearForgejoSignals(t *testing.T) {
	t.Helper()
	t.Setenv("FORGEJO_ACTIONS", "")
	t.Setenv("FORGEJO_SERVER_URL", "")
	t.Setenv("FORGEJO_REPOSITORY", "")
	t.Setenv("FORGEJO_OUTPUT", "")
}

func TestKeylessVerifyIdentity_DefaultsFromGitHub(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GITHUB_REPOSITORY", "acme/app")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	clearForgejoSignals(t)

	got := keylessVerifyIdentity(&cli.Command{}, certIdentity{})

	// A release signed by a reusable workflow carries that workflow's
	// repository in its SAN, so the repository claim pins the run.
	want := certIdentity{Regexp: `^https://github\.com/`, Issuer: "https://token.actions.githubusercontent.com", GitHubWorkflowRepository: "acme/app"}
	if got != want {
		t.Errorf("identity = %+v, want %+v", got, want)
	}
}

func TestKeylessVerifyIdentity_ExplicitWins(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "acme/app")
	clearForgejoSignals(t)

	pinned := certIdentity{Regexp: `^https://github\.com/other/`, Issuer: "https://issuer.example", GitHubWorkflowRepository: "other/app"}
	if got := keylessVerifyIdentity(&cli.Command{}, pinned); got != pinned {
		t.Errorf("explicit values should win, got %+v", got)
	}
}

// TestKeylessVerifyIdentity_LocalIsNoop covers the forges with no keyless
// support of their own -- a local checkout, and Forgejo out of the box. Both
// take the same branch: no resolver, or one that reports SupportsKeyless()
// false, so whatever the operator passed comes back untouched.
//
// Returning empty constraints looks alarming, because they travel unchanged
// into `cosign verify --certificate-identity-regexp ""`, and an empty regexp
// matches every identity. It is safe, and the reason is entirely outside this
// package: cosign refuses the flag rather than honouring it --
//
//	Error: --certificate-identity or --certificate-identity-regexp is
//	required for verification in keyless mode
//
// (verified against cosign 3.1.3). So the operator on Forgejo must supply the
// constraints or verification fails; it never silently accepts any signer.
// Recorded here because the question is natural, the answer is three packages
// away, and it rests on cosign's behaviour rather than on ours.
func TestKeylessVerifyIdentity_LocalIsNoop(t *testing.T) {
	// No CI signal → local forge → no SigningIdentityResolver → no-op.
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("GITLAB_CI", "")
	clearForgejoSignals(t)

	if got := keylessVerifyIdentity(&cli.Command{}, certIdentity{}); got != (certIdentity{}) {
		t.Errorf("local forge should leave constraints empty, got %+v", got)
	}
}

// TestVerificationIdentity_ForgeOverrideAndKMSMatrix covers every cell the
// two signature validators share: each forge the runner can be detected as,
// with no, some or all constraints pinned, a GitHub runner whose identity
// cannot be resolved, and the KMS cases, where nothing is injected whatever
// the forge. Pinned values always survive, and a derived value only fills an
// empty one, and only beside the forge's own issuer: a certificate of another
// issuer carries neither the forge's identity nor its repository claim.
//
// Not parallel: forge detection reads the process environment.
func TestVerificationIdentity_ForgeOverrideAndKMSMatrix(t *testing.T) {
	const (
		pinnedRegexp     = `^https://github\.com/other/`
		pinnedIssuer     = "https://issuer.example"
		pinnedRepository = "other/app"
	)

	forges := map[string]map[string]string{
		"github":                    {"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "acme/app", "GITHUB_SERVER_URL": "https://github.com"},
		"gitlab":                    {"GITLAB_CI": "true", "CI_SERVER_URL": "https://gitlab.example", "CI_PROJECT_URL": "https://gitlab.example/group/app"},
		"github without repository": {"GITHUB_ACTIONS": "true", "GITHUB_SERVER_URL": "https://github.com"},
		"local":                     {},
	}

	pins := []certIdentity{
		{},
		{Regexp: pinnedRegexp},
		{Issuer: pinnedIssuer},
		{GitHubWorkflowRepository: pinnedRepository},
		{Regexp: pinnedRegexp, Issuer: pinnedIssuer},
		{Issuer: "https://token.actions.githubusercontent.com"},
		{Regexp: pinnedRegexp, Issuer: pinnedIssuer, GitHubWorkflowRepository: pinnedRepository},
	}

	for forge, env := range forges {
		for _, pin := range pins {
			for _, kms := range []kmsSelector{{"", ""}, {domainrelease.SignMethodSigstore, ""}, {"", "awskms:///alias/release"}, {domainrelease.SignMethodKMS, ""}} {
				t.Run(forge+"/"+pin.Regexp+"|"+pin.Issuer+"|"+pin.GitHubWorkflowRepository+"/"+string(kms.method)+"|"+kms.key, func(t *testing.T) {
					for _, key := range []string{"GITHUB_ACTIONS", "GITHUB_REPOSITORY", "GITHUB_SERVER_URL", "GITLAB_CI", "CI_SERVER_URL", "CI_PROJECT_URL"} {
						t.Setenv(key, env[key])
					}

					clearForgejoSignals(t)

					want := expectedVerificationIdentity(forge, pin, kms)

					if got := verificationIdentity(&cli.Command{}, kms.method, kms.key, pin); got != want {
						t.Errorf("got %+v, want %+v", got, want)
					}
				})
			}
		}
	}
}

// kmsSelector is the method and key that decide whether verification is KMS.
type kmsSelector struct {
	method domainrelease.SignMethod
	key    string
}

// expectedVerificationIdentity keeps pinned values and, outside KMS
// verification on a forge with a resolvable identity, fills the empty ones
// from that forge.
func expectedVerificationIdentity(forge string, pin certIdentity, kms kmsSelector) certIdentity {
	derived := map[string]certIdentity{
		"github": {Regexp: `^https://github\.com/`, Issuer: "https://token.actions.githubusercontent.com", GitHubWorkflowRepository: "acme/app"},
		"gitlab": {Regexp: `^https://gitlab\.example/group/app/`, Issuer: "https://gitlab.example"},
	}

	values, ok := derived[forge]
	if !ok || kms.key != "" || kms.method == domainrelease.SignMethodKMS || (pin.Issuer != "" && pin.Issuer != values.Issuer) {
		return pin
	}

	want := pin
	if want.Regexp == "" {
		want.Regexp = values.Regexp
	}

	if want.Issuer == "" {
		want.Issuer = values.Issuer
	}

	if want.GitHubWorkflowRepository == "" {
		want.GitHubWorkflowRepository = values.GitHubWorkflowRepository
	}

	return want
}
