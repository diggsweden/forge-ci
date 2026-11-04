// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"flag"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/consumerinit"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

var updateDemos = flag.Bool("update-demos", false, "rewrite the example demos' CI files from their examples")

// exampleDemos are the demos that show an example: their source is their
// own, and every CI file is the example's, filled in by exampleDemoFiles.
var exampleDemos = map[string]string{
	"hello-go-cli":     "go-cli",
	"hello-go-service": "go-service",
	"hello-gradle":     "gradle-app",
	"hello-monorepo":   "monorepo",
}

// exampleDemoPin stands where a consumer pins Forge CI; the live tier puts
// the layer under test in its place, as it does for every demo.
const exampleDemoPin = "2f4e845cee96973d1082bf00a5a18689c1482148"

// exampleDemoValues fill an example's placeholders the way an adopter
// would, longest first; the image inputs are the ones the GitLab gate trial
// binds to its own images.
var exampleDemoValues = strings.NewReplacer(
	"FORGE_HOST/OWNER/forge-ci-signer@sha256:PIN_SIGNER_IMAGE_DIGEST", "forgejo.example/itiquette/forge-ci-signer@sha256:"+unrunDigest,
	"PIN_COSIGN_PUBLIC_KEY_SHA256", strings.Repeat("1", 64),
	"PIN_FORGE_CI_SHA", exampleDemoPin,
	"https://FORGE_HOST/", "https://codefloe.com/",
	"OWNER/forge-ci", "itiquette/forge-ci",
	"FIXME Release Bot", "Hello Go Release Bot",
	"FIXME-release-bot@example.com", "hello-release-bot@lab.invalid",
	"FIXME-app", "my-cli",
	"FIXME-service", "my-service",
	`"ghcr.io/itiquette/nanolinter:go@sha256:PIN_DIGEST"`, "$CI_LINT_IMAGE",
	`"ghcr.io/itiquette/nanolinter:kotlin@sha256:PIN_DIGEST"`, "$CI_LINT_IMAGE",
	`"ghcr.io/itiquette/nanolinter:core@sha256:PIN_DIGEST"`, "$CI_LINT_IMAGE",
	"registry.example.com/team/check-tools@sha256:PIN_IMAGE_DIGEST", gitlabJobPlaceholder,
	// The monorepo checks each part in its runtime image: Java in the trial's
	// job image, Node in a further one.
	"ghcr.io/diggsweden/forge-ci-runtime-java-25@sha256:PIN_JAVA_IMAGE_DIGEST", gitlabJobPlaceholder,
	"ghcr.io/diggsweden/forge-ci-runtime-node-24@sha256:PIN_NODE_IMAGE_DIGEST", gitlabJobPlaceholder+"_NODE",
	"gitlab.example.com/team/ci/forge-ci", "$CI_SERVER_FQDN/itiquette/forge-ci",
	"team/ci/forge-ci", "itiquette/forge-ci",
	"/build-go@0.90.0\n    inputs:\n", "/build-go@0.90.0\n    inputs:\n      "+gitlabRuntimeInput+": "+gitlabJobPlaceholder+"\n",
	"/build-gradle@0.90.0\n    inputs:\n", "/build-gradle@0.90.0\n    inputs:\n      "+gitlabRuntimeInput+": "+gitlabJobPlaceholder+"\n",
)

// exampleDemoFiles maps a demo's CI files to the release files they are
// filled from: the example's whole directory, where a project keeps each
// file, and the changelog configuration `forge-ci init` writes.
func exampleDemoFiles(example string) map[string]string {
	dir := path.Join("examples", example)
	files := map[string]string{
		".forge-ci/artifacts.yml":                path.Join(dir, "artifacts.yml"),
		".forge-ci/gitcliff/keepachangelog.toml": "templates/gitcliff/keepachangelog.toml",
		".forge-ci/gitcliff/minimal.toml":        "templates/gitcliff/minimal.toml",
		GitLabGateFile:                           path.Join(dir, "gitlab/.gitlab-ci.yml"),
		"nanolinter.toml":                        path.Join(dir, "nanolinter.toml"),
	}

	// Only the Go examples have Forgejo workflows.
	if example == "go-cli" || example == "go-service" {
		files[".forgejo/workflows/pullrequest.yml"] = path.Join(dir, "forgejo/pullrequest.yml")
		files[".forgejo/workflows/release.yml"] = path.Join(dir, "forgejo/release.yml")
	}

	if example == "go-service" {
		files[".forge-ci/images.yml"] = path.Join(dir, "artifacts-forgejo.yml")
		files["Containerfile"] = path.Join(dir, "Containerfile.example")
		files["Containerfile.forgejo"] = path.Join(dir, "forgejo/Containerfile.example")
	}

	return files
}

// yamlComment is a comment after a value; a FIXME there explains the value
// and is kept, as an adopter would keep it.
var yamlComment = regexp.MustCompile(`(?m)[ \t]+#.*$`)

// An example demo's CI files are its example's, never assembled by hand, so
// a demo run proves the example a person copies. Rewrite them after changing
// an example with
//
//	go test ./internal/livetest -run TestExampleDemosAreTheirExamples -args -update-demos
func TestExampleDemosAreTheirExamples(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)

	for demo, example := range exampleDemos {
		t.Run(demo, func(t *testing.T) {
			t.Parallel()

			for file, from := range exampleDemoFiles(example) {
				body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(from)))
				if err != nil {
					t.Fatal(err)
				}

				want := exampleDemoValues.Replace(string(body))

				// Init's own detection, less the program names the demos
				// build under on purpose.
				for _, left := range consumerinit.Remaining(yamlComment.ReplaceAllString(want, "")) {
					if left.Token != "my-cli" && left.Token != "my-service" {
						t.Errorf("%s:%d keeps the placeholder %s", file, left.Line, left.Token)
					}
				}

				target := filepath.Join(root, "test", "live", "demos", demo, "consumer", filepath.FromSlash(file))
				if *updateDemos {
					if err = os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
						t.Fatal(err)
					}

					if err = os.WriteFile(target, []byte(want), 0o644); err != nil {
						t.Fatal(err)
					}

					continue
				}

				got, err := os.ReadFile(target)
				if err != nil || string(got) != want {
					t.Errorf("%s is not %s filled in (%v); rewrite it with -args -update-demos", file, from, err)
				}
			}
		})
	}
}
