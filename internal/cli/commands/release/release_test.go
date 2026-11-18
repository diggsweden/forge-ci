// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package release_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/cli"
	releasecmd "github.com/diggsweden/forge-ci/internal/cli/commands/release"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/ghaenv"
	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
	"github.com/diggsweden/forge-ci/internal/testutil/testfs"
)

func TestResolveArtifactNameCmd_RequiresProjectTypeFlag(t *testing.T) {
	cmd := releasecmd.New()

	err := cmd.Run(context.Background(), []string{"release", "resolve", "artifact-name"})
	assertMissingFlag(t, err, "project-type")
}

// assertMissingFlag pins a framework required-flag refusal: the message names
// the flag, and the exit code the operator's shell sees is 2. urfave's own
// error carries no sentinel until main.go classifies it, so the classification
// is applied here rather than asserted on the raw error.
func assertMissingFlag(t *testing.T, err error, flag string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected a refusal for the missing --%s", flag)
	}

	if want := `Required flag "` + flag + `" not set`; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want %q", err, want)
	}

	if got := errs.ExitCodeFromError(cli.ClassifyError(err)); got != errs.ExitCodeUsage {
		t.Errorf("exit code = %d, want usage (%d)", got, errs.ExitCodeUsage)
	}
}

// TestGPGImportCmd_RefusesTheKeyWithItsExitClass pins the exit code of each
// way the private key can be wrong: absent is a missing secret (77), and a
// value that is no OpenPGP key is malformed input (65), refused by the
// in-process parse before gpg runs. PATH holds no gpg, so a refusal that
// reached gpg would exit 78 instead.
func TestGPGImportCmd_RefusesTheKeyWithItsExitClass(t *testing.T) {
	for _, tc := range []struct {
		name, key, cause string
		exit             errs.ExitCodeType
	}{
		{name: "absent", key: "", cause: "the GPG private key is required", exit: errs.ExitCodeNoPerm},
		{name: "armored garbage", key: "-----BEGIN PGP PRIVATE KEY BLOCK-----\nnot-a-key\n-----END PGP PRIVATE KEY BLOCK-----\n", cause: "parse armored key", exit: errs.ExitCodeDataErr},
		{name: "base64 of no key", key: base64.StdEncoding.EncodeToString([]byte("not-a-key")), cause: "parse armored key", exit: errs.ExitCodeDataErr},
		{name: "not base64", key: "not base64!", cause: "standard base64", exit: errs.ExitCodeDataErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.New(t)
			t.Setenv("PATH", t.TempDir())
			t.Setenv("GPG_PRIVATE_KEY", tc.key)

			err := releasecmd.New().Run(t.Context(), []string{"release", "gpg", "import"})
			if err == nil || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("err = %v, want it to name %q", err, tc.cause)
			}

			if strings.Contains(err.Error(), "not-a-key") {
				t.Errorf("err echoes the key material: %v", err)
			}

			if got := errs.ExitCodeFromError(cli.ClassifyError(err)); got != tc.exit {
				t.Errorf("exit code = %d (%v), want %d", got, err, tc.exit)
			}
		})
	}
}

func TestResolveMetadataCmd_WritesOutputsFromEnv(t *testing.T) {
	env := ghaenv.Setup(t)
	env.Setenv("VERSION", "v1.2.3")
	env.Setenv("REPOSITORY", "diggsweden/forge-ci")
	env.Setenv("ARTIFACT_NAME", "artifact-name")

	cmd := releasecmd.New()
	if err := cmd.Run(context.Background(), []string{"release", "resolve", "metadata"}); err != nil {
		t.Fatal(err)
	}

	if got := env.Output("version"); got != "v1.2.3" {
		t.Errorf("version = %q", got)
	}

	if got := env.Output("version-no-v"); got != "1.2.3" {
		t.Errorf("version-no-v = %q", got)
	}

	if got := env.Output("project-name"); got != "artifact-name" {
		t.Errorf("project-name = %q", got)
	}
}

