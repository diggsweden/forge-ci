// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"github.com/urfave/cli/v3"

	"github.com/diggsweden/forge-ci/internal/cli/deps"
	domainrelease "github.com/diggsweden/forge-ci/internal/domain/release"
)

// certIdentity is the keyless certificate constraint set the signature
// validators pass to cosign: the SAN regexp, the issuer, and on GitHub the
// repository whose run signed.
type certIdentity struct {
	Regexp                   string
	Issuer                   string
	GitHubWorkflowRepository string
}

// certIdentityFlags reads the operator's constraints.
func certIdentityFlags(cmd *cli.Command) certIdentity {
	return certIdentity{
		Regexp:                   cmd.String(flagCertIdentityRegexp),
		Issuer:                   cmd.String(flagCertOIDCIssuer),
		GitHubWorkflowRepository: cmd.String(flagCertGitHubWorkflowRepository),
	}
}

// verificationIdentity is the identity both signature validators verify
// against: KMS verification (an explicit --key, or --method kms) keeps the
// operator's values untouched, because an injected identity regexp would flip
// method auto-detection from kms to sigstore; everything else is filled from
// the detected forge where the operator left a constraint empty.
func verificationIdentity(cmd *cli.Command, method domainrelease.SignMethod, keyRef string, pinned certIdentity) certIdentity {
	if keyRef != "" || method == domainrelease.SignMethodKMS {
		return pinned
	}

	return keylessVerifyIdentity(cmd, pinned)
}

// keylessVerifyIdentity fills empty keyless constraints from the detected
// forge's keyless signing identity, so verifying a sigstore signature "as this
// repo on this forge" needs no hand-written regexp. The derived identity is
// pinned to the repository and read from the trusted runner environment, so
// it is both safer and less error-prone than a hand-typed pattern.
//
// "Trusted runner environment" is load-bearing, not decoration: this fills a
// value that decides which certificates are ACCEPTED, so the resolvers behind
// it read only what the runner injected. They use the run-context concepts
// through their ATTESTED traversal (ResolveAttested), which walks only the
// names the runner set — never the plain chains, which prefer the
// $REPOSITORY / $CI_SERVER_URL the orchestration layer computed. Right for
// describing a run, wrong for anchoring trust.
//
// That distinction was once lost to a consistency pass. It is now held by the
// compiler: provider.AnchorIdentity takes a runcontext.Attested, which only
// the attested traversal can mint.
//
// Explicit flag values always win (cross-repo verification stays possible),
// and each empty one is filled on its own, unless the operator named another
// issuer: a GitLab-signed file verified on a GitHub runner carries neither
// this forge's identity nor its repository claim.
//
// It is a no-op when the forge exposes no resolver or does not support keyless
// (Forgejo out of the box, local), preserving the prior behaviour where the
// operator supplied the constraints. Callers must skip it for KMS
// verification (a non-empty --key), where an identity regexp would otherwise
// flip auto-detection from kms to sigstore.
func keylessVerifyIdentity(cmd *cli.Command, pinned certIdentity) certIdentity {
	resolver, ok := deps.SigningIdentityForDetected(cmd)
	if !ok || !resolver.SupportsKeyless() {
		return pinned
	}

	id, err := resolver.ResolveKeylessIdentity()
	if err != nil {
		return pinned
	}

	// The derived identity and repository are claims of this forge's issuer.
	// Beside another issuer they would name nothing, so they stay the
	// operator's to give.
	if pinned.Issuer != "" && pinned.Issuer != id.OIDCIssuer {
		return pinned
	}

	got := certIdentity{Regexp: pinned.Regexp, Issuer: id.OIDCIssuer, GitHubWorkflowRepository: pinned.GitHubWorkflowRepository}
	if got.Regexp == "" {
		got.Regexp = id.SubjectRegexp
	}

	if got.GitHubWorkflowRepository == "" {
		got.GitHubWorkflowRepository = id.GitHubWorkflowRepository
	}

	return got
}
