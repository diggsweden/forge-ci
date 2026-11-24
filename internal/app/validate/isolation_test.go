// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strings"
	"testing"

	appvalidate "github.com/diggsweden/forge-ci/internal/app/validate"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/output"
	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"github.com/diggsweden/forge-ci/internal/testutil/testfs"
)

// isolationPin is the one commit the fixtures pin the subject to.
const isolationPin = "1111111111111111111111111111111111111111"

// isolationEntry is a call of the subject's prepare-release.yml: the job that
// makes a workflow a release workflow. A fixture of the generic rules ends
// with it so that the gate finds the workflow.
const isolationEntry = `  prepare:
    uses: examplescope/ci-layer/.forgejo/workflows/prepare-release.yml@` + isolationPin + `
`

const isolationCleanWorkflow = `name: release
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: examplescope/ci-layer/.forgejo/actions/checkout-consumer@1111111111111111111111111111111111111111
      - run: make dist
  sign:
    needs: build
    # The layer's signer workflow, the one kind of job that may receive a
    # key. A local one could be changed by the pull request.
    uses: examplescope/ci-layer/.forgejo/workflows/sign-container-images.yml@1111111111111111111111111111111111111111
    secrets:
      RELEASE_GPG_PRIVATE_KEY: ${{ secrets.RELEASE_GPG_PRIVATE_KEY }}
` + isolationEntry

// foreignCheckout is the violation every fixture that uses actions/checkout
// also has: it is pinned to a tag, not a full commit.
const foreignCheckout = "actions/checkout@v5 is neither a path in this repository, nor examplescope/ci-layer"

func runIsolation(t *testing.T, body string) (string, error) {
	t.Helper()

	mem := testfs.NewMemory(t)
	mem.WriteFile(".forgejo/workflows/release.yml", []byte(body))

	var out bytes.Buffer

	err := appvalidate.Isolation(&out, output.NewAnnotator(&out, output.FormatGitHub), appvalidate.IsolationInput{
		WorkflowsDir:     ".forgejo/workflows",
		SinglePinSubject: "examplescope/ci-layer",
		SigningSecrets:   []string{"RELEASE_GPG_PRIVATE_KEY", "COSIGN_PRIVATE_KEY"},
		FS:               mem.FS(),
	})

	return out.String(), err
}

// forgejoIsolation runs the forgejo-shaped gate over the workflow directory
// of mem.
func forgejoIsolation(t *testing.T, mem *testfs.Memory, dir, subject, pinInput string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	err := appvalidate.Isolation(&out, output.NewAnnotator(&out, output.FormatGitHub), appvalidate.IsolationInput{
		WorkflowsDir:     dir,
		SigningSecrets:   []string{"COSIGN_SIGNING_KEY", "GPG_SIGNING_KEY"},
		SinglePinSubject: subject,
		SinglePinInput:   pinInput,
		FS:               mem.FS(),
	})

	return out.String(), err
}

func runForgejoIsolation(t *testing.T, body string) (string, error) {
	t.Helper()

	mem := testfs.NewMemory(t)
	mem.WriteFile(".forgejo/workflows/release.yml", []byte(body))

	return forgejoIsolation(t, mem, ".forgejo/workflows", "examplescope/ci-layer", "")
}

func runForgejoIsolationInFS(t *testing.T, mem *testfs.Memory) (string, error) {
	t.Helper()

	return forgejoIsolation(t, mem, ".forgejo/workflows", "examplescope/ci-layer", "")
}

const forgejoIsolationCleanWorkflow = `jobs:
  prepare:
    uses: examplescope/ci-layer/.forgejo/workflows/prepare-release.yml@1111111111111111111111111111111111111111
    secrets:
      COSIGN_SIGNING_KEY: ${{ secrets.COSIGN_SIGNING_KEY }}
      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}
  build-and-release:
    outputs:
      dist-digest: ${{ steps.handoff.outputs.digest }}
    steps: []
  sign-and-publish:
    uses: examplescope/ci-layer/.forgejo/workflows/sign-and-publish-release.yml@1111111111111111111111111111111111111111
    with:
      release-tag: ${{ needs.prepare.outputs.release-tag }}
      release-sha: ${{ needs.prepare.outputs.release-sha }}
      artifacts-config: ${{ needs.prepare.outputs.artifacts-config }}
      dist-digest: ${{ needs.build-and-release.outputs.dist-digest }}
    secrets:
      COSIGN_SIGNING_KEY: ${{ secrets.COSIGN_SIGNING_KEY }}
      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}
`

func TestIsolation_CleanWorkflowPasses(t *testing.T) {
	out, err := runIsolation(t, isolationCleanWorkflow)
	if err != nil {
		t.Fatalf("Isolation: %v", err)
	}

	if !strings.Contains(out, "Static release-isolation checks passed") {
		t.Errorf("missing success line in:\n%s", out)
	}
}

func TestIsolation_JobLeakingSecretFails(t *testing.T) {
	body := `name: release
jobs:
  build:
    runs-on: ubuntu-24.04
    env:
      KEY: ${{ secrets.COSIGN_PRIVATE_KEY }}
    steps:
      - run: make dist
` + isolationEntry

	out, err := runIsolation(t, body)
	if !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}

	if !strings.Contains(out, "COSIGN_PRIVATE_KEY") || !strings.Contains(out, "never a job that runs the project's code") {
		t.Errorf("missing job-secret annotation in:\n%s", out)
	}
}

func TestIsolation_CheckoutPersistingCredentialsFails(t *testing.T) {
	tests := map[string]string{
		"missing flag": `name: release
jobs:
  build:
    steps:
      - uses: actions/checkout@v5
` + isolationEntry,
		"explicit true": `name: release
jobs:
  build:
    steps:
      - uses: actions/checkout@v5
        with:
          persist-credentials: true
` + isolationEntry,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := runIsolation(t, body)
			if !errors.Is(err, errs.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}

			if !strings.Contains(out, "persist-credentials: false") {
				t.Errorf("missing checkout annotation in:\n%s", out)
			}
		})
	}
}

