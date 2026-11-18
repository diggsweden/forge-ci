// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const verifyTokenSecret = "token-value-DO-NOT-LOG"

// verifyTokenFakeEngine records its argv, stdin and environment, then exits
// with FAKE_ENGINE_STATUS.
const verifyTokenFakeEngine = `#!/usr/bin/env bash
set -euo pipefail
printf '%s\0' "$@" >"${FAKE_ARGV_LOG:?}"
cat >"${FAKE_STDIN_LOG:?}"
env >"${FAKE_ENV_LOG:?}"
exit "${FAKE_ENGINE_STATUS:-0}"
`

// The action is one `forge-ci validate auth token`, which owns the
// redaction and the classification of a refusal. The action hands it the
// token on stdin only, never in argv or the engine's environment, and passes
// its exit status on.
func TestVerifyForgejoToken(t *testing.T) {
	t.Parallel()

	const repository = "examplescope/example"

	for _, status := range []int{0, 77, 69} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			box := newSandbox(t, "verify-forgejo-token")
			box.write(".engine/bin/forge-ci", verifyTokenFakeEngine, 0o700)

			got := box.run(actionStep(t, "verify-forgejo-token", "Verify Forgejo API token"),
				"FORGEJO_API_TOKEN="+verifyTokenSecret, "VERIFY_REPO="+repository,
				"VERIFY_SERVER=https://forgejo.invalid/", "FAKE_ENGINE_STATUS="+strconv.Itoa(status),
				"FAKE_STDIN_LOG="+filepath.Join(box.root, "stdin"), "FAKE_ENV_LOG="+filepath.Join(box.root, "env"))
			if got.status != status {
				t.Fatalf("status %d, want %d; stderr %q", got.status, status, got.stderr)
			}

			if want := []string{"validate", "auth", "token", "--repository", repository, "--token-file", "-"}; !slices.Equal(box.argv(), want) {
				t.Fatalf("argv %q, want %q", box.argv(), want)
			}

			stdin, err := os.ReadFile(filepath.Join(box.root, "stdin"))
			if err != nil || string(stdin) != verifyTokenSecret {
				t.Fatalf("engine stdin %q, %v", stdin, err)
			}

			env, err := os.ReadFile(filepath.Join(box.root, "env"))
			if err != nil {
				t.Fatal(err)
			}

			lines := strings.Split(string(env), "\n")
			if strings.Contains(string(env), verifyTokenSecret) ||
				!slices.Contains(lines, "FORGE_CI_PROVIDER=forgejo") ||
				!slices.Contains(lines, "FORGEJO_SERVER_URL=https://forgejo.invalid") {
				t.Fatalf("engine environment leaks the token or lacks the provider settings:\n%s", env)
			}

			if strings.Contains(got.stdout+got.stderr, verifyTokenSecret) {
				t.Fatalf("output exposed the token: %q %q", got.stdout, got.stderr)
			}
		})
	}
}
