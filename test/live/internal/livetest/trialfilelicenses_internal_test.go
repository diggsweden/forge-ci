// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/evidence"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func TestTrialInjectedFilesREUSE(t *testing.T) {
	evidence.RequireTool(t, "reuse")
	evidence.RequireTool(t, "ssh-keygen")
	signer := NewRequestSigner(t, t.TempDir())
	root := t.TempDir()
	license, err := os.ReadFile(filepath.Join(reporoot.Path(t), "LICENSES/CC0-1.0.txt"))
	if err != nil {
		t.Fatal(err)
	}
	stageTestWrite(t, root, "LICENSES/CC0-1.0.txt", string(license), 0600)
	stageTestWrite(t, root, trustTestPath, signer.AllowedSigners, 0600)
	for path, content := range NextReleaseRequestFiles() {
		stageTestWrite(t, root, path, content, 0600)
	}
	output, err := exec.CommandContext(t.Context(), "reuse", "--root", root, "lint").CombinedOutput()
	if err != nil {
		t.Fatalf("harness-injected files must pass the consumer's REUSE gate: %v\n%s", err, output)
	}
}

func TestLicensedTrialSignersPreservesSSHTrust(t *testing.T) {
	evidence.RequireTool(t, "ssh-keygen")
	dir := t.TempDir()
	signer := NewRequestSigner(t, dir)
	if got := licensedTrialSigners(signer.AllowedSigners); got != signer.AllowedSigners {
		t.Fatal("staging already licensed trust changed its bytes")
	}
	if got := licensedTrialSigners(trustTestSigners); strings.TrimPrefix(got, trialFileLicense) != trustTestSigners {
		t.Fatal("licensing changed the public signer entry")
	}
	trust, message := filepath.Join(dir, "allowed-signers"), filepath.Join(dir, "request")
	stageTestWrite(t, dir, "allowed-signers", signer.AllowedSigners, 0600)
	stageTestWrite(t, dir, "request", "release candidate\n", 0600)
	output, err := exec.CommandContext(t.Context(), "ssh-keygen", "-Y", "sign", "-f", signer.keyPath, "-n", "git", message).CombinedOutput()
	if err != nil {
		t.Fatalf("sign fixture: %v\n%s", err, output)
	}
	for _, principal := range []string{requestSigner, "foreign@localhost"} {
		cmd := exec.CommandContext(t.Context(), "ssh-keygen", "-Y", "verify", "-f", trust, "-I", principal, "-n", "git", "-s", message+".sig")
		cmd.Stdin = strings.NewReader("release candidate\n")
		output, err := cmd.CombinedOutput()
		if (err == nil) != (principal == requestSigner) {
			t.Errorf("verify principal %s: %v\n%s", principal, err, output)
		}
	}
}
