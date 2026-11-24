// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package runbyscan_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/diggsweden/forge-ci/internal/cli/runbyscan"
)

func TestInvokedFindsTheLongestKnownPath(t *testing.T) {
	t.Parallel()

	known := map[string]bool{"build go run": true, "artifact download": true, "release publish": true, "platform checkout": true, "validate container-signature": true}

	body := `
run: forge-ci build go run --enforce-application-tests="$X"
run: |
  run_forge_ci artifact download \
    --name x
  "${FORGE_CI_BIN}" --log-level debug release publish --tag v1
  ./cli-prebuilt/forge-ci platform checkout
  # consumers verify with:
  #   forge-ci validate container-signature ghcr.io/o/i@sha256:x
# forge-ci validate container-signature in a YAML comment
uses:diggsweden/forge-ci/.github/workflows/release-orchestrator.yml@v1
description: "forge-ci runtime image carrying Go"
`

	got := runbyscan.Invoked(body, known)
	want := []string{"build go run", "artifact download", "release publish", "platform checkout"}

	if !slices.Equal(got, want) {
		t.Fatalf("Invoked = %q, want %q", got, want)
	}
}

func TestUnitNamesActionsByDirectory(t *testing.T) {
	t.Parallel()

	for rel, want := range map[string]string{
		".forgejo/actions/build-go/action.yml":  ".forgejo/actions/build-go",
		".github/actions/build-darwin-cli/x.sh": ".github/actions/build-darwin-cli",
		".github/workflows/build-go.yml":        ".github/workflows/build-go.yml",
		"templates/build-go.yml":                "templates/build-go.yml",
	} {
		if got := runbyscan.Unit(rel); got != want {
			t.Errorf("Unit(%q) = %q, want %q", rel, got, want)
		}
	}
}

// A repository's own pipelines are not something a consumer calls, and a
// fork adds its own: neither may change the table.
func TestScanLeavesOutOwnPipelines(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, ".forgejo", "workflows")

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	step := []byte("jobs:\n  x:\n    steps:\n      - run: forge-ci release notes\n")
	for _, name := range []string{"sign-and-publish-release.yml", "self-release.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), step, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	found, err := runbyscan.Scan(root, []string{"release notes"})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{".forgejo/workflows/sign-and-publish-release.yml"}; !slices.Equal(found["release notes"], want) {
		t.Fatalf("release notes is run by %q, want %q", found["release notes"], want)
	}
}
