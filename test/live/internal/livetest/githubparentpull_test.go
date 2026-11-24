// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build live

package livetest

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// This is transport/action acceptance, not authentication of a producer's
// release evidence. The projection is a trusted fixture pinned to a public image.
func TestGitHubPublicParentPull(t *testing.T) {
	RegisterLiveScenario(t)

	if os.Getenv(GitHubContextEnv) == "" || os.Getenv("FORGE_CI_LIVE_BIN") == "" {
		t.Skip("select test-live-github-parent-pull inside Forge Lab's hosted session")
	}

	id, err := strconv.ParseInt(os.Getenv("FORGE_CI_LIVE_GITHUB_ORG_ID"), 10, 64)
	if err != nil || id <= 0 {
		t.Fatal("explicit hosted organization ID is required")
	}

	trial := OpenGitHubTrial(t, id)
	repo := minimalRepository(t, trial, "public-parent-pull")

	source := trial.tempDir("public-parent-pull")
	for _, name := range []string{".github/actions/fetch-parent-image/action.yml", ".engine/bin/lib-parent-pull.sh"} {
		minimalCopy(t, filepath.Join(reporoot.Path(t), name), filepath.Join(source, name))
	}

	binary, err := os.ReadFile(os.Getenv("FORGE_CI_LIVE_BIN"))
	if err != nil {
		t.Fatal(err)
	}

	minimalCopy(t, os.Getenv("FORGE_CI_LIVE_BIN"), filepath.Join(source, "fixture-engine"))

	workflow := strings.ReplaceAll(publicParentPullWorkflow, "ENGINE_SHA256", fmt.Sprintf("%x", sha256.Sum256(binary)))
	minimalWrite(t, source, ".github/workflows/parent-pull.yml", workflow)
	trial.git(source, "init", "--quiet", "--object-format=sha1", "--initial-branch=main")
	trial.git(source, "add", "--all")
	trial.commit(source, "test: public anonymous parent pull", "")
	commit := strings.TrimSpace(trial.git(source, "rev-parse", "HEAD"))
	trial.push(source, trial.remote(repo), "HEAD:refs/heads/main")
	run := trial.WaitRun(repo, "parent-pull.yml", "push", commit, 15*time.Minute)
	trial.ReportRun(repo, run)

	if run.GetConclusion() != "success" {
		t.Fatalf("public parent pull run %d: %s", run.GetID(), run.GetConclusion())
	}

	if count := trial.checkDevelopmentArtifacts(repo, run.GetID()); count != 1 {
		t.Fatalf("expected one read-back evidence artifact, got %d", count)
	}
}

const publicParentPullWorkflow = `name: Public parent pull
on: push
permissions:
  contents: read
jobs:
  pull:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    env:
      # Official BusyBox 1.37.0 multi-platform index; no project-owned registry.
      PARENT: docker.io/library/busybox@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262
      - name: Admit fixture binary and parent projection
        id: fixture
        shell: bash
        run: |
          set -euo pipefail
          echo 'ENGINE_SHA256  fixture-engine' | sha256sum --check -
          chmod 0700 fixture-engine
          build=$(jq -cn --arg ref "$PARENT" '{name:"child",arch:"amd64",platform:"linux/amd64","build-args":("PARENT_IMAGE="+$ref),"registry-inputs":{PARENT_IMAGE:$ref}}')
          parent=$(jq -cn --arg ref "$PARENT" '{ref:$ref,input_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}')
          printf 'build=%s\nparent=%s\n' "$build" "$parent" >> "$GITHUB_OUTPUT"
      - uses: ./.github/actions/fetch-parent-image
        with:
          engine: ./fixture-engine
          build-json: ${{ steps.fixture.outputs.build }}
          parent-base-json: ${{ steps.fixture.outputs.parent }}
          release-sha: ${{ github.sha }}
          inputs-dir: pulled
      - name: Check layout and reject a mismatched projection
        env:
          BUILD: ${{ steps.fixture.outputs.build }}
          PROJECTION: ${{ steps.fixture.outputs.parent }}
        shell: bash
        run: |
          set -euo pipefail
          test -s pulled/PARENT_IMAGE/index.json
          jq -e '.imageLayoutVersion == "1.0.0"' pulled/PARENT_IMAGE/oci-layout
          wrong=$(jq -c '.ref |= sub("sha256:[0-9a-f]+$"; "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")' <<< "$PROJECTION")
          if ./fixture-engine container release-images fetch-inputs --build-json "$BUILD" --parent-base-json "$wrong" --release-sha "$GITHUB_SHA" --server-url "$GITHUB_SERVER_URL" --repository "$GITHUB_REPOSITORY" --inputs-dir refused; then
            exit 1
          fi
          test ! -e refused
      - uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02
        with:
          name: parent-pull-evidence
          path: |
            pulled/inputs.json
            pulled/PARENT_IMAGE/index.json
            pulled/PARENT_IMAGE/oci-layout
          retention-days: 1
`
