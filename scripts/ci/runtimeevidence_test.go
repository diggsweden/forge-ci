// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func TestRuntimeEvidenceRequiresReadableIdenticalStoredFiles(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"valid", "unreadable", "missing", "modified", "extra", "hidden_extra"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for _, dir := range []string{"tools", "source/nested", "temporary"} {
				require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o700))
			}

			source := filepath.Join(root, "source")
			require.NoError(t, os.WriteFile(filepath.Join(source, "index.json"), []byte("original\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(source, "nested/empty"), nil, 0o600))
			tool := filepath.Join(root, "tools/forge-ci")
			require.NoError(t, os.WriteFile(tool, []byte(runtimeEvidenceStoreDouble), 0o600))
			require.NoError(t, os.Chmod(tool, 0o700))

			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(reporoot.Path(t), "scripts/ci/runtime-evidence.sh"), "runtime-evidence-full", source)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "tools")+":"+os.Getenv("PATH"), "RUNNER_TEMP="+filepath.Join(root, "temporary"), "STORE="+filepath.Join(root, "store"), "MODE="+mode)

			out, err := cmd.CombinedOutput()
			if mode == "valid" {
				require.NoError(t, err, "%s", out)
				require.Contains(t, string(out), "Verified stored evidence: runtime-evidence-full")
			} else {
				require.Error(t, err, "%s", out)
				require.Contains(t, string(out), "runtime-evidence-full")
				require.Contains(t, string(out), "assembly is incomplete")
				require.NotContains(t, string(out), "Verified stored evidence:")
			}

			remaining, err := os.ReadDir(filepath.Join(root, "temporary"))
			require.NoError(t, err)
			// Cleanup removes only the private read-back directory, not the source
			// or stored artifact needed for diagnosing a failed handoff.
			require.Empty(t, remaining)

			body, err := os.ReadFile(filepath.Join(source, "index.json"))
			require.NoError(t, err)
			require.Equal(t, "original\n", string(body))
			require.FileExists(t, filepath.Join(root, "store/index.json"))
		})
	}
}

const runtimeEvidenceStoreDouble = `#!/bin/sh
set -eu
test "$1" = artifact
test "$3" = --name
test "$4" = runtime-evidence-full
test "$5" = --dir
case "$2" in
upload)
  cp -R "$6" "$STORE"
  ;;
download)
  test -d "$STORE"
  test ! -e "$6"
  if [ "$MODE" = unreadable ]; then
    echo 'HTTP 500: dependency unavailable' >&2
    exit 69
  fi
  cp -R "$STORE" "$6"
  case "$MODE" in
    missing) rm "$6/nested/empty" ;;
    modified) printf substituted >"$6/index.json" ;;
    extra) printf unexpected >"$6/unexpected" ;;
    hidden_extra) printf unexpected >"$6/.unexpected" ;;
  esac
  ;;
*) exit 99 ;;
esac
`
