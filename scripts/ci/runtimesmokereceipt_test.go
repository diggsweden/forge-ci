// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package ci

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func TestRuntimeSmokeReceiptBindsCandidateIdentity(t *testing.T) {
	t.Parallel()

	for _, changed := range []string{"valid", "source", "target", "arch", "ref", "missing", "malformed"} {
		t.Run(changed, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			ref := "registry.example/acme/runtime@sha256:" + strings.Repeat("b", 64)

			receipt := map[string]string{"source": strings.Repeat("a", 40), "target": "full", "arch": "amd64", "ref": ref}
			if _, exists := receipt[changed]; exists {
				receipt[changed] = "different"
			}

			body, err := json.Marshal(receipt)
			require.NoError(t, err)

			if changed == "malformed" {
				body = []byte("not json")
			}

			if changed != "missing" {
				require.NoError(t, os.WriteFile(filepath.Join(root, "receipt.json"), body, 0o600))
			}

			tool := filepath.Join(root, "forge-ci")
			require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\nset -eu\ntest \"$1 $2 $3 $4 $5\" = 'artifact download --name runtime-smoke-full-amd64 --dir'\nmkdir -p \"$6\"\ncp \"$RECEIPT\" \"$6/receipt.json\"\n"), 0o600))
			require.NoError(t, os.Chmod(tool, 0o700))
			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(reporoot.Path(t), "scripts/ci/runtime-images.sh"), "verify-smoke", "full", "amd64", ref)
			cmd.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"), "RECEIPT="+filepath.Join(root, "receipt.json"), "SOURCE_SHA="+strings.Repeat("a", 40), "RUNNER_TEMP="+root)

			out, err := cmd.CombinedOutput()
			if changed == "valid" {
				require.NoError(t, err, "%s", out)
			} else {
				require.Error(t, err, "%s", out)
			}

			leftovers, err := filepath.Glob(filepath.Join(root, "runtime-smoke-check.*"))
			require.NoError(t, err)
			require.Empty(t, leftovers)
		})
	}
}