// Each fixture also runs actions/checkout, which a release refuses as a
// foreign action; the persist-credentials and secret refusals stand beside
// that one.
func TestIsolation_CheckoutExpressionsAndSecrets(t *testing.T) {
	checkout := `name: release
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v5
        with:
          persist-credentials: false
` + isolationEntry

	for _, tc := range []struct {
		name       string
		value      string
		wantErrors int
	}{
		{"clean_literal_false", "false", 0},
		{"clean_quoted_false", "'false'", 0},
		{"true_expression", "${{ true }}", 1},
		{"false_expression", "${{ false }}", 1},
		{"secret_expression", "${{ secrets.COSIGN_PRIVATE_KEY }}", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(checkout, "persist-credentials: false", "persist-credentials: "+tc.value, 1)

			out, err := runIsolation(t, body)
			if !errors.Is(err, errs.ErrValidation) || strings.Contains(out, "Static release-isolation checks passed") ||
				strings.Count(out, "::error ") != tc.wantErrors+1 || !strings.Contains(out, foreignCheckout) {
				t.Fatalf("checkout refusal lost or masked by another rule: %s %v", out, err)
			}

			if tc.wantErrors == 0 {
				return
			}

			if !strings.Contains(out, `line=6::job "build": actions/checkout step must set persist-credentials: false`) {
				t.Fatalf("missing checkout annotation: %s", out)
			}

			if tc.wantErrors == 2 && !strings.Contains(out, `line=8::job "build" references signing secret "COSIGN_PRIVATE_KEY"`) {
				t.Fatalf("checkout check suppressed the independent secret diagnostic: %s", out)
			}
		})
	}
}

// TestIsolation_ReportsEveryViolatingJob covers the scan across jobs.
// Every fixture here has a single job with a single problem, so a check
// that reported the first violation and stopped, or that only inspected
// the first job, would satisfy all of them -- while a workflow whose
// second job persists checkout credentials passed as isolated.
//
// This is a release-isolation invariant: a credential left on disk in any job
// is reachable by anything that job runs, not only by the first one.
func TestIsolation_ReportsEveryViolatingJob(t *testing.T) {
	body := `name: release
jobs:
  lint:
    steps:
      - uses: actions/checkout@v5
        with:
          persist-credentials: false
  build:
    steps:
      - uses: actions/checkout@v5
        with:
          persist-credentials: false
  test:
    steps:
      - uses: actions/checkout@v5
  docs:
    steps:
      - uses: actions/checkout@v5
        with:
          persist-credentials: true
` + isolationEntry

	out, err := runIsolation(t, body)
	if !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}

	// Two offending jobs, both annotated. Counting the annotations is
	// what distinguishes "found them all" from "found one and stopped".
	if got := strings.Count(out, "persist-credentials: false"); got != 2 {
		t.Errorf("annotated %d checkout violations, want 2:\n%s", got, out)
	}

	// Each on its own line, so the annotations point at the right steps
	// rather than all at the first.
	lines := map[string]bool{}

	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "persist-credentials: false") {
			lines[line] = true
		}
	}

	if len(lines) != 2 {
		t.Errorf("both violations annotated at the same location:\n%s", out)
	}
}

func TestIsolation_CheckoutConsumerIsExempt(t *testing.T) {
	body := `name: release
jobs:
  build:
    steps:
      - uses: https://forge.example/examplescope/ci-layer/actions/checkout-consumer@1111111111111111111111111111111111111111
` + isolationEntry

	out, err := runIsolation(t, body)
	if err != nil {
		t.Fatalf("checkout-consumer should be exempt: %v\n%s", err, out)
	}
}

func TestIsolation_ForgejoStrictWorkflowPasses(t *testing.T) {
	out, err := runForgejoIsolation(t, forgejoIsolationCleanWorkflow)
	if err != nil {
		t.Fatalf("Isolation: %v\n%s", err, out)
	}

	if !strings.Contains(out, "Static release-isolation checks passed") {
		t.Errorf("missing success line in:\n%s", out)
	}
}

// composedPrepare is forgejoIsolationCleanWorkflow with its called prepare
// job replaced by one built from steps.
func composedPrepare(steps string) string {
	called := forgejoIsolationCleanWorkflow[strings.Index(forgejoIsolationCleanWorkflow, "  prepare:\n"):strings.Index(forgejoIsolationCleanWorkflow, "  build-and-release:\n")]

	return strings.Replace(forgejoIsolationCleanWorkflow, called,
		"  prepare:\n    outputs:\n      release-tag: v1\n      release-sha: abc\n      artifacts-config: ''\n    steps:\n"+steps, 1)
}

func TestIsolation_ForgejoStrictViolations(t *testing.T) {
	tests := map[string]struct {
		body string
		want []string
	}{
		// A prepare job of the caller's own steps is not a prepare call: it
		// chooses the release identity and may not see a key.
		"composed_prepare": {composedPrepare(`      - name: Check secrets before checkout
        env:
          KEY: ${{ secrets.GPG_SIGNING_KEY }}
      - run: "true"
`), []string{"must take release-tag from a job that calls the layer's prepare-release.yml", `job "prepare" references signing secret "GPG_SIGNING_KEY"`}},
		"declaration_with_a_fallback": {strings.Replace(forgejoIsolationCleanWorkflow,
			"artifacts-config: ${{ needs.prepare.outputs.artifacts-config }}",
			"artifacts-config: ${{ needs.prepare.outputs.artifacts-config || '.forge-ci/evil.yml' }}", 1), []string{"must pass artifacts-config"}},
		"missing_prepare_secret": {strings.Replace(forgejoIsolationCleanWorkflow,
			`      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}
  build-and-release:`, "  build-and-release:", 1), []string{`prepare job "prepare" call site is missing secret GPG_SIGNING_KEY`}},
		"broken_dist_digest": {strings.Replace(forgejoIsolationCleanWorkflow,
			"dist-digest: ${{ needs.build-and-release.outputs.dist-digest }}",
			"dist-digest: ${{ steps.local.outputs.digest }}", 1), []string{"must pass dist-digest"}},
		"cached_toolchain": {strings.Replace(forgejoIsolationCleanWorkflow,
			"steps: []",
			`steps:
      - uses: https://forge.example/examplescope/ci-layer/actions/setup-toolchain@1111111111111111111111111111111111111111`, 1), []string{"cache: false"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := runForgejoIsolation(t, tc.body)
			if !errors.Is(err, errs.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation\n%s", err, out)
			}

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
		})
	}
}

// uncalledSigner is a reusable workflow that takes a signing key, as the
// layer's signer workflows do in the layer's own repository.
const uncalledSigner = "on:\n  workflow_call:\njobs:\n  sign:\n    steps:\n      - run: sign\n        env:\n          KEY: ${{ secrets.GPG_SIGNING_KEY }}\n"

