// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package github

import (
	"fmt"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	"github.com/diggsweden/forge-ci/internal/runcontext"
)

// SupportsKeyless reports whether GitHub Actions can supply an OIDC token for
// Sigstore keyless signing. It delegates to Capabilities so the claim has a
// single source of truth, and reads MintsOIDCToken because that is the
// question the role asks: can this forge mint a token at all.
func (p *Provider) SupportsKeyless() bool { return p.Capabilities().MintsOIDCToken }

// ResolveKeylessIdentity returns the issuer, audience, and verification
// identity for the running GitHub Actions job. The issuer reuses Describe()
// (the fixed Actions token endpoint). The repository is pinned through
// GitHubWorkflowRepository, not the SAN: Fulcio names a GitHub certificate
// after the workflow file that ran, so a release signed by a reusable workflow
// of another repository, such as forge-ci's, carries that repository's name
// in its SAN. A SAN anchored to this repository refused every such release,
// and one anchored to the workflow's repository alone accepts any repository
// that calls it. The regexp therefore admits any workflow on this server, and
// the repository claim decides whose run signed.
//
// SubjectID is $GITHUB_SERVER_URL joined with $GITHUB_WORKFLOW_REF
// (owner/repo/.github/workflows/file@ref): the SAN of a workflow that signs in
// its own file, not of a reusable workflow it calls. It is informational and
// empty when the runner did not inject the ref (e.g. outside a workflow).
// $GITHUB_REPOSITORY and $GITHUB_SERVER_URL are read by NAME here on
// purpose, not through runcontext.Repository()/ServerURL(). SubjectRegexp and
// GitHubWorkflowRepository become cosign's --certificate-identity-regexp and
// --certificate-github-workflow-repository, so they decide which
// certificates verification ACCEPTS: they must come from what the runner
// injected, never from the bare $REPOSITORY the orchestration layer computes.
// Nor from a chain spanning forges -- $FORGEJO_REPOSITORY on a GitHub runner
// names a target, not this repository.
func (p *Provider) ResolveKeylessIdentity() (provider.KeylessIdentity, error) {
	env := p.envFunc()

	// Both reads are ATTESTED: they walk only the names this runner injected,
	// so the $REPOSITORY the orchestration layer computed cannot reach the
	// constraints below. $GITHUB_SERVER_URL has no default here on purpose --
	// context.go may guess github.com when merely describing a run, but a GHES
	// runner also sets $GITHUB_ACTIONS=true, so guessing the forge for a trust
	// anchor would accept certificates issued for a same-named repository on a
	// different host.
	repo, ok := runcontext.Repository().ResolveAttested(env)
	if !ok {
		return provider.KeylessIdentity{}, fmt.Errorf(
			"a runner-provided repository (%s) is required to resolve the keyless signing identity: %w",
			runcontext.Repository(), errs.ErrUsage)
	}

	server, ok := runcontext.ServerURL().ResolveAttested(env)
	if !ok {
		return provider.KeylessIdentity{}, fmt.Errorf(
			"a runner-provided server URL (%s) is required to resolve the keyless signing identity: %w",
			runcontext.ServerURL(), errs.ErrUsage)
	}

	subjectID := ""
	if ref := strings.TrimSpace(env("GITHUB_WORKFLOW_REF")); ref != "" {
		subjectID = server.String() + "/" + ref
	}

	return provider.KeylessIdentity{
		OIDCIssuer:               p.Describe().OIDCIssuer,
		TokenAudience:            provider.KeylessAudience,
		SubjectID:                subjectID,
		SubjectRegexp:            provider.AnchorIdentity(server),
		GitHubWorkflowRepository: strings.TrimSpace(repo.String()),
	}, nil
}
