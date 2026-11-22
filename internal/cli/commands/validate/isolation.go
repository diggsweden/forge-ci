// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"context"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	appvalidate "github.com/diggsweden/forge-ci/internal/app/validate"
	"github.com/diggsweden/forge-ci/internal/cli/deps"
	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"github.com/diggsweden/forge-ci/internal/runcontext"
)

// isolationCmd wires `forge-ci validate isolation` — the static
// release-isolation checks over a consumer's workflow directory.
func isolationCmd() *cli.Command {
	return &cli.Command{
		DisableSliceFlagSeparator: true,
		Name:                      "isolation",
		Usage:                     "check static release isolation over a workflow directory (signing secrets reach only the engine's signer calls; one engine, host and pin)",
		Description: `Static release-isolation checks over a repository's workflow directory.
The release workflows are found, not named: every workflow under
--workflows-dir with a job that calls the --single-pin-subject's
.forgejo/workflows/prepare-release.yml or sign-and-publish-release.yml, or
its .github/workflows/release-orchestrator.yml. None found fails.

In every workflow of the directory, no job may reach a signing secret,
directly or through the workflow env it inherits, but a call of one of the
layer's signer workflows. In the release workflows, no job may pass
secrets: inherit, every actions/checkout step must set
persist-credentials: false, every setup-toolchain step cache: false, and a
job that checks the repository out (actions/checkout, checkout-consumer,
forge-ci platform checkout, git clone/fetch/pull or a local action) may
reference no secret but the job token and read-only DEPENDENCY_*
credentials, in its steps, its env or the workflow env it inherits. A call
of sign-and-publish-release.yml must take release-tag, release-sha and
artifacts-config from a call of prepare-release.yml, and dist-digest from a
job that declares the output it names, each as the bare
${{ needs.<job>.outputs.<name> }} expression under its exact key; the
prepare call must pass every secret the sign call takes. The jobs are read
from the sign call, never named.

A local reusable workflow a release workflow calls (uses:
./.forgejo/workflows/...), directly or through another, runs in the
release: its jobs are held to the same rules, and one that is missing fails.
Every uses: in the release workflows, in those local workflows and in the
local actions they use must be a path in the repository or a reference to
the subject, and a local action must be a composite action: no third-party
action, docker:// image or Node action runs in the release.

Every reference to the subject in the workflow directory, and in the local
workflows and actions the release workflows run, is pinned to a full commit (a branch, a tag or a short commit fails), and all of them to
the same one; a reference which writes a forge address
(https://host/owner/repository/...) writes the address of the forge the job
runs on, and every call of a signer workflow names the subject. A reference
without an address is resolved by the forge and is held to the repository
alone, in any case of its owner and name. Off a runner the forge is not known: the written addresses must then
agree, and a note says the address was not compared.
--single-pin-input (forge-ci-sha by default) names the input through which the subject's reusable
workflows receive that commit a second time (the revision they check their
helper files out at): every job in the workflow directory that calls one of
them must then pass it as a literal commit, and it counts as a pin. The name
is never derived from the subject, so a renamed repository or fork cannot
switch the check off.

--literal-input names an input of the subject's reusable workflows that a
caller must write out (by default signer-image, cosign-public-key-sha256,
transparency-log and the enforce-* settings, each in its shape), as NAME or NAME=SHAPE: every job in the workflow
directory that calls one of them and passes that input must pass a literal,
never an expression such as ${{ vars.X }}, so the value changes only by a
reviewed commit. SHAPE also holds the literal to a form: sha256 (64 lowercase
hex characters), image-digest (an image reference pinned by digest), or
enum:VALUE/VALUE (one of the listed values, for example enum:none/public). A
job that does not pass the input is left to the called workflow.

EXAMPLE:
   forge-ci validate isolation --workflows-dir .forgejo/workflows \
     --single-pin-subject example/forge-ci`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "workflows-dir", Required: true, Usage: "the repository's workflow directory, two levels below its root (for example, .forgejo/workflows)"},
			&cli.StringFlag{Name: "single-pin-subject", Required: true, Usage: "owner/repository of the engine, or its HTTPS repository URL: the workflows that call its release entry points are the release workflows, and its references must agree on one commit and one forge across the workflow directory (for example, example/forge-ci)"},
			&cli.StringFlag{Name: "single-pin-input", Value: "forge-ci-sha", Usage: "input of the subject's reusable workflows that repeats their commit pin; empty checks none"},
			&cli.StringFlag{Name: "contract-root", Usage: "trusted checkout of --single-pin-subject at the caller's pin; reject undeclared action/workflow inputs against its YAML, without fetching anything"},
			&cli.StringSliceFlag{
				Name:  "literal-input",
				Usage: "input of the subject's reusable workflows that a caller must pass as a literal, as NAME or NAME=SHAPE with SHAPE sha256, image-digest or enum:VALUE/VALUE (repeatable; replaces the default list)",
				Value: defaultLiteralInputs,
			},
			&cli.StringSliceFlag{
				Name:  "signing-secret",
				Usage: "signing secret name no job but a signer workflow call may reference (repeatable; replaces the layer's own names)",
				Value: validate.DefaultSigningSecrets(),
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return appvalidate.Isolation(os.Stderr, deps.Annotator(cmd), appvalidate.IsolationInput{
				WorkflowsDir:     cmd.String("workflows-dir"),
				SinglePinSubject: cmd.String("single-pin-subject"),
				ServerURL:        runcontext.ServerURL().Resolve(os.Getenv),
				SinglePinInput:   cmd.String("single-pin-input"),
				ContractRoot:     cmd.String("contract-root"),
				LiteralInputs:    cmd.StringSlice("literal-input"),
				SigningSecrets:   signingSecretNames(cmd.StringSlice("signing-secret")),
			})
		},
	}
}

// defaultLiteralInputs are the trust inputs of the layer's workflows a caller
// writes out: the signer image runs with the signing keys, the public key
// digest binds a key read from a variable to the reviewed workflow, a
// transparency-log entry cannot be withdrawn, and an enforce-* setting from a
// variable would relax a gate without a reviewed commit.
var defaultLiteralInputs = []string{
	"signer-image=image-digest",
	"cosign-public-key-sha256=sha256",
	"transparency-log=enum:none/public",
	"enforce-lint=enum:true/false",
	"enforce-vulnerabilities=enum:true/false",
	"enforce-application-tests=enum:true/false",
	"enforce-reproducible-builds=enum:true/false",
}

// Keep the existing comma-list syntax for secret names, independently of the
// literal-input pairs whose enum values must retain commas as data.
func signingSecretNames(values []string) []string {
	names := make([]string, 0, len(values))

	for _, value := range values {
		names = append(names, strings.Split(value, ",")...)
	}

	return names
}
