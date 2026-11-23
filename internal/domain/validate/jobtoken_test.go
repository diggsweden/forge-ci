// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
)

// A release job that runs anything but the layer's actions may hold no
// secret but the job token and read-only dependency credentials, under
// whichever name, step or env level the secret goes to. The rule reads the
// job's shape, not its commands: the rule it replaces looked for checkout
// commands, and an archive download, a submodule update, the engine binary
// under another name or a downloaded artifact passed with a publish token.
func TestCheckIsolation_ASecretJobRunsOnlyTheLayersActions(t *testing.T) {
	t.Parallel()

	const (
		checkout = "      - uses: actions/checkout@1111111111111111111111111111111111111111\n        with:\n          persist-credentials: false\n"
		engine   = "      - uses: https://forge.example/team/engine/.forgejo/actions/registry-login@1111111111111111111111111111111111111111\n"
		layer    = "      - uses: https://forge.example/team/engine/.forgejo/actions/"
		pin      = "@1111111111111111111111111111111111111111\n"
		release  = "${{ secrets.RELEASE_API_TOKEN }}"
		refused  = "references secret \"RELEASE_API_TOKEN\" and runs "
	)

	job := func(steps string) string { return "jobs:\n  build:\n    steps:\n" + steps }
	run := func(command, env string) string { return "      - run: " + command + "\n" + env }
	stepEnv := func(name, value string) string { return "        env:\n          " + name + ": " + value + "\n" }
	withToken := func(step string) string { return step + "        with:\n          token: " + release + "\n" }

	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"a checkout with no secret":            {body: job(checkout + run("make", ""))},
		"the job token by forgejo":             {body: job(checkout + run("make", stepEnv("TOKEN", "${{ forgejo.token }}")))},
		"the reserved job token secret":        {body: job(checkout + run("make", stepEnv("TOKEN", "${{ secrets.GITHUB_TOKEN }}")))},
		"the forgejo job token secret":         {body: job(checkout + run("make", stepEnv("TOKEN", "${{ secrets.FORGEJO_TOKEN }}")))},
		"the job token secret in any case":     {body: job(checkout + run("make", stepEnv("TOKEN", "${{ secrets.gitea_token }}")))},
		"a dependency token":                   {body: job(checkout + run("make", stepEnv("GH", "${{ secrets.DEPENDENCY_GITHUB_TOKEN }}")))},
		"a dependency token in any case":       {body: job(checkout + run("make", stepEnv("GH", "${{ secrets.dependency_token }}")))},
		"a name that only contains it":         {body: job(checkout + run("make", stepEnv("GH", "${{ secrets.MY_DEPENDENCY_TOKEN }}"))), want: "references secret \"MY_DEPENDENCY_TOKEN\""},
		"the layer's actions alone":            {body: job(engine + withToken(engine))},
		"a secret in the job env of actions":   {body: "jobs:\n  build:\n    env:\n      X: " + release + "\n    steps:\n" + engine},
		"a secret in a reusable-workflow call": {body: "jobs:\n  publish:\n    uses: ./.forgejo/workflows/publish.yml\n    secrets:\n      token: " + release + "\n"},

		"a secret after the checkout":       {body: job(checkout + run("make", stepEnv("ANY_NAME", release))), want: refused + "the action actions/checkout@"},
		"a secret before the checkout":      {body: job(run("echo", stepEnv("ANY_NAME", release)) + checkout), want: refused + "a run: step"},
		"a secret in a checkout's with":     {body: job(withToken("      - uses: actions/checkout@1111111111111111111111111111111111111111\n") + "          persist-credentials: false\n"), want: refused + "the action actions/checkout@"},
		"a secret in the job env":           {body: "jobs:\n  build:\n    env:\n      X: " + release + "\n    steps:\n" + engine + checkout, want: refused + "the action actions/checkout@"},
		"a secret in a run line":            {body: job(run("'curl -H \"Authorization="+release+"\" x'", "")), want: refused + "a run: step"},
		"a secret from the workflow env":    {body: "env:\n  X: " + release + "\n" + job(run("make", "")), want: "inherits secret \"RELEASE_API_TOKEN\" through workflow env \"X\" and runs a run: step"},
		"the workflow env overridden":       {body: "env:\n  X: " + release + "\n" + "jobs:\n  build:\n    env:\n      X: ''\n    steps:\n" + run("make", "")},
		"a run step without a checkout":     {body: job(engine + run("forge-ci release publish", stepEnv("RELEASE_TOKEN", release))), want: refused + "a run: step"},
		"an archive download":               {body: job(run("curl -fsSL \"$SERVER/archive/main.tar.gz\" | tar xz", stepEnv("T", release))), want: refused},
		"the engine binary by its variable": {body: job(run("'\"$FORGE_CI_BIN\" platform checkout'", stepEnv("T", release))), want: refused},
		"a submodule update":                {body: job(run("git submodule update --init", stepEnv("T", release))), want: refused},
		"a downloaded artifact":             {body: job(engine + run("./dist/publish", stepEnv("T", release))), want: refused},
		"a checkout-consumer step":          {body: job(withToken("      - uses: https://forge.example/team/engine/.forgejo/actions/checkout-consumer@1111111111111111111111111111111111111111\n")), want: refused + "the layer's checkout-consumer, which runs the project's code"},
		"a local action":                    {body: job(withToken("      - uses: ./.forgejo/actions/build\n")), want: refused + "the local action ./.forgejo/actions/build"},
		"a step without uses or run":        {body: job("      - name: nothing\n        env:\n          T: " + release + "\n"), want: refused + "a step without uses:"},

		"a layer action that builds":            {body: job(withToken(layer + "build-go" + pin)), want: refused + "the layer's build-go, which runs the project's code"},
		"a layer action the list does not name": {body: job(withToken(layer + "new-action" + pin)), want: refused + "the layer's new-action"},
		"a third-party action":                  {body: job(withToken("      - uses: https://code.forgejo.org/actions/cache" + pin)), want: refused + "the action https://code.forgejo.org/actions/cache@"},
		"an action nested under the directory":  {body: job(withToken(layer + "build-go/sub" + pin)), want: "build-go/sub@1111111111111111111111111111111111111111, which is not the layer's"},
		"a Maven publish by default":            {body: job(withToken(layer + "publish-forge-packages" + pin)), want: refused + "publish-forge-packages with mvn deploy"},
		"a Maven publish from a computed type":  {body: job(layer + "publish-forge-packages" + pin + "        with:\n          project-type: ${{ vars.TYPE }}\n          token: " + release + "\n"), want: refused + "publish-forge-packages with mvn deploy"},
		"a Maven publish of the build":          {body: job(layer + "publish-forge-packages" + pin + "        with:\n          from-build: true\n          token: " + release + "\n")},
		"an npm publish":                        {body: job(layer + "publish-forge-packages" + pin + "        with:\n          project-type: npm\n          token: " + release + "\n")},
		"a Gradle publish":                      {body: job(layer + "publish-forge-packages" + pin + "        with:\n          project-type: gradle\n          token: " + release + "\n")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			violations, err := validate.CheckIsolation([]byte(tc.body), validate.IsolationConfig{SigningSecrets: []string{"COSIGN_PRIVATE_KEY"}})
			if err != nil {
				t.Fatal(err)
			}

			if tc.want == "" {
				if len(violations) != 0 {
					t.Fatalf("violations = %+v, want none", violations)
				}

				return
			}

			if len(violations) != 1 || !strings.Contains(violations[0].Msg, tc.want) || !strings.Contains(violations[0].Msg, "may run only the layer's actions") {
				t.Fatalf("violations = %+v, want one naming %q", violations, tc.want)
			}
		})
	}

	// A signing secret in such a job is the signing-secret rule's, and
	// is reported once, by it.
	t.Run("a signing secret is reported once", func(t *testing.T) {
		t.Parallel()

		violations, err := validate.CheckIsolation([]byte(job(checkout+run("make", stepEnv("K", "${{ secrets.COSIGN_PRIVATE_KEY }}")))),
			validate.IsolationConfig{SigningSecrets: []string{"COSIGN_PRIVATE_KEY"}})
		if err != nil || len(violations) != 1 || !strings.Contains(violations[0].Msg, "references signing secret") {
			t.Fatalf("violations = %+v, err = %v", violations, err)
		}
	})

	t.Run("a signing secret in another case is refused", func(t *testing.T) {
		t.Parallel()

		violations, err := validate.CheckIsolation([]byte(job(checkout+run("make", stepEnv("K", "${{ secrets.cosign_private_key }}")))),
			validate.IsolationConfig{SigningSecrets: []string{"COSIGN_PRIVATE_KEY"}})
		if err != nil || len(violations) != 1 || !strings.Contains(violations[0].Msg, "references signing secret") {
			t.Fatalf("violations = %+v, err = %v", violations, err)
		}
	})

	t.Run("a local call may not rename a write token as read-only", func(t *testing.T) {
		t.Parallel()

		body := "jobs:\n  publish:\n    uses: ./.forgejo/workflows/publish.yml\n    secrets:\n      DEPENDENCY_X: " + release + "\n"

		violations, err := validate.CheckIsolation([]byte(body), validate.IsolationConfig{})
		if err != nil || len(violations) != 1 || !strings.Contains(violations[0].Msg, "read-only name") {
			t.Fatalf("violations = %+v, err = %v", violations, err)
		}
	})

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "job env", body: "jobs:\n  build:\n    env:\n      BASH_ENV: x\n    steps:\n" + withToken(engine), want: "BASH_ENV"},
		{name: "step env", body: job(withToken(engine) + stepEnvAfterWith("LD_PRELOAD")), want: "LD_PRELOAD"},
		{name: "workflow env", body: "env:\n  PATH: /x\n" + job(withToken(engine)), want: "PATH"},
		{name: "no secret held", body: "jobs:\n  build:\n    env:\n      BASH_ENV: x\n    steps:\n" + engine},
		{name: "an ordinary variable", body: "jobs:\n  build:\n    env:\n      MODE: x\n    steps:\n" + withToken(engine)},
	} {
		t.Run("startup variable: "+tc.name, func(t *testing.T) {
			t.Parallel()

			violations, err := validate.CheckIsolation([]byte(tc.body), validate.IsolationConfig{})
			if err != nil {
				t.Fatal(err)
			}

			if tc.want == "" {
				if len(violations) != 0 {
					t.Fatalf("violations = %+v, want none", violations)
				}

				return
			}

			if len(violations) != 1 || !strings.Contains(violations[0].Msg, "sets "+tc.want) {
				t.Fatalf("violations = %+v, want one naming %s", violations, tc.want)
			}
		})
	}
}

// stepEnvAfterWith is an env block for the step written just before it.
func stepEnvAfterWith(name string) string {
	return "        env:\n          " + name + ": x\n"
}
