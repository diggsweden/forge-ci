// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabbehavior

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// The check and release jobs run the commands their caller lists, which call
// forge-ci by name. forge_ci_admin_path puts a forge-ci first on PATH that
// runs the verified engine with the administrator's four settings as flags,
// ahead of the command's own arguments, and admits only true or false.
func TestAdminPathRunsTheEngineWithTheAdministratorSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	engine := filepath.Join(dir, "engine")

	if err := os.WriteFile(engine, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	run := func(settings ...string) (string, error) {
		script := `set -euo pipefail
source "$1"
HELPERS_ENGINE_VERIFIED="$2"
forge_ci_admin_path "$3/admin-bin" "${@:4}"
forge-ci build maven run --working-dir 'a b'
`
		args := append([]string{"--noprofile", "--norc", "-c", script, "admin-path", filepath.Join(reporoot.Path(t), ".engine/bin/lib-forge-ci.sh"), engine, dir}, settings...)
		command := exec.CommandContext(t.Context(), "/bin/bash", args...)
		command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir}

		output, err := command.CombinedOutput()

		return string(output), err
	}

	output, err := run("false", "true", "false", "true")
	want := "--enforce-lint=false\n--enforce-vulnerabilities=true\n--enforce-application-tests=false\n--enforce-reproducible-builds=true\nbuild\nmaven\nrun\n--working-dir\na b\n"

	if err != nil || output != want {
		t.Fatalf("the job's forge-ci ran %q (%v), want %q", output, err, want)
	}

	for _, settings := range [][]string{{"false", "true", "false"}, {"false", "true", "false", "yes"}, {"FALSE", "true", "true", "true"}, {"", "true", "true", "true"}} {
		if output, err := run(settings...); err == nil || strings.Contains(output, "--enforce-") {
			t.Errorf("settings %q were admitted: %s", settings, output)
		}
	}
}
