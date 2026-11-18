// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/consumerinit"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// githubExampleRuntimeImage is a forge-ci runtime image a GitHub example
// names, with the family suffix and whatever follows the repository.
var githubExampleRuntimeImage = regexp.MustCompile(`ghcr\.io/diggsweden/forge-ci-runtime((?:-[a-z0-9.]+)*)([@:][^\s"']*)?`)

// Every workflow admits its runtime images by digest only, so a tag in an
// example fails the adopter's first run, and the live trial, which pins the
// engine's own digests, would not show it. A zero digest is no better:
// doctor could not see it. Each image is a PIN_<FAMILY>_IMAGE_DIGEST
// placeholder, which init's scan reports until the person fills it in.
func TestGitHubExamplesPinRuntimeImagesByPlaceholder(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	images := 0

	err := filepath.WalkDir(filepath.Join(root, "examples"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || filepath.Ext(path) != ".yml" ||
			!slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "github") {
			return walkErr
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		for number, line := range strings.Split(string(body), "\n") {
			for _, match := range githubExampleRuntimeImage.FindAllStringSubmatch(line, -1) {
				images++

				family := "FULL"
				if match[1] != "" {
					family = strings.ToUpper(strings.Split(match[1], "-")[1])
				}

				token := "PIN_" + family + "_IMAGE_DIGEST"
				if match[2] != "@sha256:"+token {
					t.Errorf("%s:%d names %s; pin it as @sha256:%s", path, number+1, match[0], token)
				} else if !slices.Contains(consumerinit.Remaining(line), consumerinit.Placeholder{Token: token, Line: 1}) {
					t.Errorf("%s:%d: init's placeholder scan does not report %s", path, number+1, token)
				}
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if images == 0 {
		t.Fatal("no GitHub example names a runtime image; the walk lost the examples")
	}
}

func TestGitHubCosignExampleReportsItsUnfilledTrustPin(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(reporoot.Path(t), "examples/signing/cosign-key/github/release-workflow.yml"))
	if err != nil {
		t.Fatal(err)
	}

	const token = "PIN_GITHUB_COSIGN_PUBLIC_KEY_SHA256"
	if !strings.Contains(string(body), "cosign-public-key-sha256: "+token) {
		t.Fatal("the example must leave a detectable trust-pin placeholder, not a valid-looking digest")
	}

	if !slices.ContainsFunc(consumerinit.Remaining(string(body)), func(p consumerinit.Placeholder) bool { return p.Token == token }) {
		t.Fatal("init and doctor cannot report the unfilled example trust pin")
	}
}

func TestArtifactExampleDocumentationIsPinnedByInit(t *testing.T) {
	t.Parallel()

	for _, example := range []string{"go-cli", "gradle-app", "signing/cosign-key", "signing/openbao-kms", "signing/sigstore-keyless"} {
		body, err := os.ReadFile(filepath.Join(reporoot.Path(t), "examples", example, "artifacts.yml"))
		if err != nil {
			t.Fatal(err)
		}

		const prefix = "https://github.com/diggsweden/forge-ci/blob/"
		if strings.Contains(string(body), prefix+"main/") || !strings.Contains(string(body), prefix+"PIN_FORGE_CI_SHA/") {
			t.Errorf("%s documentation must follow the source commit init pins, not mutable main", example)
		}
	}
}