// The release workflows are found by what they call, whatever they are
// named, and each one is checked; a directory without one is refused rather
// than passed over.
func TestIsolation_FindsTheReleaseWorkflows(t *testing.T) {
	t.Parallel()

	leaky := strings.Replace(forgejoIsolationCleanWorkflow, "    steps: []\n",
		"    env:\n      KEY: ${{ secrets.COSIGN_SIGNING_KEY }}\n    steps: []\n", 1)

	_, signJobs, found := strings.Cut(forgejoIsolationCleanWorkflow, "  build-and-release:\n")
	if !found {
		t.Fatal("the clean workflow has no build-and-release job")
	}

	signOnly := "  build-and-release:\n" + signJobs

	for name, test := range map[string]struct {
		files map[string]string
		want  []string
		found []string
	}{
		"a release workflow named release.yml": {
			files: map[string]string{"release.yml": forgejoIsolationCleanWorkflow},
			found: []string{".forgejo/workflows/release.yml"},
		},
		"a release workflow under any name": {
			files: map[string]string{"ship-it.yaml": forgejoIsolationCleanWorkflow, "pullrequest.yml": "jobs:\n  test:\n    steps:\n      - run: make test\n"},
			found: []string{".forgejo/workflows/ship-it.yaml"},
		},
		"each of two release workflows is checked": {
			files: map[string]string{"release.yml": forgejoIsolationCleanWorkflow, "hotfix.yml": leaky},
			want:  []string{`file=.forgejo/workflows/hotfix.yml,line=`, `job "build-and-release" references signing secret "COSIGN_SIGNING_KEY"`},
		},
		"a workflow that only signs is a release workflow": {
			files: map[string]string{"release.yml": "jobs:\n" + signOnly},
			want:  []string{"must take release-tag from a job that calls the layer's prepare-release.yml"},
		},
		"no release workflow": {
			files: map[string]string{"pullrequest.yml": "jobs:\n  lint:\n    uses: examplescope/ci-layer/.forgejo/workflows/lint.yml@" + isolationPin + "\n"},
			want:  []string{"no workflow under .forgejo/workflows calls examplescope/ci-layer's .forgejo/workflows/prepare-release.yml, .forgejo/workflows/sign-and-publish-release.yml, .forgejo/workflows/sign-container-images.yml"},
		},
		// fxD of the second review: a call of another signer workflow makes
		// a release workflow, and its other jobs are held to the release
		// rules.
		"a base-image lifecycle call is a release workflow": {
			files: map[string]string{
				"release.yml": forgejoIsolationCleanWorkflow,
				"bases.yml": "on:\n  push:\n    branches: [main]\njobs:\n  build:\n    steps:\n" +
					"      - uses: examplescope/ci-layer/.forgejo/actions/checkout-consumer@" + isolationPin + "\n" +
					"      - run: make\n        env:\n          T: ${{ secrets.PACKAGE_PUBLISH_TOKEN }}\n" +
					"  bases:\n    uses: examplescope/ci-layer/.forgejo/workflows/base-image-lifecycle.yml@" + isolationPin + "\n    secrets: inherit\n",
			},
			want: []string{
				`file=.forgejo/workflows/bases.yml,line=10::job "build" references secret "PACKAGE_PUBLISH_TOKEN" and runs the layer's checkout-consumer`,
				`file=.forgejo/workflows/bases.yml,line=12::job "bases" inherits every caller secret`,
			},
		},
		"another repository's entry point does not make a release workflow": {
			files: map[string]string{"release.yml": strings.ReplaceAll(forgejoIsolationCleanWorkflow, "examplescope/ci-layer", "other/ci-layer")},
			want:  []string{"no workflow under .forgejo/workflows calls examplescope/ci-layer's"},
		},
		// The layer's own reusable workflows in a fork that releases
		// itself: they run only when called, and nothing here calls them, so
		// this repository's keys never reach them.
		"a reusable workflow nothing here calls": {
			files: map[string]string{"release.yml": forgejoIsolationCleanWorkflow, "signer.yml": uncalledSigner},
			found: []string{".forgejo/workflows/release.yml"},
		},
		"a reusable workflow a local job calls": {
			files: map[string]string{
				"release.yml":     forgejoIsolationCleanWorkflow,
				"signer.yml":      uncalledSigner,
				"pullrequest.yml": "on: pull_request\njobs:\n  call:\n    uses: ./.forgejo/workflows/signer.yml\n",
			},
			want: []string{`file=.forgejo/workflows/signer.yml,line=8::job "sign" references signing secret "GPG_SIGNING_KEY"`},
		},
		"a reusable workflow called by its full reference": {
			files: map[string]string{
				"release.yml":     forgejoIsolationCleanWorkflow,
				"signer.yml":      uncalledSigner,
				"pullrequest.yml": "on: pull_request\njobs:\n  call:\n    uses: acme/app/.forgejo/workflows/signer.yml@" + isolationPin + "\n    secrets: inherit\n",
			},
			want: []string{`file=.forgejo/workflows/signer.yml,line=8::job "sign" references signing secret "GPG_SIGNING_KEY"`},
		},
		"a reusable workflow with a second trigger": {
			files: map[string]string{
				"release.yml": forgejoIsolationCleanWorkflow,
				"signer.yml":  strings.Replace(uncalledSigner, "on:\n  workflow_call:\n", "on: [workflow_call, push]\n", 1),
			},
			want: []string{`file=.forgejo/workflows/signer.yml,line=7::job "sign" references signing secret "GPG_SIGNING_KEY"`},
		},
		// A key in a workflow that is no release workflow is held to the same
		// boundary: only the engine's signers receive one.
		"a signing secret in a workflow that does not release": {
			files: map[string]string{
				"release.yml": forgejoIsolationCleanWorkflow,
				"docs.yml":    "jobs:\n  pages:\n    steps:\n      - run: deploy\n        env:\n          KEY: ${{ secrets.GPG_SIGNING_KEY }}\n",
			},
			want: []string{`file=.forgejo/workflows/docs.yml,line=6::job "pages" references signing secret "GPG_SIGNING_KEY"`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mem := testfs.NewMemory(t)
			for file, body := range test.files {
				mem.WriteFile(".forgejo/workflows/"+file, []byte(body))
			}

			out, err := runForgejoIsolationInFS(t, mem)
			if len(test.want) == 0 {
				if err != nil {
					t.Fatalf("refused: %v\n%s", err, out)
				}

				for _, file := range test.found {
					if !strings.Contains(out, "Release workflow "+file+", which calls examplescope/ci-layer, checked") {
						t.Errorf("release workflow %s not reported checked:\n%s", file, out)
					}
				}

				return
			}

			if !errors.Is(err, errs.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation\n%s", err, out)
			}

			for _, want := range test.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
		})
	}
}

// A call of any signer workflow of the layer, the workflows that take signing
// secrets, makes a release workflow: the list is the one the signing-secret
// rule admits, never a second copy of it.
func TestIsolation_EverySignerWorkflowMakesARelease(t *testing.T) {
	t.Parallel()

	for _, workflow := range validate.SignerWorkflows() {
		t.Run(workflow, func(t *testing.T) {
			t.Parallel()

			dir := path.Dir(workflow)
			mem := testfs.NewMemory(t)
			mem.WriteFile(dir+"/images.yml", []byte("jobs:\n  images:\n    uses: examplescope/ci-layer/"+workflow+"@"+isolationPin+
				"\n    secrets: inherit\n"))

			out, err := forgejoIsolation(t, mem, dir, "examplescope/ci-layer", "")
			if !errors.Is(err, errs.ErrValidation) || !strings.Contains(out, `job "images" inherits every caller secret`) {
				t.Fatalf("err = %v, want the release rules applied\n%s", err, out)
			}
		})
	}
}

