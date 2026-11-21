// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build live

package conformance_test

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/test/live/internal/livetest"
)

// TestForgejoLayer_CargoCache exercises the runner's cache service across fresh
// job containers, including a workspace-only version bump and unavailable cache.
func TestForgejoLayer_CargoCache(t *testing.T) {
	livetest.RegisterAdmittedScenario(t)
	admission := requireLayerRunner(t)
	livetest.Admitted(t)
	// Runner cache entries outlive deletion of a repository with the same name.
	scenario := provisionLayerScenario(t, admission, "cargo-cache-"+strings.ToLower(rand.Text()[:10]), false, "sha1")

	probe, err := os.ReadFile(os.Getenv("FORGE_CI_LIVE_CARGO_CACHE_PROBE_BIN"))
	if err != nil {
		t.Fatal("read launcher-built Cargo cache probe: ", err)
	}

	var compressed bytes.Buffer

	zip := gzip.NewWriter(&compressed)
	if _, err := zip.Write(probe); err != nil {
		t.Fatal(err)
	}

	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}

	encodedProbe := base64.StdEncoding.EncodeToString(compressed.Bytes())

	var afterID int64

	for _, phase := range []struct{ name, version, flags, want, crates, seed string }{
		{"concurrent-save", "0.2.0", "", "Cargo download cache restored;", "memchr", "concurrent"},
		{"cold", "0.1.0", "", "Cargo download cache uploaded:", "itoa", ""},
		{"warm", "0.1.0", "", "Cargo download cache restored;", "itoa", ""},
		{"workspace-version", "0.2.0", "", "Cargo download cache restored;", "itoa", ""},
		{"dependency-change", "0.2.0", "", "Cargo download cache uploaded:", "itoa ryu", ""},
		{"dependency-warm", "0.2.0", "", "Cargo download cache restored;", "itoa ryu", ""},
		{"unavailable", "0.2.0", "ACTIONS_CACHE_URL= ACTIONS_RUNTIME_TOKEN=", "warning: Cargo download cache unavailable;", "itoa", ""},
		{"outage", "0.2.0", "ACTIONS_CACHE_URL=http://127.0.0.1:1/", "warning: Cargo download cache restore unavailable or rejected;", "itoa", ""},
		{"corruption", "0.2.0", "", "warning: Cargo download cache restore unavailable or rejected;", "ryu", "corrupt"},
	} {
		manifest, lock := cargoCacheFixture(phase.version, phase.crates)

		seed := ""
		if phase.seed != "" {
			seed = "base64 --decode cache-probe.gz.b64 | gzip -d > \"${RUNNER_TEMP}/cache-probe\"\n          chmod 500 \"${RUNNER_TEMP}/cache-probe\"\n          cargo fetch --locked\n          \"${RUNNER_TEMP}/cache-probe\" " + phase.seed + "\n          export CARGO_HOME=\"${RUNNER_TEMP}/cargo-cache-fresh\""
		}

		files := map[string]string{
			".forgejo/workflows/cargo-cache.yml": scenario.render(t, cargoCacheWorkflow, "CACHE_ENV", phase.flags, "CACHE_SEED", seed),
			"Cargo.toml":                         manifest,
			"Cargo.lock":                         lock,
			"cache-probe.gz.b64":                 encodedProbe,
			"src/lib.rs":                         "// Fetch-only witness: never compiled or executed.\n",
			"phase":                              phase.name,
		}
		livetest.CommitFiles(t, scenario.target, scenario.repo, livetest.FileCommit{Branch: layerMain, Message: "test: Cargo cache " + phase.name, Files: files})
		run := livetest.WaitForConsumerRunAfter(t, scenario.target, scenario.repo, "cargo-cache.yml", "push", afterID)
		jobs := scenario.requireStatus(t, run, jobSuccess)
		afterID = run.ID

		if len(jobs) != 1 {
			t.Fatalf("%s: expected one cache witness job, got %d", phase.name, len(jobs))
		}

		log := livetest.JobLog(t, scenario.target, scenario.repo, jobs[0])
		if !strings.Contains(log, phase.want) {
			t.Fatalf("%s: missing cache outcome %q in retained job log", phase.name, phase.want)
		}

		if (phase.name == "cold" || phase.name == "dependency-change") && !strings.Contains(log, "Cargo download cache miss;") {
			t.Fatal("fresh repository did not start with a cold cache")
		}

		if phase.name == "outage" && !strings.Contains(log, "cache save unavailable; locked fetch succeeded") {
			t.Fatal("outage did not exercise save fallback")
		}

		if phase.seed != "" {
			want := "Cargo cache probe: corrupt entry seeded"
			if phase.seed == "concurrent" {
				want = "Cargo cache probe: competing saves verified"
			}

			if !strings.Contains(log, want) {
				t.Fatalf("%s: missing seed proof", phase.name)
			}

			if phase.seed == "corrupt" && (!strings.Contains(log, "cache downloaded (pending verification):") || strings.Contains(log, "cache restored;")) {
				t.Fatal("corruption case must download and reject the seeded archive")
			}
		}

		t.Logf("Cargo cache phase=%s run=%d job=%d verified", phase.name, run.ID, jobs[0].ID)
	}
}

