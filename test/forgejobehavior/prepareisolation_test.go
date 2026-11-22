// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"slices"
	"testing"
)

// prepare-release.yml checks the caller's workflows itself, right after it
// checks the consumer out in the plan job and before any job that holds a
// key: the pull-request gate can be skipped by a direct push, the release
// cannot. The subject is the repository the layer's files were fetched
// from, the contract root the helpers at the caller's pin, and a refusal
// stops the release.
func TestPrepareReleaseChecksTheCallersIsolation(t *testing.T) {
	t.Parallel()

	var document struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	loadYAML(t, ".forgejo/workflows/prepare-release.yml", &document)

	steps := document.Jobs["plan"].Steps
	checkout := slices.IndexFunc(steps, func(s step) bool { return s.Name == "Checkout consumer repository at release request tag" })

	if checkout < 0 || checkout+1 >= len(steps) || steps[checkout+1].Name != "Check release isolation of the caller's workflows" {
		t.Fatal("the plan job must check the caller's release isolation right after the consumer checkout")
	}

	check := steps[checkout+1]
	if check.If != "" || check.Env["FORGE_CI_REPOSITORY_URL"] != "${{ needs.coordinates.outputs.helpers_repository }}" {
		t.Fatalf("the check must always run against the validated helpers repository: if %q env %q", check.If, check.Env)
	}

	want := []string{
		"validate", "isolation", "--workflows-dir", ".forgejo/workflows",
		"--single-pin-subject", "https://forge.example/owner/engine.git", "--contract-root", ".forge-ci-helpers",
	}

	for status, wantStatus := range map[string]int{"0": 0, "1": 1} {
		box := newSandbox(t, "prepare-release")
		box.write(".forge-ci-helpers/.engine/bin/lib-forge-ci.sh", fakeResolver, 0o600)
		box.write(".forge-ci-helpers/.engine/bin/forge-ci", fakeEngine, 0o700)

		got := box.run(check.Run, "FORGE_CI_REPOSITORY_URL=https://forge.example/owner/engine.git", "FAKE_ENGINE_STATUS="+status)
		if got.status != wantStatus || !slices.Equal(box.argv(), want) {
			t.Fatalf("status %d argv %q, want %d %q; stderr %s", got.status, box.argv(), wantStatus, want, got.stderr)
		}
	}

	box := newSandbox(t, "prepare-release")
	box.write(".forge-ci-helpers/.engine/bin/lib-forge-ci.sh", fakeResolver, 0o600)
	box.write(".forge-ci-helpers/.engine/bin/forge-ci", fakeEngine, 0o700)

	if got := box.run(check.Run); got.status == 0 || box.argv() != nil {
		t.Fatalf("an unset helpers repository reached the engine: status %d argv %q", got.status, box.argv())
	}
}