// The subject and the directory are required: without them nothing finds
// the release workflows.
func TestIsolation_SubjectAndDirectoryAreRequired(t *testing.T) {
	t.Parallel()

	mem := testfs.NewMemory(t)
	mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow))

	if out, err := forgejoIsolation(t, mem, ".forgejo/workflows", "", ""); !errors.Is(err, errs.ErrUsage) {
		t.Errorf("no subject: err = %v, want ErrUsage\n%s", err, out)
	}

	if out, err := forgejoIsolation(t, mem, "", "examplescope/ci-layer", ""); !errors.Is(err, errs.ErrUsage) {
		t.Errorf("no directory: err = %v, want ErrUsage\n%s", err, out)
	}

	if out, err := forgejoIsolation(t, mem, ".gitea/workflows", "examplescope/ci-layer", ""); !errors.Is(err, errs.ErrMissingInput) {
		t.Errorf("missing directory: err = %v, want ErrMissingInput\n%s", err, out)
	}
}

func TestIsolation_UnmatchedSinglePinSubjectFails(t *testing.T) {
	// A subject that matches nothing used to pass: the pin set was empty, empty
	// is not "more than one", and the gate reported success. A stale or
	// misspelled subject therefore switched this check off while still printing
	// a tick.
	mem := testfs.NewMemory(t)
	mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow))

	out, err := runIsolationForSubject(t, mem, "examplescope/nowhere-ci", "")
	if !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation\n%s", err, out)
	}

	if !strings.Contains(out, "matches nothing") {
		t.Errorf("missing unmatched-subject violation in:\n%s", out)
	}
}

// otherSubjectWorkflow is forgejoIsolationCleanWorkflow with its calls pinned
// to a subject whose repository is not named after the pin input, as a
// renamed fork or a lab snapshot is. The %[1]s is the extra input of the
// prepare and the sign job.
const otherSubjectWorkflow = `jobs:
  prepare:
    uses: example/other-ci/.forgejo/workflows/prepare-release.yml@1111111111111111111111111111111111111111
    with:
%[1]s    secrets:
      COSIGN_SIGNING_KEY: ${{ secrets.COSIGN_SIGNING_KEY }}
      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}
  build-and-release:
    outputs:
      dist-digest: ${{ steps.handoff.outputs.digest }}
    steps: []
  sign-and-publish:
    uses: example/other-ci/.forgejo/workflows/sign-and-publish-release.yml@1111111111111111111111111111111111111111
    with:
      release-tag: ${{ needs.prepare.outputs.release-tag }}
      release-sha: ${{ needs.prepare.outputs.release-sha }}
      artifacts-config: ${{ needs.prepare.outputs.artifacts-config }}
      dist-digest: ${{ needs.build-and-release.outputs.dist-digest }}
%[1]s    secrets:
      COSIGN_SIGNING_KEY: ${{ secrets.COSIGN_SIGNING_KEY }}
      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}
`

// runIsolationForSubject runs the forgejo-shaped gate with a caller-chosen
// single-pin subject and pin input.
func runIsolationForSubject(t *testing.T, mem *testfs.Memory, subject, pinInput string) (string, error) {
	t.Helper()

	return forgejoIsolation(t, mem, ".forgejo/workflows", subject, pinInput)
}

// TestIsolation_SinglePinInputIsTheCallersName proves the pin input is the
// name the caller states and that a call without it is refused.
//
// The name used to be derived from the subject's repository
// ("<repo>-helpers-sha"), so a subject served from a repository that was not
// named like the input had this half of the check skipped without a word.
func TestIsolation_SinglePinInputIsTheCallersName(t *testing.T) {
	const pinInput = "layer-sha"

	tests := map[string]struct {
		with    string
		input   string
		want    string
		wantErr bool
	}{
		"the stated input repeats the uses pin": {
			with:  "      layer-sha: 1111111111111111111111111111111111111111\n",
			input: pinInput,
		},
		"the stated input disagrees with the uses pin": {
			with:    "      layer-sha: 3333333333333333333333333333333333333333\n",
			input:   pinInput,
			want:    "mixed example/other-ci pins",
			wantErr: true,
		},
		"a call without the stated input is refused": {
			input:   pinInput,
			want:    `job "sign-and-publish" calls example/other-ci without a literal commit in its layer-sha input`,
			wantErr: true,
		},
		"a computed value is not a pin": {
			with:    "      layer-sha: ${{ needs.prepare.outputs.release-sha }}\n",
			input:   pinInput,
			want:    "without a literal commit in its layer-sha input",
			wantErr: true,
		},
		"an input named after the repository is not read": {
			with:    "      other-ci-helpers-sha: 1111111111111111111111111111111111111111\n",
			input:   pinInput,
			want:    "without a literal commit in its layer-sha input",
			wantErr: true,
		},
		"no stated input compares uses pins only": {
			with: "      other-ci-helpers-sha: 3333333333333333333333333333333333333333\n",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			mem := testfs.NewMemory(t)
			mem.WriteFile(".forgejo/workflows/release.yml", fmt.Appendf(nil, otherSubjectWorkflow, testCase.with))

			out, err := runIsolationForSubject(t, mem, "example/other-ci", testCase.input)
			if testCase.wantErr != errors.Is(err, errs.ErrValidation) {
				t.Fatalf("err = %v, want a validation failure: %v\n%s", err, testCase.wantErr, out)
			}

			if !strings.Contains(out, testCase.want) {
				t.Errorf("missing %q in:\n%s", testCase.want, out)
			}
		})
	}
}

// Retention checks nothing out at a helper commit, so its call needs none;
// its uses: pin is still compared with the others.
func TestIsolation_RetentionCallTakesNoHelperPin(t *testing.T) {
	const retention = `on: push
jobs:
  prune:
    uses: example/other-ci/.forgejo/workflows/package-retention.yml@%s
`

	for pin, wantErr := range map[string]bool{
		"1111111111111111111111111111111111111111": false,
		"3333333333333333333333333333333333333333": true,
	} {
		mem := testfs.NewMemory(t)
		mem.WriteFile(".forgejo/workflows/release.yml",
			fmt.Appendf(nil, otherSubjectWorkflow, "      layer-sha: 1111111111111111111111111111111111111111\n"))
		mem.WriteFile(".forgejo/workflows/retention.yml", fmt.Appendf(nil, retention, pin))

		out, err := runIsolationForSubject(t, mem, "example/other-ci", "layer-sha")
		if wantErr != errors.Is(err, errs.ErrValidation) {
			t.Fatalf("pin %s: err = %v, want a validation failure: %v\n%s", pin, err, wantErr, out)
		}

		if !wantErr && strings.Contains(out, "retention.yml") {
			t.Errorf("pin %s: the retention call was flagged:\n%s", pin, out)
		}
	}
}

