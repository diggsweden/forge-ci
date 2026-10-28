// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package kaniko

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/mockbinary"
)

// fakeExecutor stands in for /kaniko/executor: it prints its argv and the
// credentials directory kaniko would read, and records the config.json it finds
// there, so the test sees what kaniko would have seen.
func fakeExecutor(t *testing.T) *Adapter {
	t.Helper()

	bins := mockbinary.New(t)
	bins.Add("executor", `printf 'ARGV %s\n' "$*"; printf 'DOCKER_CONFIG=%s\n' "${DOCKER_CONFIG:-<unset>}"; if [ -n "${DOCKER_CONFIG:-}" ]; then printf 'CONFIG %s\n' "$(cat "$DOCKER_CONFIG/config.json")"; fi`)

	return &Adapter{Executor: bins.Path("executor"), euid: func() int { return 0 }}
}

func TestBuildToLayout_ArgvShape(t *testing.T) {
	t.Parallel()

	adapter := fakeExecutor(t)
	authFile := filepath.Join(t.TempDir(), "auth.json")

	if err := os.WriteFile(authFile, []byte(`{"auths":{"ghcr.io":{"auth":"Y2FuYXJ5"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	layout := filepath.Join(t.TempDir(), "layout")

	var log strings.Builder

	err := adapter.BuildToLayout(t.Context(), container.BuildRequest{
		AuthFile: authFile, Context: ".", Containerfile: "Containerfile", Target: "runtime", Platform: "linux/arm64",
		BuildArgs: []string{"VERSION=1.2.3"}, Labels: []string{"org.opencontainers.image.revision=abc"},
		SourceDateEpoch: "1700000000", CacheRepo: "ghcr.io/org/cache", CachePush: false,
		Mode: container.BuildModePushByDigest, ImageRef: "ghcr.io/org/app",
	}, layout, &log)
	if err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()

	for _, want := range []string{
		"--context dir://" + cwd,
		"--no-push",
		"--oci-layout-path " + layout,
		"--ignore-path " + layout,
		"--ignore-path " + cwd,
		"--dockerfile " + filepath.Join(cwd, "Containerfile"),
		"--custom-platform linux/arm64",
		"--target runtime",
		"--build-arg VERSION=1.2.3",
		"--label org.opencontainers.image.revision=abc",
		"--reproducible",
		"--cache=true --cache-repo ghcr.io/org/cache --no-push-cache",
		`CONFIG {"auths":{"ghcr.io":{"auth":"Y2FuYXJ5"}}}`,
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("missing %q:\n%s", want, log.String())
		}
	}

	if strings.Contains(log.String(), "DOCKER_CONFIG=<unset>") {
		t.Error("kaniko was not given the registry credentials")
	}

	if !strings.Contains(log.String(), "replaces this container's root filesystem") {
		t.Error("the log must warn what kaniko does to the job container")
	}
}

func TestBuildToLayout_WithoutCredentialsOrCacheStaysMinimal(t *testing.T) {
	t.Parallel()

	adapter := fakeExecutor(t)

	var log strings.Builder

	err := adapter.BuildToLayout(t.Context(), container.BuildRequest{Context: ".", Mode: container.BuildModeOCI, OutputDir: t.TempDir()}, t.TempDir(), &log)
	if err != nil {
		t.Fatal(err)
	}

	for _, absent := range []string{"--dockerfile", "--custom-platform", "--reproducible", "--cache", "--target", "CONFIG "} {
		if strings.Contains(log.String(), absent) {
			t.Errorf("unexpected %q:\n%s", absent, log.String())
		}
	}

	if !strings.Contains(log.String(), "DOCKER_CONFIG=<unset>") {
		t.Error("no auth file must mean no credentials directory")
	}
}

// What kaniko cannot do is refused by name, never worked around.
func TestAdapter_RefusesWhatNeedsALocalStoreOrSecrets(t *testing.T) {
	t.Parallel()

	adapter := fakeExecutor(t)

	for name, run := range map[string]func() error{
		"load mode": func() error {
			return adapter.Build(t.Context(), container.BuildRequest{Context: ".", Mode: container.BuildModeLoad, ImageRef: "app:test"}, &strings.Builder{})
		},
		"local mode": func() error {
			return adapter.Build(t.Context(), container.BuildRequest{Context: ".", Mode: container.BuildModeLocal, OutputDir: t.TempDir()}, &strings.Builder{})
		},
		"build secrets": func() error {
			return adapter.BuildToLayout(t.Context(), container.BuildRequest{Context: ".", Mode: container.BuildModeOCI, OutputDir: t.TempDir(), Secrets: []string{"id=npm,src=/tmp/npmrc"}}, t.TempDir(), &strings.Builder{})
		},
		"export without a staged build": func() error {
			return adapter.ExportLocalImageToLayout(t.Context(), "localhost/nothing:amd64", t.TempDir(), &strings.Builder{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := run()
			if !errors.Is(err, errs.ErrUsage) || !strings.Contains(err.Error(), "kaniko") {
				t.Fatalf("err=%v, want a usage refusal naming kaniko", err)
			}
		})
	}
}

func TestPreflight_NamesTheMissingExecutorAndTheMissingRoot(t *testing.T) {
	t.Parallel()

	missing := &Adapter{Executor: filepath.Join(t.TempDir(), "executor"), euid: func() int { return 0 }}
	if err := missing.BuildToLayout(t.Context(), container.BuildRequest{Context: ".", Mode: container.BuildModeOCI, OutputDir: t.TempDir()}, t.TempDir(), &strings.Builder{}); !errors.Is(err, errs.ErrDependencyUnavailable) || !strings.Contains(err.Error(), "kaniko job image") {
		t.Errorf("missing executor: err=%v", err)
	}

	unprivileged := fakeExecutor(t)
	unprivileged.euid = func() int { return 1000 }

	if err := unprivileged.BuildToLayout(t.Context(), container.BuildRequest{Context: ".", Mode: container.BuildModeOCI, OutputDir: t.TempDir()}, t.TempDir(), &strings.Builder{}); !errors.Is(err, errs.ErrUsage) || !strings.Contains(err.Error(), "uid 0") {
		t.Errorf("non-root: err=%v", err)
	}
}

// The build-arch tool builds into a local tag and exports it. Kaniko has no
// local store, so the build waits for the export and runs once, straight
// into the layout, with the provenance labels the tool expects.
func TestBuildArch_BuildsOnceAtExportWithProvenanceLabels(t *testing.T) {
	t.Parallel()

	adapter := fakeExecutor(t)

	var log strings.Builder

	req := container.SignerImageBuildToolRequest{
		Platform: "linux/amd64", SourceURL: "https://forge.example/org/forge-ci", Revision: "abc123",
		LocalImage: "localhost/signer:amd64", Containerfile: "containers/signer/Containerfile", Context: ".", Title: "signer",
		Target: "runtime", BuildArgs: []string{"FLAVOR=core"}, Labels: []string{"org.example.flavor=core"},
	}
	if err := adapter.BuildSignerImage(t.Context(), req, &log); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(log.String(), "ARGV") {
		t.Fatal("the build must not run before the export names its layout")
	}

	layout := filepath.Join(t.TempDir(), "layout")
	if err := adapter.ExportLocalImageToLayout(t.Context(), "localhost/signer:amd64", layout, &log); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"--oci-layout-path " + layout,
		"--custom-platform linux/amd64",
		"--label org.opencontainers.image.source=https://forge.example/org/forge-ci",
		"--label org.opencontainers.image.revision=abc123",
		"--label org.opencontainers.image.title=signer",
		"--label org.example.flavor=core",
		"--target runtime",
		"--build-arg FLAVOR=core",
		"--reproducible",
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("missing %q:\n%s", want, log.String())
		}
	}

	if strings.Count(log.String(), "ARGV") != 1 {
		t.Errorf("want exactly one kaniko run, got:\n%s", log.String())
	}

	// A second export of the same tag has nothing staged.
	if err := adapter.ExportLocalImageToLayout(t.Context(), "localhost/signer:amd64", layout, &log); !errors.Is(err, errs.ErrUsage) {
		t.Errorf("second export: err=%v, want a usage refusal", err)
	}
}

func TestStageDockerConfig_RequiresTheAuthsDocument(t *testing.T) {
	t.Parallel()

	notAuths := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(notAuths, []byte("ghp_canary\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := stageDockerConfig(notAuths); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("err=%v, want ErrInvalidConfig for a bare token", err)
	}
}