func cargoCacheFixture(version, crates string) (string, string) {
	manifest := fmt.Sprintf("[package]\nname = 'cache-witness'\nversion = '%s'\nedition = '2021'\n[dependencies]\n", version)

	var dependencies, packages []string

	var crateLines strings.Builder

	for name := range strings.FieldsSeq(crates) {
		crate := map[string]struct{ version, checksum string }{
			"itoa":   {"1.0.18", "8f42a60cbdf9a97f5d2305f08a87dc4e09308d1276d28c869c684d7777685682"},
			"ryu":    {"1.0.23", "9774ba4a74de5f7b1c1451ed6cd5285a32eddb5cccb8cc655a4e50009e06477f"},
			"memchr": {"2.8.3", "cf8baf1c55e62ffcace7a9f06f4bd9cd3f0c4beb022d3b367256b91b87513d98"},
		}[name]
		_, _ = fmt.Fprintf(&crateLines, "%s = '=%s'\n", name, crate.version)
		dependencies = append(dependencies, fmt.Sprintf("%q", name))
		packages = append(packages, fmt.Sprintf("[[package]]\nname = %q\nversion = %q\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = %q\n", name, crate.version, crate.checksum))
	}

	manifest += crateLines.String()

	lock := fmt.Sprintf("version = 4\n\n[[package]]\nname = \"cache-witness\"\nversion = %q\ndependencies = [%s]\n\n%s", version, strings.Join(dependencies, ", "), strings.Join(packages, "\n"))

	return manifest, lock
}

const cargoCacheWorkflow = `name: Cargo download cache witness
on:
  push:
    branches: [main]
jobs:
  fetch:
    runs-on: ${{ fromJSON(vars.CI_RUNNER_LABEL) }}
    timeout-minutes: 15
    container:
      image: docker.io/library/rust@sha256:c1e5f19e773b7878c3f7a805dd00a495e747acbdc76fb2337a4ebf0418896b33
    steps:
      - uses: https://FORGE_HOST/OWNER/forge-ci/.forgejo/actions/checkout-consumer@PIN_FORGE_CI_SHA
      - id: setup
        uses: https://FORGE_HOST/OWNER/forge-ci/.forgejo/actions/setup-forge-ci@PIN_FORGE_CI_SHA
        with:
          expected-sha256: RENDER_FORGE_CI_SHA256
          add-to-path: 'false'
      - name: Fetch locked crates through native cache
        env:
          FORGE_CI_BIN: ${{ steps.setup.outputs.binary-path }}
        run: |
          set -euo pipefail
          export CARGO_HOME="${RUNNER_TEMP}/cargo-cache-witness"
          CACHE_SEED
          CACHE_ENV "${FORGE_CI_BIN}" build cargo fetch --cache
`