// Full SHA-256 pins obey the same exact-match rule as SHA-1 pins.
func TestIsolation_SHA256PinInputs(t *testing.T) {
	t.Parallel()

	pin := strings.Repeat("a", 64)
	workflow := strings.ReplaceAll(otherSubjectWorkflow, strings.Repeat("1", 40), pin)

	for _, scenario := range []struct {
		name, input, want string
	}{
		{"matching", pin, ""},
		{"different SHA-256", strings.Repeat("b", 64), "mixed example/other-ci pins"},
		{"SHA-1 prefix", pin[:40], "mixed example/other-ci pins"},
		{"abbreviated SHA-256", pin[:63], "without a literal commit"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			mem := testfs.NewMemory(t)
			mem.WriteFile(".forgejo/workflows/release.yml", fmt.Appendf(nil, workflow, "      layer-sha: "+scenario.input+"\n"))

			out, err := runIsolationForSubject(t, mem, "example/other-ci", "layer-sha")
			if scenario.want == "" {
				if err != nil {
					t.Fatalf("matching SHA-256 pins refused: %v\n%s", err, out)
				}
			} else if !errors.Is(err, errs.ErrValidation) || !strings.Contains(out, scenario.want) {
				t.Fatalf("err = %v, output = %s, want validation failure containing %q", err, out, scenario.want)
			}
		})
	}
}

// TestIsolation_SinglePinInputCoversEveryWorkflowFile holds the directory-wide
// half: a call to the subject in a sibling workflow file answers to the same
// input, and is reported at that file.
func TestIsolation_SinglePinInputCoversEveryWorkflowFile(t *testing.T) {
	mem := testfs.NewMemory(t)
	mem.WriteFile(".forgejo/workflows/release.yml", fmt.Appendf(nil, otherSubjectWorkflow,
		"      layer-sha: 1111111111111111111111111111111111111111\n"))
	mem.WriteFile(".forgejo/workflows/bases.yml", []byte(`jobs:
  verify:
    uses: example/other-ci/.forgejo/workflows/verify.yml@1111111111111111111111111111111111111111
    with:
      flavor: core
`))

	out, err := runIsolationForSubject(t, mem, "example/other-ci", "layer-sha")
	if !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation\n%s", err, out)
	}

	if !strings.Contains(out, `file=.forgejo/workflows/bases.yml,line=3::job "verify" calls example/other-ci without a literal commit`) {
		t.Errorf("missing the sibling file's violation in:\n%s", out)
	}
}

func TestIsolation_MixedPinsFail(t *testing.T) {
	mem := testfs.NewMemory(t)
	mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow))
	mem.WriteFile(".forgejo/workflows/other.yml", []byte(`jobs:
  probe:
    steps:
      - uses: https://forge.example/examplescope/ci-layer/actions/setup-buildah@2222222222222222222222222222222222222222
`))

	out, err := runForgejoIsolationInFS(t, mem)
	if !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation\n%s", err, out)
	}

	if !strings.Contains(out, "mixed examplescope/ci-layer pins") {
		t.Errorf("missing mixed-pin violation in:\n%s", out)
	}
}

func TestIsolation_CommentedPinDoesNotCreateAConflict(t *testing.T) {
	mem := testfs.NewMemory(t)
	mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow))
	mem.WriteFile(".forgejo/workflows/other.yml", []byte(`# uses: https://forge.example/examplescope/ci-layer/actions/setup-buildah@2222222222222222222222222222222222222222
jobs:
  probe:
    steps:
      - run: echo no pin here
`))

	out, err := runForgejoIsolationInFS(t, mem)
	if err != nil {
		t.Fatalf("commented pin created a conflict: %v\n%s", err, out)
	}
}

func TestIsolation_SecretScalarAnnotations(t *testing.T) {
	body := "jobs:\n  build:\n    steps:\n      - run: >-\n          ${{ secrets.COSIGN_PRIVATE_KEY }}\n          ${{ secrets.RELEASE_GPG_PRIVATE_KEY }}\n      - env:\n          KEY: ${{ secrets.COSIGN_PRIVATE_KEY }}\n" + isolationEntry

	out, err := runIsolation(t, body)
	if !errors.Is(err, errs.ErrValidation) || strings.Contains(out, "Static release-isolation checks passed") {
		t.Fatalf("unsafe expressions did not refuse cleanly: %s %v", out, err)
	}

	for _, annotation := range []string{
		`::error file=.forgejo/workflows/release.yml,line=4::job "build" references signing secret "COSIGN_PRIVATE_KEY"`,
		`::error file=.forgejo/workflows/release.yml,line=4::job "build" references signing secret "RELEASE_GPG_PRIVATE_KEY"`,
		`::error file=.forgejo/workflows/release.yml,line=8::job "build" references signing secret "COSIGN_PRIVATE_KEY"`,
	} {
		if strings.Count(out, annotation) != 1 {
			t.Fatalf("missing/duplicate scalar annotation %q: %s", annotation, out)
		}
	}
}

// subjectGate runs the gate over a workflow directory holding release.yml and
// other.yml, for the subject examplescope/ci-layer on the forge at server.
func subjectGate(t *testing.T, dir, release, other, server string) (string, error) {
	t.Helper()

	mem := testfs.NewMemory(t)
	mem.WriteFile(dir+"/workflows/release.yml", []byte(release))
	mem.WriteFile(dir+"/workflows/other.yml", []byte(other))

	return subjectGateAt(t, mem, dir+"/workflows", server)
}

// subjectGateIn runs the gate over mem's .forgejo/workflows on the forge at
// server.
func subjectGateIn(t *testing.T, mem *testfs.Memory, server string) (string, error) {
	t.Helper()

	return subjectGateAt(t, mem, ".forgejo/workflows", server)
}

func subjectGateAt(t *testing.T, mem *testfs.Memory, workflowsDir, server string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	err := appvalidate.Isolation(&out, output.NewAnnotator(&out, output.FormatGitHub), appvalidate.IsolationInput{
		WorkflowsDir:     workflowsDir,
		SigningSecrets:   []string{"COSIGN_SIGNING_KEY", "GPG_SIGNING_KEY"},
		SinglePinSubject: "examplescope/ci-layer",
		ServerURL:        server,
		FS:               mem.FS(),
	})

	return out.String(), err
}

func checkSubjectGate(t *testing.T, out string, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Fatalf("refused: %v\n%s", err, out)
		}

		return
	}

	if !errors.Is(err, errs.ErrValidation) || !strings.Contains(out, want) {
		t.Fatalf("err = %v, want a validation failure containing %q\n%s", err, want, out)
	}
}