func TestNotesCmd_UsesDefaultsAndWritesFallbackContentFromEnv(t *testing.T) {
	fsys := testfs.NewReal(t)
	fsys.Chdir()

	env := ghaenv.Setup(t)
	env.Setenv("RELEASE_VERSION", "v2.0.0")
	env.Setenv("RELEASE_COMMIT", "abc1234")

	cmd := releasecmd.New()
	if err := cmd.Run(context.Background(), []string{"release", "notes"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(fsys.Path("release-notes.md")); err != nil {
		t.Errorf("default target not created: %v", err)
	}

	body := fsys.ReadFile("release-notes.md")
	for _, want := range []string{"# Release v2.0.0", "Release created from commit abc1234"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("release-notes.md missing %q:\n%s", want, body)
		}
	}
}

// TestPublishCmd_DryRunReconcileNeedsNoForge proves the reconcile-strategy
// preview: on the local platform (no forge) a real publish fails with
// ErrUnsupported, while --dry-run succeeds because the narrating decorator
// replaces the forge publisher — the forge mutation path is never entered.
func TestPublishCmd_DryRunReconcileNeedsNoForge(t *testing.T) {
	fsys := testfs.NewReal(t)
	fsys.Chdir()
	fsys.WriteFile("dist/release-notes.md", []byte("notes\n"))
	fsys.WriteFile("dist/asset.tgz", []byte("asset\n"))

	testenv.New(t) // isolated env => local platform, no forge roles

	args := make([]string, 0, 9)
	args = append(args,
		"release", "publish", "--tag", "v1.2.3", "--repository", "owner/repo",
		"--asset", "dist/asset.tgz")

	err := releasecmd.New().Run(context.Background(), append(args, "--dry-run"))
	if err != nil {
		t.Fatalf("dry-run publish should not need a forge: %v", err)
	}

	// Flag off: byte-identical behavior — the forge publisher is required
	// and the local platform cannot provide it.
	err = releasecmd.New().Run(context.Background(), args)
	if !errors.Is(err, errs.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported without --dry-run on local", err)
	}
}

// TestPublishCmd_DryRunRecreateNeedsNoForge is the recreate-strategy twin.
func TestPublishCmd_DryRunRecreateNeedsNoForge(t *testing.T) {
	fsys := testfs.NewReal(t)
	fsys.Chdir()
	fsys.WriteFile("dist/release-notes.md", []byte("notes\n"))
	fsys.WriteFile("release-assembly.json", []byte(`{"version":1}`))

	testenv.New(t)

	args := make([]string, 0, 11)
	args = append(args,
		"release", "publish", "--strategy", "recreate", "--tag", "v1.2.3",
		"--repository", "owner/repo", "--assembly", "release-assembly.json")

	err := releasecmd.New().Run(context.Background(), append(args, "--dry-run"))
	if err != nil {
		t.Fatalf("dry-run recreate publish should not need a forge: %v", err)
	}

	err = releasecmd.New().Run(context.Background(), args)
	if !errors.Is(err, errs.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported without --dry-run on local", err)
	}
}

// TestPublishCmd_DryRunStillFailsOnMissingAsset proves asset existence and
// manifest resolution still run under --dry-run, so config errors surface.
func TestPublishCmd_DryRunStillFailsOnMissingAsset(t *testing.T) {
	fsys := testfs.NewReal(t)
	fsys.Chdir()
	fsys.WriteFile("dist/release-notes.md", []byte("notes\n"))

	testenv.New(t)

	err := releasecmd.New().Run(context.Background(), []string{
		"release", "publish", "--dry-run", "--tag", "v1.2.3",
		"--repository", "owner/repo", "--asset", "dist/missing.tgz",
	})
	if !errors.Is(err, errs.ErrMissingInput) {
		t.Fatalf("err = %v, want ErrMissingInput for a missing asset in dry-run", err)
	}
}

func TestVerifyChangelogCmd_RequiresFlag(t *testing.T) {
	cmd := releasecmd.New()

	err := cmd.Run(context.Background(), []string{"release", "validate-changelog"})
	assertMissingFlag(t, err, "changelog-file")
}

func TestVerifyChangelogCmd_SucceedsWhenFileExists(t *testing.T) {
	fsys := testfs.NewReal(t)
	fsys.Chdir()
	fsys.WriteFile("CHANGELOG.md", []byte("# Changelog\n\n- entry\n"))

	cmd := releasecmd.New()
	if err := cmd.Run(context.Background(), []string{"release", "validate-changelog", "--changelog-file", "CHANGELOG.md"}); err != nil {
		t.Fatal(err)
	}
}
