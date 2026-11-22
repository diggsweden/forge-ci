// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
)

// A job that checks the repository out runs the project's code, so it may
// hold no secret but the job token, under whichever name, step or env level
// the secret goes to. The rule is the job's, not a step's: the old rule read
// the token variables of the engine's checkout and validate-tag steps only,
// and a secret one step later, under another name, passed.
func TestCheckIsolation_ACheckoutJobHoldsOnlyTheJobToken(t *testing.T) {
	t.Parallel()

	const (
		checkout = "      - uses: actions/checkout@1111111111111111111111111111111111111111\n        with:\n          persist-credentials: false\n"
		release  = "${{ secrets.RELEASE_API_TOKEN }}"
		refused  = "checks out the repository and references secret \"RELEASE_API_TOKEN\""
	)

	job := func(steps string) string { return "jobs:\n  build:\n    steps:\n" + steps }
	run := func(command, env string) string { return "      - run: " + command + "\n" + env }
	stepEnv := func(name, value string) string { return "        env:\n          " + name + ": " + value + "\n" }

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
		"a secret in a job without checkout":   {body: job(run("forge-ci release publish", stepEnv("RELEASE_TOKEN", release)))},
		"a secret in a reusable-workflow call": {body: "jobs:\n  publish:\n    uses: ./.forgejo/workflows/publish.yml\n    secrets:\n      token: " + release + "\n"},

		"a secret after the checkout":       {body: job(checkout + run("make", stepEnv("ANY_NAME", release))), want: refused},
		"a secret before the checkout":      {body: job(run("echo", stepEnv("ANY_NAME", release)) + checkout), want: refused},
		"a secret in a checkout's with":     {body: job("      - uses: actions/checkout@1111111111111111111111111111111111111111\n        with:\n          persist-credentials: false\n          token: " + release + "\n"), want: refused},
		"a secret in the job env":           {body: "jobs:\n  build:\n    env:\n      X: " + release + "\n    steps:\n" + checkout, want: refused},
		"a secret in a run line":            {body: job(checkout + run("'curl -H \"Authorization="+release+"\" x'", "")), want: refused},
		"a secret from the workflow env":    {body: "env:\n  X: " + release + "\n" + job(checkout), want: "inherits secret \"RELEASE_API_TOKEN\" through workflow env \"X\""},
		"the workflow env overridden":       {body: "env:\n  X: " + release + "\n" + "jobs:\n  build:\n    env:\n      X: ''\n    steps:\n" + checkout},
		"the engine's checkout":             {body: job(run("forge-ci platform checkout", stepEnv("RELEASE_TOKEN", release))), want: refused},
		"through the engine helper":         {body: job(run("run_forge_ci platform checkout --fetch-all-refs", stepEnv("CI_TOKEN", release))), want: refused},
		"a git clone":                       {body: job(run("git clone https://forge.example/a/b", stepEnv("T", release))), want: refused},
		"a git fetch with options":          {body: job(run("git -c x=y fetch origin", stepEnv("T", release))), want: refused},
		"a checkout-consumer step":          {body: job("      - uses: https://forge.example/team/engine/.forgejo/actions/checkout-consumer@1111111111111111111111111111111111111111\n" + run("make", stepEnv("T", release))), want: refused},
		"a local action":                    {body: job("      - uses: ./.forgejo/actions/build\n" + run("make", stepEnv("T", release))), want: refused},
		"a secret in a local action's with": {body: job("      - uses: ./.forgejo/actions/build\n        with:\n          token: " + release + "\n"), want: refused},
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

			if len(violations) != 1 || !strings.Contains(violations[0].Msg, tc.want) || !strings.Contains(violations[0].Msg, "may hold only the job token") {
				t.Fatalf("violations = %+v, want one naming %q", violations, tc.want)
			}
		})
	}

	// A signing secret in a checkout job is the signing-secret rule's, and
	// is reported once, by it.
	t.Run("a signing secret is reported once", func(t *testing.T) {
		t.Parallel()

		violations, err := validate.CheckIsolation([]byte(job(checkout+run("make", stepEnv("K", "${{ secrets.COSIGN_PRIVATE_KEY }}")))),
			validate.IsolationConfig{SigningSecrets: []string{"COSIGN_PRIVATE_KEY"}})
		if err != nil || len(violations) != 1 || !strings.Contains(violations[0].Msg, "references signing secret") {
			t.Fatalf("violations = %+v, err = %v", violations, err)
		}
	})
}
