// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// jobTokenSecrets are the secret names a forge reserves for the run's own
// job token: a secret under one of them is the job token.
var jobTokenSecrets = map[string]bool{"GITHUB_TOKEN": true, "GITEA_TOKEN": true, "FORGEJO_TOKEN": true}

// isJobTokenSecret reports whether a referenced secret is the job token.
// Forges compare secret names without case. The whole secrets context
// (secret == "") is every secret, not the job token.
func isJobTokenSecret(secret string) bool {
	return jobTokenSecrets[strings.ToUpper(secret)]
}

// dependencySecretPrefix names the secrets a build job may hold besides the
// job token: read-only dependency credentials (DEPENDENCY_GITHUB_TOKEN,
// DEPENDENCY_TOKEN, ...), which the build needs while it runs the project's
// code and which, by the documented contract, carry no write authority. The
// layer's own workflows read none of them.
const dependencySecretPrefix = "DEPENDENCY_"

// checkoutCommand matches a run: that fetches the repository: the engine's
// checkout, or git cloning, fetching or pulling.
var checkoutCommand = regexp.MustCompile(`\b(?:forge-ci|run_forge_ci)\s+platform\s+checkout\b|\bgit\b[^\n;&|]*\s(?:clone|fetch|pull)\b`)

// checksOutTheRepository reports whether a job of steps brings the
// project's code into its workspace: an actions/checkout or checkout-consumer
// step, a local action (code of the repository, run from its checkout), or a
// run: that checks the repository out or fetches it with git.
func checksOutTheRepository(job yaml.Node) bool {
	for _, step := range jobSteps(job) {
		uses := scalarValue(mappingValue(&step, "uses"))
		if usesAction(uses, "actions/checkout@") || usesAction(uses, "/checkout-consumer@") || strings.HasPrefix(uses, "./") {
			return true
		}

		if checkoutCommand.MatchString(scalarValue(mappingValue(&step, "run"))) {
			return true
		}
	}

	return false
}

// jobTokenViolations refuses every secret but the job token and the
// read-only dependency credentials in a job that
// checks the repository out: once the project's code is in the workspace,
// any step of the job can run it, and it reads whatever the job holds,
// whichever step, env name, file or $FORGEJO_ENV write the secret went to.
// A release, registry, API or dependency token belongs in a job that runs
// only the engine's code, or in a call of one of its workflows. The workflow
// env a job inherits is read as well, unless the job overrides the name.
// A signing secret and the whole secrets context are left to the
// signing-secret rule, which refuses them in every such job already.
func jobTokenViolations(workflowEnv yaml.Node, jobs map[string]yaml.Node, cfg IsolationConfig) []IsolationViolation {
	signing := map[string]bool{}
	for _, name := range cfg.SigningSecrets {
		signing[name] = true
	}

	isOtherToken := func(secret string) bool {
		return secret != "" && !signing[secret] && !isJobTokenSecret(secret) &&
			!strings.HasPrefix(strings.ToUpper(secret), dependencySecretPrefix)
	}

	var violations []IsolationViolation

	for _, name := range sortedKeys(jobs) {
		job := jobs[name]
		if !checksOutTheRepository(job) {
			continue
		}

		refs, err := findSecretRefsMatching(&job, isOtherToken)
		if err != nil {
			violations = append(violations, IsolationViolation{Line: job.Line, Msg: fmt.Sprintf("job %q: %v", name, err)})

			continue
		}

		for _, ref := range refs {
			violations = append(violations, IsolationViolation{Line: ref.line, Msg: fmt.Sprintf("job %q checks out the repository and references secret %s%s", name, ref.describe(), checkoutJobMayHold)})
		}

		violations = append(violations, inheritedTokenViolations(workflowEnv, name, &job, isOtherToken)...)
	}

	return violations
}

// checkoutJobMayHold ends each diagnostic of a checkout job with what the job may hold.
const checkoutJobMayHold = "; a job that runs the project's code may hold only the job token (${{ forgejo.token }}, ${{ github.token }} or secrets.GITHUB_TOKEN) and read-only DEPENDENCY_* credentials, so move the step that needs it to a job without a checkout"

// inheritedTokenViolations reports a secret in the workflow env that a job
// which checks the repository out inherits without overriding the name.
func inheritedTokenViolations(workflowEnv yaml.Node, name string, job *yaml.Node, isOtherToken func(string) bool) []IsolationViolation {
	overrides := map[string]bool{}
	for _, key := range mappingKeys(mappingValue(job, "env")) {
		overrides[key] = true
	}

	var violations []IsolationViolation

	for _, key := range mappingKeys(&workflowEnv) {
		if overrides[key] {
			continue
		}

		refs, err := findSecretRefsMatching(mappingValue(&workflowEnv, key), isOtherToken)
		if err != nil {
			return append(violations, IsolationViolation{Line: workflowEnv.Line, Msg: fmt.Sprintf("workflow env %q: %v", key, err)})
		}

		for _, ref := range refs {
			violations = append(violations, IsolationViolation{Line: ref.line, Msg: fmt.Sprintf("job %q checks out the repository and inherits secret %s through workflow env %q%s", name, ref.describe(), key, checkoutJobMayHold)})
		}
	}

	return violations
}
