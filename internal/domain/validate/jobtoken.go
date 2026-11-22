// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
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

// engineOnlySteps returns the first step of a job of steps that is not one
// of the layer's actions that run only the engine (layerActions): a run:
// step, a local action, another action, or a layer action that runs the
// project's code or the caller's commands. The secret-holding job of a
// release may run nothing else. Whether every remote action is the layer's
// at its pin is the caller's check of every uses: of the release; this one
// reads the job's shape.
func engineOnlySteps(job yaml.Node) (yaml.Node, string, bool) {
	for _, step := range jobSteps(job) {
		uses := scalarValue(mappingValue(&step, "uses"))
		name, layer := layerActionName(uses)
		with := mappingValue(&step, "with")

		switch {
		case mappingValue(&step, "run") != nil:
			return step, "a run: step", false
		case uses == "":
			return step, "a step without uses:", false
		case strings.HasPrefix(uses, "./"):
			return step, "the local action " + uses, false
		case !layer:
			return step, "the action " + uses + ", which is not the layer's", false
		case !layerActions[name]:
			return step, "the layer's " + name + ", which runs the project's code or the caller's commands", false
		case name == "publish-forge-packages" && runsMavenDeploy(scalarValue(mappingValue(with, "project-type")), scalarValue(mappingValue(with, "from-build"))):
			return step, "publish-forge-packages with mvn deploy, which runs the project's build (publish the build's hand-off with from-build: true)", false
		}
	}

	return yaml.Node{}, "", true
}

// jobTokenViolations refuses every secret but the job token and the
// read-only dependency credentials in a job of a release workflow that runs
// anything but the layer's actions that run the engine alone. A run: step, a
// local action, a checkout or a layer action that builds, lints or sets up
// the project's tools runs code the release does not pin, or the project's
// code, and it reads whatever the job holds, whichever step,
// env name, file or $FORGEJO_ENV write the secret went to. Telling a
// checkout from any other way of fetching code (an archive download, a
// submodule, a downloaded artifact) is not possible from the text, so the
// job's shape decides. A release, registry, API or dependency token belongs
// in a job of the layer's actions alone, or in a call of one of its
// workflows. The workflow env a job inherits is read as well, unless the job
// overrides the name. A signing secret and the whole secrets context are
// left to the signing-secret rule, which refuses them in every such job
// already.
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

		// An input of a local workflow reaches every step there under
		// another name, where the called job's own check cannot see a secret.
		if strings.HasPrefix(scalarValue(mappingValue(&job, "uses")), "./") {
			refs, err := findSecretRefsMatching(mappingValue(&job, "with"), isOtherToken)
			if err != nil {
				violations = append(violations, IsolationViolation{Line: job.Line, Msg: fmt.Sprintf("job %q: %v", name, err)})
			}

			for _, ref := range refs {
				violations = append(violations, IsolationViolation{Line: ref.line, Msg: fmt.Sprintf("job %q passes secret %s to a local workflow as an input%s", name, ref.describe(), secretJobMayRun)})
			}
		}

		step, runs, ok := engineOnlySteps(job)
		if ok {
			continue
		}

		why := fmt.Sprintf(" and runs %s (line %d)", runs, step.Line)

		refs, err := findSecretRefsMatching(&job, isOtherToken)
		if err != nil {
			violations = append(violations, IsolationViolation{Line: job.Line, Msg: fmt.Sprintf("job %q: %v", name, err)})

			continue
		}

		for _, ref := range refs {
			violations = append(violations, IsolationViolation{Line: ref.line, Msg: fmt.Sprintf("job %q references secret %s%s%s", name, ref.describe(), why, secretJobMayRun)})
		}

		violations = append(violations, inheritedTokenViolations(workflowEnv, name, &job, why, isOtherToken)...)
	}

	return violations
}

// secretJobMayRun ends each diagnostic of a secret-holding job with what it may run.
var secretJobMayRun = "; a release job that holds a secret but the job token (${{ forgejo.token }}, ${{ github.token }} or secrets.GITHUB_TOKEN) and read-only DEPENDENCY_* credentials may run only the layer's actions that run the engine alone (" + strings.Join(PrivilegedActions(), ", ") + "), with no run: step, local action, checkout or other action, so move the secret to such a job or to a call of one of the layer's workflows"

// inheritedTokenViolations reports a secret in the workflow env that a job
// which runs more than the layer's actions inherits without overriding the
// name.
func inheritedTokenViolations(workflowEnv yaml.Node, name string, job *yaml.Node, why string, isOtherToken func(string) bool) []IsolationViolation {
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
			violations = append(violations, IsolationViolation{Line: ref.line, Msg: fmt.Sprintf("job %q inherits secret %s through workflow env %q%s%s", name, ref.describe(), key, why, secretJobMayRun)})
		}
	}

	return violations
}