// The subject is one repository: a reference names it only as its own
// repository, and every such reference agrees on one commit. The GitHub and
// the Forgejo form of a reference without a forge address read alike.
func TestIsolation_SubjectIsOneRepositoryAtOnePin(t *testing.T) {
	t.Parallel()

	const one, two = isolationPin, "2222222222222222222222222222222222222222"

	for _, dir := range []string{".forgejo", ".github"} {
		release := "jobs:\n  build:\n    uses: examplescope/ci-layer/" + dir + "/workflows/build.yml@" + one + "\n" + isolationEntry

		for name, test := range map[string]struct{ uses, want string }{
			"another owner's repository of the same name": {"other/ci-layer/" + dir + "/actions/x@" + two, ""},
			"a repository whose name extends the subject": {"examplescope/ci-layer-extra/" + dir + "/actions/x@" + two, ""},
			"the subject as a path under another owner":   {"prefix/examplescope/ci-layer/" + dir + "/actions/x@" + two, ""},
			"a directory of the subject at a second pin":  {"examplescope/ci-layer/nested/" + dir + "/actions/x@" + two, "mixed examplescope/ci-layer pins"},
			"the subject at a second pin":                 {"examplescope/ci-layer/" + dir + "/actions/x@" + two, "mixed examplescope/ci-layer pins"},
			"the subject at the same pin":                 {"examplescope/ci-layer/" + dir + "/actions/x@" + one, ""},
		} {
			t.Run(dir+"/"+name, func(t *testing.T) {
				t.Parallel()

				out, err := subjectGate(t, dir, release, "jobs:\n  probe:\n    steps:\n      - uses: "+test.uses+"\n", "")
				checkSubjectGate(t, out, err, test.want)
			})
		}

		// The only reference is a path in another repository: nothing names
		// the subject, and the gate says so in place of passing over nothing.
		t.Run(dir+"/only a foreign repository", func(t *testing.T) {
			t.Parallel()

			out, err := subjectGate(t, dir, strings.ReplaceAll(release, "examplescope/", "prefix/examplescope/"), "jobs: {}\n", "")
			checkSubjectGate(t, out, err, "matches nothing")
		})
	}
}

// A reference that writes a forge address is fetched from that address, so
// the subject on a foreign host is another engine. Workflows are called
// without an address and resolved by the forge; actions write one.
func TestIsolation_SubjectIsOnTheForgeOfTheRun(t *testing.T) {
	t.Parallel()

	const (
		pin     = isolationPin
		action  = "/examplescope/ci-layer/.forgejo/actions/x@" + pin
		release = "jobs:\n  build:\n    uses: examplescope/ci-layer/.forgejo/workflows/build.yml@" + pin + "\n" + isolationEntry
	)

	for name, test := range map[string]struct {
		server string
		uses   []string
		want   string
		note   bool
	}{
		"actions from the forge of the run":  {server: "https://forge.example", uses: []string{"https://forge.example" + action}},
		"the server URL ends in a slash":     {server: "https://forge.example/", uses: []string{"https://forge.example" + action}},
		"host names compare without case":    {server: "https://Forge.Example", uses: []string{"https://forge.example" + action}},
		"a forge served below a path":        {server: "https://forge.example/git", uses: []string{"https://forge.example/git" + action}},
		"no address written, forge known":    {server: "https://forge.example", uses: []string{action[1:]}},
		"a foreign host":                     {server: "https://forge.example", uses: []string{"https://evil.example" + action}, want: "examplescope/ci-layer is referenced at https://evil.example, and this forge is https://forge.example"},
		"another scheme":                     {server: "https://forge.example", uses: []string{"http://forge.example" + action}, want: "referenced at http://forge.example"},
		"a path before the repository":       {server: "https://forge.example", uses: []string{"https://forge.example/git" + action}, want: "referenced at https://forge.example/git"},
		"one foreign host among the forge's": {server: "https://forge.example", uses: []string{"https://forge.example" + action, "https://evil.example" + action}, want: "referenced at https://evil.example"},
		"forge not known, one address":       {uses: []string{"https://evil.example" + action}, note: true},
		"forge not known, two addresses":     {uses: []string{"https://forge.example" + action, "https://evil.example" + action}, want: "more than one forge address under workflow directory: https://evil.example, https://forge.example"},
		"forge not known, no address":        {uses: []string{action[1:]}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var other strings.Builder

			other.WriteString("jobs:\n  probe:\n    steps:\n")

			for _, uses := range test.uses {
				other.WriteString("      - uses: " + uses + "\n")
			}

			out, err := subjectGate(t, ".forgejo", release, other.String(), test.server)
			checkSubjectGate(t, out, err, test.want)

			if noted := strings.Contains(out, "the address https://evil.example of the examplescope/ci-layer references was not compared"); noted != test.note {
				t.Errorf("note that the address was not compared: %v, want %v\n%s", noted, test.note, out)
			}
		})
	}
}

// A job that may receive a signing secret calls the subject. The secret check
// admits a call by the signer workflow's name, so without this a foreign
// repository's workflow took the keys.
func TestIsolation_SigningSecretsGoToTheSubject(t *testing.T) {
	t.Parallel()

	const (
		pin    = "@1111111111111111111111111111111111111111"
		signer = "examplescope/ci-layer/.forgejo/workflows/sign-and-publish-release.yml" + pin
		idle   = "jobs: {}\n"
	)

	for name, test := range map[string]struct{ release, other, server, want string }{
		"the subject's signer": {forgejoIsolationCleanWorkflow, idle, "https://forge.example", ""},
		"a signer of another repository": {
			strings.Replace(forgejoIsolationCleanWorkflow, signer, "other/ci-layer/.forgejo/workflows/sign-and-publish-release.yml"+pin, 1), idle, "",
			`job "sign-and-publish" calls other/ci-layer/.forgejo/workflows/sign-and-publish-release.yml` + pin + ", which is not examplescope/ci-layer",
		},
		"a prepare job of another repository": {
			strings.Replace(forgejoIsolationCleanWorkflow, "examplescope/ci-layer/.forgejo/workflows/prepare-release.yml", "prefix/examplescope/ci-layer/.forgejo/workflows/prepare-release.yml", 1), idle, "",
			`job "prepare" calls prefix/examplescope/ci-layer/.forgejo/workflows/prepare-release.yml`,
		},
		// A job that calls another repository's workflow under another name
		// is no signer call: the key may not reach it.
		"a sign job that calls another repository under another name": {
			strings.Replace(forgejoIsolationCleanWorkflow, signer, "other/ci-layer/.forgejo/workflows/sign.yml"+pin, 1), idle, "",
			`job "sign-and-publish" references signing secret "COSIGN_SIGNING_KEY"`,
		},
		"the subject's signer on a foreign host": {
			strings.Replace(forgejoIsolationCleanWorkflow, signer, "https://evil.example/"+signer, 1), idle, "https://forge.example",
			"examplescope/ci-layer is referenced at https://evil.example",
		},
		"a signer of another repository in a sibling file": {
			forgejoIsolationCleanWorkflow, "jobs:\n  images:\n    uses: other/ci-layer/.forgejo/workflows/sign-container-images.yml" + pin + "\n", "",
			`job "images" calls other/ci-layer/.forgejo/workflows/sign-container-images.yml`,
		},
		"another repository's workflow that is no signer": {
			forgejoIsolationCleanWorkflow, "jobs:\n  notify:\n    uses: other/tools/.forgejo/workflows/notify.yml" + pin + "\n", "", "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out, err := subjectGate(t, ".forgejo", test.release, test.other, test.server)
			checkSubjectGate(t, out, err, test.want)
		})
	}
}

