// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package pathsafe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemLayoutTargetsAreExplicit(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"/bin": "/usr/bin", "/sbin": "/usr/sbin",
		"/lib": "/usr/lib", "/lib32": "/usr/lib32",
		"/lib64": "/usr/lib64", "/libx32": "/usr/libx32",
		"/var/run": "/run", "/var/lock": "/run/lock",
		"/workspace/link": "", "/builds/project/link": "",
		"/tmp/var/run": "", "/opt/project/bin": "", "/var/link": "",
	} {
		if got := systemLayoutTarget(path); got != want {
			t.Errorf("systemLayoutTarget(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSystemLayoutAliasRequiresItsExpectedTarget(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("the positive system-layout exception requires root-owned fixtures")
	}

	for target, want := range map[string]bool{"/run": true, "../run": true, "/tmp/run": false} {
		dir := t.TempDir()
		if err := os.Symlink(target, filepath.Join(dir, "run")); err != nil {
			t.Fatal(err)
		}

		parent, err := os.Open(dir)
		if err != nil {
			t.Fatal(err)
		}

		info, err := os.Lstat(filepath.Join(dir, "run"))
		if err != nil {
			_ = parent.Close()

			t.Fatal(err)
		}

		resolved, allowed := systemLink(parent, "/var", "run", info, []string{"child"})
		_ = parent.Close()

		if allowed != want || (allowed && resolved != "/run/child") {
			t.Errorf("target %s: resolved %q, allowed %t; want %t", target, resolved, allowed, want)
		}
	}
}
