// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// LauncherHostTools are the pinned host tools the live launcher checks before
// it builds anything; the scenarios run them on this host.
var LauncherHostTools = []string{"base64", "cosign", "flock", "jq", "syft", "buildah", "git", "sha256sum"}

// miseWhichTimeout bounds `mise which`, which only reads local state.
const miseWhichTimeout = 30 * time.Second

const miseCommand = "mise"

// HostTool returns the real executable a scenario runs for name, failing the
// test when there is none. Use it for every host tool instead of exec.LookPath.
func HostTool(tb TB, name string) string {
	tb.Helper()

	path, err := ResolveHostTool(Context(tb), name)
	if err != nil {
		tb.Fatalf("livetest: %v", err)
	}

	return path
}

// CheckHostTools resolves every name and reports the ones that do not resolve
// together, so one refusal lists all of them.
func CheckHostTools(ctx context.Context, names []string) error {
	var problems []error

	for _, name := range names {
		if _, err := ResolveHostTool(ctx, name); err != nil {
			problems = append(problems, err)
		}
	}

	return errors.Join(problems...)
}

// ResolveHostTool finds name on PATH and returns the executable that actually
// runs. A mise shim is on PATH even when the version it should run is not
// installed, and copying or hashing it yields the mise binary, so a shim is
// resolved with `mise which` against the repository's pins.
func ResolveHostTool(ctx context.Context, name string) (string, error) {
	return resolveHostTool(ctx, name, repositoryDir())
}

func resolveHostTool(ctx context.Context, name, dir string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is required and is not on PATH: %w", name, errs.ErrValidation)
	}

	if mise, shim := miseShim(path); shim {
		path, err = miseWhich(ctx, mise, name, dir)
		if err != nil {
			return "", fmt.Errorf("%s on PATH is a mise shim with nothing installed behind it (%w); run mise install && mise reshim: %w", name, err, errs.ErrValidation)
		}
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s resolves to %s, which cannot be read: %w", name, path, err)
	}

	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s resolves to %s, which is not an executable regular file: %w", name, resolved, errs.ErrValidation)
	}

	return resolved, nil
}

// miseShim reports whether path is a mise shim, and the mise binary to ask.
// Shims live in mise's shims directory and are, by default, symlinks to mise.
func miseShim(path string) (string, bool) {
	target, err := filepath.EvalSymlinks(path)
	if err == nil && filepath.Base(target) == miseCommand && filepath.Base(path) != miseCommand {
		return target, true
	}

	dir := filepath.Dir(path)
	if filepath.Base(dir) == "shims" && filepath.Base(filepath.Dir(dir)) == miseCommand {
		return miseCommand, true
	}

	return "", false
}

func miseWhich(ctx context.Context, mise, name, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, miseWhichTimeout)
	defer cancel()

	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, mise, "which", name) //nolint:gosec // G204: the shim's own mise and a fixed tool name.
	cmd.Dir = dir
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("mise which %s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}

	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("mise which %s printed %q, not an absolute path: %w", name, path, errs.ErrValidation)
	}

	return path, nil
}

// repositoryDir is the checkout holding .mise.toml above the working
// directory, where the pins live; the working directory when there is none.
func repositoryDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".mise.toml")); err == nil {
			return dir
		}

		if filepath.Dir(dir) == dir {
			return cwd
		}
	}
}