// The gate reads uses: references. A GitLab include names its component under
// another key, on any host, so it is not a reference the gate knows: the
// subject matches nothing and the gate refuses, where passing would claim a
// check of the component's repository, host and pin that nothing made.
func TestIsolation_GitLabComponentsAreNotRead(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"$CI_SERVER_FQDN", "evil.example"} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()

			mem := testfs.NewMemory(t)
			mem.WriteFile(".gitlab-ci.yml", []byte("include:\n  - component: "+host+"/examplescope/ci-layer/check@1111111111111111111111111111111111111111\n"))

			var out bytes.Buffer

			err := appvalidate.Isolation(&out, output.NewAnnotator(&out, output.FormatGitHub), appvalidate.IsolationInput{
				WorkflowsDir: ".", SinglePinSubject: "examplescope/ci-layer", ServerURL: "https://forge.example", FS: mem.FS(),
			})
			checkSubjectGate(t, out.String(), err, "matches nothing")
		})
	}
}

// Every reference to the subject is pinned to a full commit. A branch, a tag
// or a short commit names whatever the engine's repository points it at
// next, and passed before because only full commits were collected as pins.
func TestIsolation_SubjectIsPinnedToAFullCommit(t *testing.T) {
	t.Parallel()

	const action = "examplescope/ci-layer/.forgejo/actions/setup-forge-ci"

	for name, test := range map[string]struct{ uses, want string }{
		"a full commit":            {uses: action + "@" + isolationPin},
		"a branch":                 {uses: action + "@main", want: "references examplescope/ci-layer at @main, which is not a full commit"},
		"a tag":                    {uses: action + "@v1.2.3", want: "at @v1.2.3, which is not a full commit"},
		"a short commit":           {uses: action + "@" + isolationPin[:12], want: "at @" + isolationPin[:12] + ", which is not a full commit"},
		"an upper-case commit":     {uses: action + "@" + strings.Repeat("A", 40), want: "which is not a full commit"},
		"no ref":                   {uses: action, want: "at no ref, which is not a full commit"},
		"a branch on the forge":    {uses: "https://forge.example/" + action + "@main", want: "at @main, which is not a full commit"},
		"another repository's tag": {uses: "other/tools/.forgejo/actions/x@v1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mem := testfs.NewMemory(t)
			mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow))
			mem.WriteFile(".forgejo/workflows/lint.yml", []byte("jobs:\n  lint:\n    steps:\n      - uses: "+test.uses+"\n"))

			out, err := runForgejoIsolationInFS(t, mem)
			if test.want == "" {
				if err != nil {
					t.Fatalf("refused: %v\n%s", err, out)
				}

				return
			}

			if !errors.Is(err, errs.ErrValidation) || !strings.Contains(out, "file=.forgejo/workflows/lint.yml,line=4::") || !strings.Contains(out, test.want) {
				t.Fatalf("err = %v, want a refusal at lint.yml:4 containing %q\n%s", err, test.want, out)
			}
		})
	}

	// A release workflow whose only engine reference is a branch is refused
	// for the branch, not passed over as a subject that matches nothing.
	t.Run("the entry point at a branch", func(t *testing.T) {
		t.Parallel()

		out, err := runForgejoIsolation(t, strings.ReplaceAll(forgejoIsolationCleanWorkflow, "@"+isolationPin, "@main"))
		if !errors.Is(err, errs.ErrValidation) || !strings.Contains(out, "prepare-release.yml@main references examplescope/ci-layer at @main") ||
			strings.Contains(out, "matches nothing") {
			t.Fatalf("err = %v\n%s", err, out)
		}
	})
}

// The engine is the same repository in any case of its owner and name: a
// reference spelled in another case is held to the one pin, may receive the
// signing keys as a signer call, and makes a workflow a release workflow.
func TestIsolation_SubjectMatchesInAnyCase(t *testing.T) {
	t.Parallel()

	upper := strings.ReplaceAll(forgejoIsolationCleanWorkflow, "examplescope/ci-layer", "ExampleScope/CI-Layer")

	for name, test := range map[string]struct {
		files map[string]string
		want  string
	}{
		"a release workflow in another case": {files: map[string]string{"release.yml": upper}},
		"another case at a second pin": {
			files: map[string]string{
				"release.yml": forgejoIsolationCleanWorkflow,
				"lint.yml":    "jobs:\n  lint:\n    steps:\n      - uses: ExampleScope/ci-layer/.forgejo/actions/x@" + strings.Repeat("2", 40) + "\n",
			},
			want: "mixed examplescope/ci-layer pins",
		},
		"another case at a branch": {
			files: map[string]string{
				"release.yml": forgejoIsolationCleanWorkflow,
				"lint.yml":    "jobs:\n  lint:\n    steps:\n      - uses: EXAMPLESCOPE/CI-LAYER/.forgejo/actions/x@main\n",
			},
			want: "which is not a full commit",
		},
		"another case on a foreign host": {
			files: map[string]string{
				"release.yml": forgejoIsolationCleanWorkflow,
				"lint.yml":    "jobs:\n  lint:\n    steps:\n      - uses: https://evil.example/ExampleScope/CI-Layer/.forgejo/actions/x@" + isolationPin + "\n",
			},
			want: "referenced at https://evil.example",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mem := testfs.NewMemory(t)
			for file, body := range test.files {
				mem.WriteFile(".forgejo/workflows/"+file, []byte(body))
			}

			out, err := subjectGateIn(t, mem, "https://forge.example")
			checkSubjectGate(t, out, err, test.want)
		})
	}
}

// githubReleaseWorkflow is a GitHub release workflow: one call of the
// layer's release orchestrator, which signs inside, with named secrets.
const githubReleaseWorkflow = `on:
  push:
    tags: ["release-request/v*"]
jobs:
  release:
    uses: examplescope/ci-layer/.github/workflows/release-orchestrator.yml@` + isolationPin + `
    secrets:
      RELEASE_TOKEN: ${{ secrets.RELEASE_TOKEN }}
      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}
    with:
      forge-ci-sha: ` + isolationPin + `
`

