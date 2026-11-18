// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

// Shared flag names and usage text for the three signature-verification
// commands (artifact-signature, container-signature, container-attestation),
// which accept the same gpg/sigstore/kms verification inputs. Named once so the
// definitions stay in sync and the package stays under goconst's literal
// budget.
const (
	flagMethod             = "method"
	flagCertIdentityRegexp = "cert-identity-regexp"
	flagCertOIDCIssuer     = "cert-oidc-issuer"
	flagKey                = "key"

	flagCertGitHubWorkflowRepository = "cert-github-workflow-repository"

	usageCertIdentityRegexp = "regexp the Fulcio cert identity must match for --method=sigstore (matched against the full identity URL: on GitHub the signing workflow, e.g. ^https://github\\.com/diggsweden/forge-ci/\\.github/workflows/; on GitLab the project, e.g. ^https://gitlab\\.com/<group>/<project>/)"

	usageCertGitHubWorkflowRepository = "owner/repo whose GitHub Actions run must have signed, for --method=sigstore (the certificate's repository claim; a reusable workflow's identity names its own repository, not the caller's); required with a GitHub issuer, and defaults to this repository on a GitHub runner"
)
