// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaseLifecycleExposesEvidenceOnlyAfterExactAuthentication(t *testing.T) {
	t.Parallel()
	run := workflowStep(t, "base-image-lifecycle.yml", "authenticate", "Authenticate promoted base").Run
	for _, mode := range []string{"success", "missing-ref", "missing-ledger", "missing-token", "login-failure", "verification-failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			box := newSandbox(t, "base-lifecycle")
			box.write(".engine/bin/forge-ci", `#!/usr/bin/env bash
set -euo pipefail
test -z "${REGISTRY_TOKEN:-}"
case "$1 $2" in
'container login')
  test "$(cat)" = fixture-token
  test "$MODE" != login-failure || exit 73
  ;;
'container base-images')
  test "$3" = authenticate-parent
  test "$4" = --auth-file
  test "$6" = --expected-ref
  test "$7" = 'registry.example/owner/base@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
  test "$8" = --base-inputs-json
  test "$9" = '[{"flavor":"base","base_input_id":"independent"}]'
  test "$MODE" != verification-failure || exit 74
  printf '%s\n' '{"ref":"authenticated","input_id":"independent"}'
  ;;
*) exit 99 ;;
esac
`, 0o700)
			for _, dir := range []string{"private", "consumer"} {
				if err := os.Mkdir(filepath.Join(box.root, dir), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ref, ledger, token := "registry.example/owner/base@sha256:"+strings.Repeat("a", 64), `[{"flavor":"base"}]`, "fixture-token"
			switch mode {
			case "missing-ref":
				ref = ""
			case "missing-ledger":
				ledger = ""
			case "missing-token":
				token = ""
			}
			out := filepath.Join(box.root, "output")
			box.write("output", "", 0o600)
			got := box.run(run, "HELPERS_DIR="+box.root, "SIGNER_WORKSPACE="+filepath.Join(box.root, "private"),
				"CONSUMER_WORKSPACE="+filepath.Join(box.root, "consumer"), "MODE="+mode, "FORGEJO_OUTPUT="+out,
				"TRUSTED_KEY_INPUT=fixture-key", "TRUSTED_KEY_SHA256_INPUT=fixture-digest", "EXPECTED_REF="+ref, "LEDGER_JSON="+ledger,
				`BASE_INPUTS_JSON=[{"flavor":"base","base_input_id":"independent"}]`,
				"EXPECTED_REPOSITORY=registry.example/owner/base", "FORGEJO_SERVER=https://forge.example", "FORGEJO_REPO=owner/app",
				"REGISTRY_USER=owner", "REGISTRY_TOKEN="+token)
			body, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "success" {
				want := "ledger_json=" + ledger + "\nparent_base_json={\"ref\":\"authenticated\",\"input_id\":\"independent\"}\n"
				if got.status != 0 || string(body) != want {
					t.Fatalf("status=%d output=%s stderr=%s", got.status, body, got.stderr)
				}
			} else if got.status == 0 || len(body) != 0 {
				t.Fatalf("%s exposed unverified output: status=%d output=%s", mode, got.status, body)
			}
			if strings.Contains(got.stdout+got.stderr, "fixture-token") {
				t.Fatal("credential escaped into diagnostics")
			}
		})
	}
}
