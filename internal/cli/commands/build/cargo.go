// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package build

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/diggsweden/forge-ci/internal/adapters/actionscache"
	"github.com/diggsweden/forge-ci/internal/adapters/cargo"
	"github.com/diggsweden/forge-ci/internal/adapters/cargocache"
	appbuild "github.com/diggsweden/forge-ci/internal/app/build"
	"github.com/diggsweden/forge-ci/internal/cli/cienv"
	"github.com/diggsweden/forge-ci/internal/cli/deps"
	"github.com/diggsweden/forge-ci/internal/domain/build"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func cargoCmd() *cli.Command {
	return &cli.Command{
		Name:  "cargo",
		Usage: "Cargo build helpers (artifact-first Rust projects)",
		Commands: []*cli.Command{
			cargoRunCmd(),
			cargoFetchCmd(),
		},
	}
}

func cargoFetchCmd() *cli.Command {
	return &cli.Command{
		Name: "fetch", Usage: "fetch locked Cargo dependencies without building or running tests",
		Description: "Runs cargo fetch --locked once by default. Explicit bounded retries use cancellation-aware linear waits; no compilation, artifact publication or signing is retried.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: flagWorkingDir, Value: ".", Sources: cli.EnvVars("WORKING_DIRECTORY"), Usage: usageCargoDirectory},
			&cli.IntFlag{Name: "attempts", Value: 1, Usage: "dependency fetch attempts (1–10)"},
			&cli.DurationFlag{Name: "retry-delay", Value: 2 * time.Second, Usage: "linear retry delay (0–1m)"},
			&cli.BoolFlag{Name: "cache", Usage: "reuse checksum-verified crates.io downloads via the Forgejo Actions v1 cache (best effort, no Node)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			in := appbuild.CargoFetchInput{Dir: cmd.String(flagWorkingDir), Attempts: cmd.Int("attempts"), RetryDelay: cmd.Duration("retry-delay")}
			if cmd.Bool("cache") {
				cache, err := cargoFetchCache(ctx, in.Dir, cargo.New())
				if err != nil {
					_, _ = fmt.Fprintln(os.Stderr, "warning: Cargo download cache unavailable; using locked fetch")
				} else {
					in.Cache = cache
				}
			}

			return appbuild.CargoFetchWithRetry(ctx, cargo.New(), os.Stderr, os.Stderr, in)
		},
	}
}

func cargoFetchCache(ctx context.Context, dir string, tool appbuild.CargoTool) (*cargocache.Cache, error) {
	// The v1 transport must not guess at GitHub's separate v2 service protocol.
	if os.Getenv("ACTIONS_CACHE_SERVICE_V2") != "" && os.Getenv("ACTIONS_CACHE_SERVICE_V2") != "false" {
		return nil, fmt.Errorf("%w: Actions cache v2 is not supported by this transport", errs.ErrInvalidConfig)
	}

	transport := actionscache.Client{URL: os.Getenv("ACTIONS_CACHE_URL"), Token: os.Getenv("ACTIONS_RUNTIME_TOKEN"), Log: os.Stderr}
	if err := transport.Validate(); err != nil {
		return nil, err
	}

	home := os.Getenv("CARGO_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}

		home = filepath.Join(userHome, ".cargo")
	} else if !filepath.IsAbs(home) {
		home = filepath.Join(dir, home)
	}

	var manifest bytes.Buffer
	if err := tool.Run(ctx, appbuild.CargoRunInput{Dir: dir, Args: []string{"locate-project", "--workspace", "--message-format", "plain"}, Stdout: &manifest, Stderr: os.Stderr}); err != nil {
		return nil, err
	}

	lockfile := filepath.Join(filepath.Dir(strings.TrimSpace(manifest.String())), "Cargo.lock")

	return cargocache.New(lockfile, home, transport)
}

// cargoRunCmd runs the whole Cargo release build in one step (the Cargo sibling
// of `build go run`).
func cargoRunCmd() *cli.Command {
	return &cli.Command{
		Name:  subCmdRun,
		Usage: "run the full Cargo release build (metadata, fetch, test, SBOM, compile, status)",
		Description: `Runs the whole Cargo build sequence in one step: resolve metadata, fetch
    deps, run mandatory native tests under the admin enforcement policy, generate the Build SBOM with cargo-cyclonedx
   (unless --no-build-sbom), and cross-compile per platform into dist/. The
   forge job wraps this with checkout + artifact upload only.

EXAMPLE:
   forge-ci build cargo run --working-dir . --platforms linux/amd64,linux/arm64 --version 1.2.3`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: flagWorkingDir, Value: ".", Sources: cli.EnvVars("WORKING_DIRECTORY"), Usage: usageCargoDirectory},
			&cli.StringFlag{Name: flagArtifactName, Sources: cli.EnvVars("ARTIFACT_NAME"), Usage: "explicit artifact-name override (also names the SBOM)"},
			&cli.StringFlag{Name: flagBinaryName, Sources: cli.EnvVars("BINARY_NAME"), Usage: "explicit binary name (defaults to the Cargo.toml [[bin]] target or package name)"},
			&cli.StringFlag{Name: flagVersion, Sources: cli.EnvVars("VERSION"), Usage: "release version (defaults to --ref-name, then Cargo.toml; 'dev' when none)"},
			&cli.StringFlag{Name: flagRefName, Sources: cienv.RefName(), Usage: usageVersionRef},
			&cli.StringFlag{Name: "commit", Sources: cienv.Commit(), Usage: "prepared release commit exposed as FORGE_CI_COMMIT during compilation"},
			&cli.StringFlag{Name: flagPlatforms, Value: build.DefaultPlatform, Sources: cli.EnvVars("PLATFORMS"), Usage: "comma/space/newline-separated GOOS/GOARCH targets to cross-compile (each must have a Rust target triple mapping)"},
			&cli.BoolFlag{Name: flagSkipTests, Sources: cli.EnvVars("SKIP_TESTS"), Usage: usageSkipTestTask},
			&cli.StringFlag{Name: flagTestSelectionJSON, Usage: "administrator-owned individual Cargo bin/test executables with explicit application/policy gate (policy always blocks)"},
			&cli.BoolFlag{Name: flagBuildSBOM, Value: true, Sources: cli.EnvVars("ENABLE_BUILD_SBOM"), Usage: "generate the cargo-cyclonedx Build SBOM (default true)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return deps.FromCmd(ctx, cmd, func(d *deps.Deps) error {
				return appbuild.CargoReleaseBuild(ctx, d.SummarySink, cargo.New(), os.Stderr, os.Stderr, appbuild.CargoReleaseBuildInput{
					Dir:               cmd.String(flagWorkingDir),
					ArtifactName:      cmd.String(flagArtifactName),
					SkipTests:         cmd.Bool(flagSkipTests),
					TestSelectionJSON: cmd.String(flagTestSelectionJSON),
					EnableBuildSBOM:   cmd.Bool(flagBuildSBOM),
					BinaryName:        cmd.String(flagBinaryName),
					Version:           cmd.String(flagVersion),
					RefName:           cmd.String(flagRefName),
					Commit:            cmd.String("commit"),
					Platforms:         cmd.String(flagPlatforms),
				})
			})
		},
	}
}
