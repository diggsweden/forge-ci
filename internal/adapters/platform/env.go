// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package platform detects which CI platform the binary is running on
// based on environment variables. Thin lookup — no business logic, no
// I/O beyond os.Getenv.
//
// Detection has two orthogonal axes, mirroring internal/domain/provider:
//
//   - Detect()       → provider.ForgeAPI   (which forge API to call)
//   - DetectRunner() → provider.RunnerKind (which runner conventions to emit)
//
// Forgejo Actions sets GITHUB_ACTIONS=true while exposing a Gitea/Forgejo
// forge API *and* a distinct runner dialect (it renders neither GitHub
// workflow commands nor job summaries — go-gitea/gitea#27898 — and uses
// $FORGEJO_* sinks). So both axes treat Forgejo as its own value, probed
// ahead of the GitHub branch. Both axes honour an explicit override so a
// runner masquerading as another forge can always be corrected.
package platform

import (
	"os"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/provider"
	"github.com/diggsweden/forge-ci/internal/runcontext"
)

// Override env vars, read when the caller states no override of its own. The
// root --provider / --runner flags fall back to the same names, so a command
// line and a caller without one agree.
const (
	envProviderOverride = "FORGE_CI_PROVIDER"
	envRunnerOverride   = "FORGE_CI_RUNNER"
)

// Detect returns the active forge API (provider.ForgeAPI).
//
// override is the caller's explicit choice, the root --provider value. Empty
// states nothing and defers to FORGE_CI_PROVIDER; "auto" asks for detection.
// The override travels as an argument rather than through the process
// environment, so a command line never changes what the next caller in the
// same process detects.
//
// Order matters: Forgejo masquerades as GitHub (it sets
// GITHUB_ACTIONS=true), so the Forgejo/Gitea signal is probed BEFORE the
// GitHub branch. A valid override wins over all auto-detection.
//
//	override (or FORGE_CI_PROVIDER) valid → that platform
//	Forgejo/Gitea signal present          → Forgejo
//	GITHUB_ACTIONS=true                   → GitHub
//	GITLAB_CI=true                        → GitLab
//	else                                  → Local
func Detect(override string) provider.ForgeAPI {
	if p := provider.ForgeAPI(stated(override, envProviderOverride)); p.IsValid() {
		return p
	}

	switch {
	case isForgejoTarget():
		return provider.ForgeForgejo
	case ciFlag("GITHUB_ACTIONS"):
		return provider.ForgeGitHub
	case ciFlag("GITLAB_CI"):
		return provider.ForgeGitLab
	default:
		return provider.ForgeLocal
	}
}

// DetectRunner returns the active runner conventions (provider.RunnerKind).
//
// Forgejo/Gitea Actions sets GITHUB_ACTIONS=true but is a *distinct* runner
// dialect — it does not render GitHub workflow commands and exposes its own
// $FORGEJO_* sinks — so the Forgejo signal is probed BEFORE the GitHub
// branch (same precedence as Detect). override is the root --runner value,
// read as Detect reads its own: empty defers to FORGE_CI_RUNNER, and a valid
// value wins over auto-detection.
//
//	override (or FORGE_CI_RUNNER) valid        → that runner
//	GITHUB_ACTIONS=true + Forgejo/Gitea signal → RunnerForgejo
//	GITHUB_ACTIONS=true                        → RunnerGitHub
//	GITLAB_CI=true                             → RunnerGitLab
//	else                                       → RunnerLocal
func DetectRunner(override string) provider.RunnerKind {
	if r := provider.RunnerKind(stated(override, envRunnerOverride)); r.IsValid() {
		return r
	}

	switch {
	case isForgejoRunner():
		return provider.RunnerForgejo
	case ciFlag("GITHUB_ACTIONS"):
		return provider.RunnerGitHub
	case ciFlag("GITLAB_CI"):
		return provider.RunnerGitLab
	default:
		return provider.RunnerLocal
	}
}

// stated returns the normalised override, or the named variable's value when
// the caller stated none. "auto" and an unknown value are not valid on either
// axis, so they fall through to detection.
func stated(override, envName string) string {
	if override == "" {
		override = os.Getenv(envName)
	}

	return strings.ToLower(strings.TrimSpace(override))
}

// isForgejoTarget reports whether the FORGE API to talk to is Forgejo/Gitea.
//
// This is a question about the destination, so naming one is enough:
// $FORGEJO_SERVER_URL or $FORGEJO_REPOSITORY set on ANY runner means "talk
// to Forgejo", which is exactly how a GitHub-hosted repo publishes to a
// Forgejo instance.
func isForgejoTarget() bool {
	if isForgejoRunner() {
		return true
	}

	if !ciFlag("GITHUB_ACTIONS") {
		return false
	}

	return os.Getenv("FORGEJO_SERVER_URL") != "" || os.Getenv("FORGEJO_REPOSITORY") != ""
}

// isForgejoRunner reports whether we are EXECUTING on a Forgejo/Gitea runner.
//
// Split from isForgejoTarget because the two questions have different answers.
// $FORGEJO_SERVER_URL is why: a Forgejo runner sets it, but so does a workflow
// on ANY runner that publishes to Forgejo. Read as identity it makes a GitHub
// runner claim to be Forgejo, suppressing the GitHub annotations the run should
// emit (RunnerKind picks the output dialect) and making $GITHUB_TOKEN look like
// a Forgejo credential.
//
// runcontext owns the marker names and the rule; this just binds it to the
// process environment.
func isForgejoRunner() bool {
	return runcontext.ForgejoRunner(os.Getenv)
}

// ciFlag reports whether the named CI marker env var is exactly "true",
// the value GitHub/Forgejo/GitLab runners set on their respective
// markers (GITHUB_ACTIONS / GITLAB_CI).
func ciFlag(name string) bool {
	return os.Getenv(name) == "true"
}