// A GitHub workflow directory is read as a Forgejo one is: the workflows
// that call the release orchestrator are the release workflows, the
// orchestrator call is the signer call that may take the keys, and
// secrets: inherit, which hands it every secret, is refused.
func TestIsolation_GitHubReleaseWorkflows(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		release string
		other   string
		want    string
	}{
		"named secrets to the orchestrator": {release: githubReleaseWorkflow},
		"secrets: inherit": {
			release: strings.Replace(githubReleaseWorkflow, "    secrets:\n      RELEASE_TOKEN: ${{ secrets.RELEASE_TOKEN }}\n      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}\n", "    secrets: inherit\n", 1),
			want:    `job "release" inherits every caller secret (secrets: inherit)`,
		},
		"a signing key to another job": {
			release: githubReleaseWorkflow + "  notes:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: echo\n        env:\n          KEY: ${{ secrets.GPG_SIGNING_KEY }}\n",
			want:    `job "notes" references signing secret "GPG_SIGNING_KEY"`,
		},
		"a signing key in a workflow that is not a release": {
			release: githubReleaseWorkflow,
			other:   "jobs:\n  lint:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: echo\n        env:\n          KEY: ${{ secrets.GPG_SIGNING_KEY }}\n",
			want:    "file=.github/workflows/other.yml,line=7::job \"lint\" references signing secret",
		},
		"the orchestrator at a branch": {
			release: strings.Replace(githubReleaseWorkflow, "release-orchestrator.yml@"+isolationPin, "release-orchestrator.yml@main", 1),
			want:    "release-orchestrator.yml@main references examplescope/ci-layer at @main, which is not a full commit",
		},
		"the orchestrator of another repository": {
			release: githubReleaseWorkflow + "  more:\n    uses: other/fork/.github/workflows/release-orchestrator.yml@" + isolationPin + "\n    secrets:\n      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}\n",
			want:    "other/fork/.github/workflows/release-orchestrator.yml",
		},
		"a Forgejo signer path on GitHub": {
			release: githubReleaseWorkflow + "  more:\n    uses: examplescope/ci-layer/.github/workflows/prepare-release.yml@" + isolationPin + "\n    secrets:\n      GPG_SIGNING_KEY: ${{ secrets.GPG_SIGNING_KEY }}\n",
			want:    `job "more" references signing secret "GPG_SIGNING_KEY"`,
		},
		"no orchestrator call": {
			release: "jobs:\n  build:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make\n",
			want:    "no workflow under .github/workflows calls examplescope/ci-layer's",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			other := test.other
			if other == "" {
				other = "jobs:\n  lint:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: echo\n"
			}

			out, err := subjectGate(t, ".github", test.release, other, "https://github.com")
			checkSubjectGate(t, out, err, test.want)
		})
	}
}

// A reusable workflow of the layer knows its repository as the HTTPS URL it
// fetched its files from, so the subject is taken in that form too and read
// as the repository it names.
func TestIsolation_SubjectAsARepositoryURL(t *testing.T) {
	t.Parallel()

	for subject, want := range map[string]error{
		"examplescope/ci-layer":                            nil,
		"https://forge.example/examplescope/ci-layer.git":  nil,
		"https://forge.example/examplescope/ci-layer":      nil,
		"https://forge.example/examplescope/ci-layer/":     nil,
		"https://forge.example/ExampleScope/CI-Layer.git":  nil,
		"https://forge.example/other/ci-layer.git":         errs.ErrValidation,
		"https://forge.example/ci-layer.git":               errs.ErrUsage,
		"https://forge.example/examplescope/../ci-layer":   errs.ErrUsage,
		"http://forge.example/examplescope/ci-layer.git":   errs.ErrUsage,
		"https://user@forge.example/examplescope/ci-layer": errs.ErrUsage,
		"https://forge.example/examplescope/ci-layer?x=1":  errs.ErrUsage,
		"https:///examplescope/ci-layer":                   errs.ErrUsage,
	} {
		t.Run(subject, func(t *testing.T) {
			t.Parallel()

			mem := testfs.NewMemory(t)
			mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow))

			out, err := forgejoIsolation(t, mem, ".forgejo/workflows", subject, "")
			if want == nil && err != nil || want != nil && !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v\n%s", err, want, out)
			}
		})
	}
}

// The second review's fixtures of secret-holding release jobs: fxB fetches
// and runs code by an archive, the engine binary's variable, a submodule and
// a script of its own; fxE names the secret by a computed index, a bracket
// and a lower-case dependency name; fxF fetches with git init and a forge
// CLI, and pins the engine by another case at a branch.
func TestIsolation_SecretJobsOfTheSecondReview(t *testing.T) {
	t.Parallel()

	const refuse = "may run only the layer's actions"

	for name, test := range map[string]struct {
		jobs string
		want []string
	}{
		"fxB": {
			jobs: "  publish-extra:\n    needs: [build-and-release]\n    env:\n      TOKEN: ${{ secrets.PACKAGE_PUBLISH_TOKEN }}\n    steps:\n      - run: |\n" +
				"          curl -fsSL \"$FORGEJO_SERVER/$FORGEJO_REPO/archive/main.tar.gz\" | tar xz\n" +
				"          \"$FORGE_CI_BIN\" platform checkout\n          git submodule update --init\n          ./forge-sync/publish.sh\n",
			want: []string{`line=24::job "publish-extra" references secret "PACKAGE_PUBLISH_TOKEN" and runs a run: step (line 26)`, refuse},
		},
		"fxE": {
			jobs: "  build:\n    env:\n      X: ${{ secrets[format('{0}_TOKEN', 'PACKAGE_PUBLISH')] }}\n      Y: ${{ secrets['PACKAGE_PUBLISH_TOKEN'] }}\n" +
				"      Z: ${{ secrets.dependency_x }}\n    steps:\n      - run: make\n",
			want: []string{
				`job "build" references signing secret through the whole secrets context`,
				`job "build" references secret "PACKAGE_PUBLISH_TOKEN" and runs a run: step`,
			},
		},
		"fxF": {
			jobs: "  fetch:\n    steps:\n      - run: |\n          git init . && git remote add o \"$FORGEJO_SERVER/$FORGEJO_REPO\" && git fetch o\n          GH=1 tea clone x\n" +
				"        env:\n          T: ${{ secrets.PACKAGE_PUBLISH_TOKEN }}\n" +
				"  other-prepare:\n    uses: https://forge.example/ExampleScope/CI-Layer/.forgejo/workflows/prepare-release.yml@main\n",
			want: []string{
				`job "fetch" references secret "PACKAGE_PUBLISH_TOKEN" and runs a run: step`,
				"https://forge.example/ExampleScope/CI-Layer/.forgejo/workflows/prepare-release.yml@main references examplescope/ci-layer at @main, which is not a full commit",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out, err := runForgejoIsolation(t, forgejoIsolationCleanWorkflow+test.jobs)
			if !errors.Is(err, errs.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation\n%s", err, out)
			}

			for _, want := range test.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
		})
	}
}
