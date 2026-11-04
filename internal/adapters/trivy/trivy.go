// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package trivy wraps the system `trivy` binary. The runtime image
// bakes trivy in; local runs need it on PATH.
package trivy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"

	"github.com/diggsweden/forge-ci/internal/safeexec"
)

// Adapter wraps the trivy binary. Bin is overridable for tests.
type Adapter struct {
	Bin string // empty → "trivy"
}

// New returns an Adapter using the system trivy.
func New() *Adapter { return &Adapter{} }

// runtimeKeep is the whole environment trivy is given. Every scan the engine
// runs gates or records a release, and trivy takes most of its settings from
// TRIVY_* variables and from trivy.yaml, .trivyignore and trivy-secret.yaml
// in its working directory. A forge injects project and group variables into
// every job, and the checkout is the working directory, so either route could
// skip directories, swap the vulnerability database, add an ignore policy or
// VEX statements, and pass a gate the engine's own flags describe.
//
// Kept: tool lookup, home and temporary storage, the database cache
// (XDG_CACHE_HOME; trivy's default cache lives under it or HOME), registry
// credentials the job logged in with (DOCKER_CONFIG, REGISTRY_AUTH_FILE and
// XDG_RUNTIME_DIR for the containers auth file), and the network route and
// trust a proxied or private-CA runner needs. TRIVY_PLATFORM is the one trivy
// setting kept: the multi-arch container build selects each leg's platform
// with it. GITHUB_TOKEN is not kept: trivy would send it to ghcr.io, and on a
// Forgejo job it holds the forge token.
var runtimeKeep = map[string]bool{
	"PATH":               true,
	"HOME":               true,
	"TMPDIR":             true,
	"XDG_CACHE_HOME":     true,
	"XDG_RUNTIME_DIR":    true,
	"DOCKER_CONFIG":      true,
	"REGISTRY_AUTH_FILE": true,
	"SSL_CERT_FILE":      true,
	"SSL_CERT_DIR":       true,
	"HTTP_PROXY":         true,
	"HTTPS_PROXY":        true,
	"NO_PROXY":           true,
	"http_proxy":         true,
	"https_proxy":        true,
	"no_proxy":           true,
	"TRIVY_PLATFORM":     true,
}

// RunInherit invokes `trivy` with args, streaming stdout/stderr, and returns
// the process exit code and any error from starting or waiting.
//
// Trivy runs with only runtimeKeep from the environment, with configuration
// file loading disabled, in a private empty working directory, so nothing in
// the caller's directory is discovered. Path arguments must therefore be
// absolute.
func (a *Adapter) RunInherit(ctx context.Context, stdout, stderr io.Writer, args ...string) (int, error) {
	dir, err := os.MkdirTemp("", "forge-ci-trivy-*")
	if err != nil {
		return -1, fmt.Errorf("create trivy working directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	cmd := safeexec.Command(ctx, a.bin(), slices.Concat(args, []string{"--config="})...)
	cmd.Env = safeexec.EnvKeep(os.Environ(), func(name string) bool { return runtimeKeep[name] })
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	if err == nil {
		return 0, nil
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), nil
	}

	// A non-exit failure (most commonly trivy missing from PATH) is an
	// external-dependency problem, not an internal bug — classify it so it
	// surfaces as EX_UNAVAILABLE (69) rather than the unclassified
	// EX_SOFTWARE (70).
	return -1, safeexec.WrapError(err, a.bin(), safeexec.FirstArg(args))
}

func (a *Adapter) bin() string {
	if a.Bin != "" {
		return a.Bin
	}

	return "trivy"
}
