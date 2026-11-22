// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/testfs"
)

// A local reusable workflow runs in the release with what its caller hands
// it, so the release-isolation rules hold for its jobs too.
func TestIsolation_CalledLocalWorkflowsAreChecked(t *testing.T) {
	const (
		images = ".forgejo/workflows/release-native-images.yml"
		call   = "  images:\n    uses: ./" + images + "\n"
		clean  = "on:\n  workflow_call: {}\njobs:\n  build:\n    steps:\n" +
			"      - uses: https://forge.example/examplescope/ci-layer/.forgejo/actions/setup-toolchain@1111111111111111111111111111111111111111\n" +
			"        with:\n          cache: false\n"
	)

	for name, tc := range map[string]struct {
		files map[string]string
		call  string
		want  string
	}{
		"a clean local workflow": {files: map[string]string{images: clean}, call: call},
		"a cached toolchain": {
			files: map[string]string{images: strings.Replace(clean, "cache: false", "cache: true", 1)},
			call:  call, want: "file=" + images + `,line=6::job "build": setup-toolchain step must set cache: false`,
		},
		"a persisted checkout": {
			files: map[string]string{images: clean + "      - uses: actions/checkout@1111111111111111111111111111111111111111\n"},
			call:  call, want: "actions/checkout step must set persist-credentials: false",
		},
		"a signing secret": {
			files: map[string]string{images: clean + "    env:\n      KEY: ${{ secrets.GPG_SIGNING_KEY }}\n"},
			call:  call, want: `job "build" references signing secret "GPG_SIGNING_KEY"`,
		},
		"a second local workflow it calls": {
			files: map[string]string{
				images:                          clean + "  nested:\n    uses: ./.forgejo/workflows/nested.yml\n",
				".forgejo/workflows/nested.yml": strings.Replace(clean, "cache: false", "cache: true", 1),
			},
			call: call, want: `file=.forgejo/workflows/nested.yml,line=6::job "build": setup-toolchain step must set cache: false`,
		},
		"a local workflow that is not there": {
			files: map[string]string{}, call: call,
			want: `job "images" calls ./` + images + `, which the repository does not have`,
		},
		"a path outside the repository": {
			files: map[string]string{}, call: "  images:\n    uses: ./../other/.forgejo/workflows/x.yml\n",
			want: "which is outside the repository",
		},
	} {
		t.Run(name, func(t *testing.T) {
			mem := testfs.NewMemory(t)
			mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow+tc.call))

			for file, body := range tc.files {
				mem.WriteFile(file, []byte(body))
			}

			out, err := runForgejoIsolationInFS(t, mem)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Isolation: %v\n%s", err, out)
				}

				if !strings.Contains(out, "Local workflow "+images+", which a release workflow calls, checked with it") {
					t.Errorf("the called workflow is not said to be checked:\n%s", out)
				}

				return
			}

			if !errors.Is(err, errs.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation\n%s", err, out)
			}

			if !strings.Contains(out, tc.want) {
				t.Errorf("missing %q in:\n%s", tc.want, out)
			}
		})
	}
}

// A workflow that calls a release workflow as a local reusable workflow is a
// release workflow too, through any number of calls: its other jobs run with
// the caller's trigger and secrets beside the release. A tag-triggered
// wrapper whose checkout job held a publish token passed before, because
// only a direct call of an entry point made a release.
func TestIsolation_WorkflowsThatCallAReleaseAreReleases(t *testing.T) {
	const (
		release = ".forgejo/workflows/release.yml"
		outer   = ".forgejo/workflows/outer.yml"
		sneaky  = "  sneaky:\n    steps:\n" +
			"      - uses: examplescope/ci-layer/.forgejo/actions/checkout-consumer@" + isolationPin + "\n" +
			"      - run: ./scripts/publish.sh\n        env:\n          T: ${{ secrets.PACKAGE_PUBLISH_TOKEN }}\n"
		tokenRefused = `job "sneaky" references secret "PACKAGE_PUBLISH_TOKEN" and runs the layer's checkout-consumer`
	)

	wrapper := func(target, extra string) string {
		return "on:\n  push:\n    tags: ['release-request/v*']\njobs:\n" + extra +
			"  release:\n    uses: ./" + target + "\n    secrets: inherit\n"
	}

	for name, tc := range map[string]struct {
		files map[string]string
		want  []string
		pass  bool
	}{
		// fxC of the second review.
		"a wrapper with a token-holding checkout job": {
			files: map[string]string{outer: wrapper(release, sneaky)},
			want: []string{
				"file=" + outer + ",line=12::job \"release\" inherits every caller secret (secrets: inherit)",
				"file=" + outer + ",line=10::" + tokenRefused,
			},
		},
		"a wrapper of a wrapper": {
			files: map[string]string{
				outer:                          wrapper(".forgejo/workflows/mid.yml", ""),
				".forgejo/workflows/mid.yml":   "on:\n  workflow_call: {}\njobs:\n" + sneaky + "  release:\n    uses: ./" + release + "\n",
				".forgejo/workflows/other.yml": "on: push\njobs:\n  lint:\n    uses: ./.forgejo/workflows/lint.yml\n    secrets: inherit\n",
				".forgejo/workflows/lint.yml":  "on:\n  workflow_call: {}\njobs:\n" + sneaky,
			},
			want: []string{
				"file=" + outer + `,line=6::job "release" inherits every caller secret`,
				"file=.forgejo/workflows/mid.yml,line=9::" + tokenRefused,
			},
		},
		"a workflow that calls no release": {
			files: map[string]string{
				".forgejo/workflows/other.yml": "on: push\njobs:\n  lint:\n    uses: ./.forgejo/workflows/lint.yml\n    secrets: inherit\n",
				".forgejo/workflows/lint.yml":  "on:\n  workflow_call: {}\njobs:\n" + sneaky,
			},
			pass: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			mem := testfs.NewMemory(t)
			mem.WriteFile(release, []byte("on:\n  workflow_call: {}\n"+forgejoIsolationCleanWorkflow))

			for file, body := range tc.files {
				mem.WriteFile(file, []byte(body))
			}

			out, err := runForgejoIsolationInFS(t, mem)
			if tc.pass {
				if err != nil {
					t.Fatalf("Isolation: %v\n%s", err, out)
				}

				return
			}

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
