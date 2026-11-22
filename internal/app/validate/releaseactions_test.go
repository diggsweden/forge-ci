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

// A release runs only the repository's own composite actions and the
// subject's, in the release workflow and in each local workflow it calls.
func TestIsolation_ReleaseRunsOnlyLocalAndSubjectActions(t *testing.T) {
	const (
		subjectAction = "https://forge.example/examplescope/ci-layer/.forgejo/actions/setup-forge-ci@1111111111111111111111111111111111111111"
		images        = ".forgejo/workflows/images.yml"
		composite     = "runs:\n  using: composite\n  steps:\n    - run: make dist\n      shell: bash\n"
	)

	job := func(uses ...string) string {
		var body strings.Builder

		body.WriteString("  extra:\n    steps:\n")

		for _, u := range uses {
			body.WriteString("      - uses: " + u + "\n")
		}

		return body.String()
	}

	for name, tc := range map[string]struct {
		jobs  string
		files map[string]string
		want  string
	}{
		"the subject's action":                    {jobs: job(subjectAction)},
		"a local composite action":                {jobs: job("./.forgejo/actions/pack"), files: map[string]string{".forgejo/actions/pack/action.yml": composite}},
		"an action.yaml":                          {jobs: job("./.forgejo/actions/pack"), files: map[string]string{".forgejo/actions/pack/action.yaml": composite}},
		"a pinned action by address":              {jobs: job("https://code.forgejo.org/actions/setup-go@1111111111111111111111111111111111111111")},
		"a pinned action":                         {jobs: job("example/tool@1111111111111111111111111111111111111111")},
		"a pinned action, write token":            {jobs: "  extra:\n    permissions:\n      packages: write\n    steps:\n      - uses: actions/setup-node@1111111111111111111111111111111111111111\n"},
		"a pinned action with a dependency token": {jobs: "  extra:\n    env:\n      T: ${{ secrets.DEPENDENCY_TOKEN }}\n    steps:\n      - uses: example/tool@1111111111111111111111111111111111111111\n"},
		"a pinned action beside a real secret":    {jobs: "  extra:\n    env:\n      T: ${{ secrets.NPM_TOKEN }}\n    steps:\n      - uses: example/tool@1111111111111111111111111111111111111111\n", want: `references secret "NPM_TOKEN"`},
		"a pinned action beside a signing secret": {jobs: "  extra:\n    env:\n      K: ${{ secrets.GPG_SIGNING_KEY }}\n    steps:\n      - uses: example/tool@1111111111111111111111111111111111111111\n", want: `references signing secret "GPG_SIGNING_KEY"`},
		"a real secret as a local workflow input": {
			jobs:  "  images:\n    uses: ./" + images + "\n    with:\n      t: ${{ secrets.NPM_TOKEN }}\n",
			files: map[string]string{images: "on:\n  workflow_call: {}\njobs:\n  build:\n    steps:\n      - uses: example/tool@1111111111111111111111111111111111111111\n"},
			want:  `passes secret "NPM_TOKEN" to a local workflow`,
		},
		"another repository at a layer action's path": {jobs: job("evil/repo/.forgejo/actions/upload-dist@1111111111111111111111111111111111111111"), want: "upload-dist@1111111111111111111111111111111111111111 is neither a path"},
		"a third-party reusable workflow":             {jobs: "  other:\n    uses: evil/repo/.github/workflows/x.yml@1111111111111111111111111111111111111111\n", want: "x.yml@1111111111111111111111111111111111111111 is neither a path"},
		"a climbing action path":                      {jobs: job("actions/../../x@1111111111111111111111111111111111111111"), want: "is neither a path"},
		"a third-party action at a tag":               {jobs: job("https://code.forgejo.org/actions/setup-go@v5"), want: "https://code.forgejo.org/actions/setup-go@v5 is neither a path in this repository, nor examplescope/ci-layer"},
		"a third-party action at a short commit":      {jobs: job("actions/checkout@1111111"), want: "actions/checkout@1111111 is neither a path"},
		"a container image":                           {jobs: job("docker://alpine:3"), want: "docker://alpine:3 is neither a path"},
		"a local Node action": {
			jobs: job("./.forgejo/actions/pack"), files: map[string]string{".forgejo/actions/pack/action.yml": "runs:\n  using: node20\n  main: index.js\n"},
			want: `local action ./.forgejo/actions/pack runs "node20": a release runs composite actions only`,
		},
		"a local action without action.yml":     {jobs: job("./.forgejo/actions/pack"), want: "local action ./.forgejo/actions/pack has no action.yml"},
		"a local action outside the repository": {jobs: job("./../pack"), want: "./../pack is outside the repository"},
		"a third-party action inside a local action": {
			jobs:  job("./.forgejo/actions/pack"),
			files: map[string]string{".forgejo/actions/pack/action.yml": "runs:\n  using: composite\n  steps:\n    - uses: example/tool@v1\n"},
			want:  "file=.forgejo/actions/pack/action.yml,line=4::example/tool@v1 is neither a path",
		},
		"a third-party action in a called local workflow": {
			jobs:  "  images:\n    uses: ./" + images + "\n",
			files: map[string]string{images: "on:\n  workflow_call: {}\njobs:\n  build:\n    steps:\n      - uses: example/tool@v1\n"},
			want:  "file=" + images + ",line=6::example/tool@v1 is neither a path",
		},
	} {
		t.Run(name, func(t *testing.T) {
			mem := testfs.NewMemory(t)
			mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow+tc.jobs))

			for file, body := range tc.files {
				mem.WriteFile(file, []byte(body))
			}

			out, err := runForgejoIsolationInFS(t, mem)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Isolation: %v\n%s", err, out)
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

// A local action the release runs is read for its references to the engine
// as a workflow is: each is pinned to the directory's one full commit and
// written on the forge of the run. The action walk admitted the engine at
// any ref and on any host, and the pin check read the workflow directory
// alone.
func TestIsolation_LocalActionsAreHeldToThePinAndForge(t *testing.T) {
	t.Parallel()

	const engine = "examplescope/ci-layer/.forgejo/actions/setup-forge-ci"

	action := func(uses string) string {
		return "runs:\n  using: composite\n  steps:\n    - uses: " + uses + "\n"
	}

	for name, test := range map[string]struct {
		files map[string]string
		want  string
	}{
		"the engine at the directory's pin": {files: map[string]string{".forgejo/actions/pack/action.yml": action("https://forge.example/" + engine + "@" + isolationPin)}},
		"the engine at a branch": {
			files: map[string]string{".forgejo/actions/pack/action.yml": action("https://forge.example/" + engine + "@main")},
			want:  "file=.forgejo/actions/pack/action.yml,line=4::https://forge.example/" + engine + "@main references examplescope/ci-layer at @main, which is not a full commit",
		},
		"the engine at another commit": {
			files: map[string]string{".forgejo/actions/pack/action.yml": action(engine + "@" + strings.Repeat("2", 40))},
			want:  "mixed examplescope/ci-layer pins under workflow directory: " + isolationPin + ", " + strings.Repeat("2", 40),
		},
		"the engine on a foreign host": {
			files: map[string]string{".forgejo/actions/pack/action.yml": action("https://evil.example/" + engine + "@" + isolationPin)},
			want:  "examplescope/ci-layer is referenced at https://evil.example, and this forge is https://forge.example",
		},
		"the engine at a branch in a nested local action": {
			files: map[string]string{
				".forgejo/actions/pack/action.yml":  action("./.forgejo/actions/inner"),
				".forgejo/actions/inner/action.yml": action(engine + "@v1"),
			},
			want: "file=.forgejo/actions/inner/action.yml,line=4::" + engine + "@v1 references examplescope/ci-layer at @v1",
		},
		"the engine at a branch in an action of a called workflow": {
			files: map[string]string{
				".forgejo/workflows/images.yml":     "on:\n  workflow_call: {}\njobs:\n  build:\n    steps:\n      - uses: ./.forgejo/actions/inner\n",
				".forgejo/actions/inner/action.yml": action(engine + "@main"),
			},
			want: "file=.forgejo/actions/inner/action.yml,line=4::",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			release := forgejoIsolationCleanWorkflow + "  extra:\n    steps:\n      - uses: ./.forgejo/actions/pack\n"
			if _, ok := test.files[".forgejo/workflows/images.yml"]; ok {
				release = forgejoIsolationCleanWorkflow + "  images:\n    uses: ./.forgejo/workflows/images.yml\n"
			}

			mem := testfs.NewMemory(t)
			mem.WriteFile(".forgejo/workflows/release.yml", []byte(release))

			for file, body := range test.files {
				mem.WriteFile(file, []byte(body))
			}

			out, err := subjectGateIn(t, mem, "https://forge.example")
			if test.want == "" {
				if err != nil {
					t.Fatalf("refused: %v\n%s", err, out)
				}

				return
			}

			if !errors.Is(err, errs.ErrValidation) || !strings.Contains(out, test.want) {
				t.Fatalf("err = %v, want a refusal containing %q\n%s", err, test.want, out)
			}
		})
	}
}

// A release job's container and service images are code its steps run in,
// with what the job holds: each is pinned by digest or taken from one
// repository variable. fxA of the second review ran in docker.io/evil
// images at :latest.
func TestIsolation_ReleaseImagesArePinned(t *testing.T) {
	t.Parallel()

	const (
		digest = "@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		refuse = "a release job's container and service images are pinned by digest"
	)

	job := func(spec string) string { return "  extra:\n" + spec + "    steps: []\n" }

	for name, tc := range map[string]struct {
		jobs  string
		files map[string]string
		want  string
	}{
		"a digest-pinned container":           {jobs: job("    container:\n      image: docker.io/library/node:24" + digest + "\n")},
		"a digest-pinned container by string": {jobs: job("    container: ghcr.io/example/runner" + digest + "\n")},
		"a container from a variable":         {jobs: job("    container:\n      image: ${{ vars.CI_JOB_IMAGE }}\n")},
		"a digest-pinned service":             {jobs: job("    services:\n      db:\n        image: docker.io/library/postgres:17" + digest + "\n")},
		"fxA: a container at latest": {
			jobs: job("    container:\n      image: docker.io/evil/anything:latest\n"),
			want: `line=23::job "extra" runs in image docker.io/evil/anything:latest: ` + refuse,
		},
		"fxA: a service at latest": {
			jobs: job("    services:\n      db:\n        image: docker.io/evil/svc:latest\n"),
			want: `line=24::job "extra" starts service "db" from image docker.io/evil/svc:latest: ` + refuse,
		},
		"a short digest":                 {jobs: job("    container: docker.io/library/alpine@sha256:abc\n"), want: `runs in image docker.io/library/alpine@sha256:abc`},
		"a container by string at a tag": {jobs: job("    container: alpine:3\n"), want: `job "extra" runs in image alpine:3`},
		"a container from an input":      {jobs: job("    container:\n      image: ${{ inputs.image }}\n"), want: `runs in image ${{ inputs.image }}`},
		"a container from the event":     {jobs: job("    container:\n      image: ${{ github.event.inputs.image }}\n"), want: `runs in image ${{ github.event.inputs.image }}`},
		"a variable with a fallback":     {jobs: job("    container:\n      image: ${{ vars.IMAGE || 'alpine:3' }}\n"), want: `runs in image ${{ vars.IMAGE || 'alpine:3' }}`},
		"a variable inside a reference":  {jobs: job("    container:\n      image: docker.io/x/${{ vars.NAME }}:1\n"), want: `runs in image docker.io/x/${{ vars.NAME }}:1`},
		"an image behind a YAML merge key": {
			jobs:  "  images:\n    uses: ./.forgejo/workflows/images.yml\n",
			files: map[string]string{".forgejo/workflows/images.yml": "on:\n  workflow_call: {}\nx-base: &c\n  image: alpine:3\njobs:\n  build:\n    container:\n      <<: *c\n    steps: []\n"},
			want:  `job "build" runs in image alpine:3`,
		},
		"a container in a called local workflow": {
			jobs:  "  images:\n    uses: ./.forgejo/workflows/images.yml\n",
			files: map[string]string{".forgejo/workflows/images.yml": "on:\n  workflow_call: {}\njobs:\n  build:\n    container: alpine:3\n    steps: []\n"},
			want:  `file=.forgejo/workflows/images.yml,line=5::job "build" runs in image alpine:3`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mem := testfs.NewMemory(t)
			mem.WriteFile(".forgejo/workflows/release.yml", []byte(forgejoIsolationCleanWorkflow+tc.jobs))

			for file, body := range tc.files {
				mem.WriteFile(file, []byte(body))
			}

			out, err := runForgejoIsolationInFS(t, mem)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Isolation: %v\n%s", err, out)
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
