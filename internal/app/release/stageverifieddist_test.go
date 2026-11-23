// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package release_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apprelease "github.com/diggsweden/forge-ci/internal/app/release"
	"github.com/stretchr/testify/require"
)

func TestStageVerifiedDistChecksInstalledBytesAndCleansFailedStaging(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"valid", "tampered", "link", "preseeded", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			source := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(source, ".asset"), []byte("reviewed bytes"), 0o755))
			digest, err := apprelease.DistDigestWithManifestRoot(source, ".")
			require.NoError(t, err)
			destination := filepath.Join(t.TempDir(), "dist")

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			switch scenario {
			case "tampered":
				require.NoError(t, os.WriteFile(filepath.Join(source, ".asset"), []byte("tampered bytes"), 0o755))
			case "link":
				require.NoError(t, os.Symlink(".asset", filepath.Join(source, "escape")))
			case "preseeded":
				require.NoError(t, os.Mkdir(destination, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(destination, "keep"), []byte("owned"), 0o600))
			case "cancelled":
				cancel()
			}

			err = apprelease.StageVerifiedDist(ctx, source, destination, digest, ".")
			if scenario == "valid" {
				require.NoError(t, err)
				require.NoError(t, apprelease.VerifyDistWithManifestRoot(destination, digest, "."))
				info, err := os.Stat(filepath.Join(destination, ".asset"))
				require.NoError(t, err)
				require.NotZero(t, info.Mode().Perm()&0o111)

				entries, err := os.ReadDir(destination)
				require.NoError(t, err)
				require.Len(t, entries, 1)
			} else {
				require.Error(t, err)

				if scenario == "preseeded" {
					require.FileExists(t, filepath.Join(destination, "keep"))
				} else {
					require.NoDirExists(t, destination)
				}
			}
		})
	}
}

func TestVerifiedContextOverlayPreservesCheckoutAndRefusesUnsafeInputs(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"valid", "tampered", "missing-digest", "source-link", "destination-link", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			source, destination := t.TempDir(), t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(source, "nested"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(source, "nested", ".binary"), []byte("verified"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(destination, "keep"), []byte("checkout"), 0o644))

			outside := t.TempDir()
			if scenario == "destination-link" {
				require.NoError(t, os.Symlink(outside, filepath.Join(destination, "nested")))
			} else {
				require.NoError(t, os.Mkdir(filepath.Join(destination, "nested"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(destination, "nested", ".binary"), []byte("old"), 0o644))
			}

			digest, err := apprelease.DistDigestWithManifestRoot(source, ".")
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			switch scenario {
			case "tampered":
				require.NoError(t, os.WriteFile(filepath.Join(source, "nested", ".binary"), []byte("changed"), 0o755))
			case "missing-digest":
				digest = ""
			case "source-link":
				require.NoError(t, os.Symlink(outside, filepath.Join(source, "escape")))
			case "cancelled":
				cancel()
			}

			err = apprelease.OverlayVerifiedDist(ctx, source, destination, digest, ".")
			if scenario == "valid" {
				require.NoError(t, err)
				body, err := os.ReadFile(filepath.Join(destination, "nested", ".binary"))
				require.NoError(t, err)
				require.Equal(t, "verified", string(body))

				info, err := os.Stat(filepath.Join(destination, "nested", ".binary"))
				require.NoError(t, err)
				require.NotZero(t, info.Mode().Perm()&0o111)
			} else {
				require.Error(t, err)

				if scenario != "destination-link" {
					body, err := os.ReadFile(filepath.Join(destination, "nested", ".binary"))
					require.NoError(t, err)
					require.Equal(t, "old", string(body))
				}
			}

			body, err := os.ReadFile(filepath.Join(destination, "keep"))
			require.NoError(t, err)
			require.Equal(t, "checkout", string(body))

			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			require.Empty(t, entries)
			entries, err = os.ReadDir(destination)
			require.NoError(t, err)
			require.Len(t, entries, 2, "private staging must be cleaned")
		})
	}
}

func TestVerifiedContextOverlayRefusesMissingDestinationAndTypeCollisions(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"missing destination", "file destination", "file over directory", "directory over file"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			source, root := t.TempDir(), t.TempDir()
			destination := filepath.Join(root, "checkout")

			switch scenario {
			case "missing destination":
			case "file destination":
				require.NoError(t, os.WriteFile(destination, []byte("owned"), 0o600))
			default:
				require.NoError(t, os.Mkdir(destination, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(destination, "keep"), []byte("owned"), 0o600))
			}

			if scenario == "directory over file" {
				require.NoError(t, os.Mkdir(filepath.Join(source, "collision"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(source, "collision", "asset"), []byte("new"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(destination, "collision"), []byte("owned"), 0o600))
			} else {
				require.NoError(t, os.WriteFile(filepath.Join(source, "collision"), []byte("new"), 0o600))

				if scenario == "file over directory" {
					require.NoError(t, os.Mkdir(filepath.Join(destination, "collision"), 0o700))
					require.NoError(t, os.WriteFile(filepath.Join(destination, "collision", "keep"), []byte("owned"), 0o600))
				}
			}

			digest, err := apprelease.DistDigestWithManifestRoot(source, ".")
			require.NoError(t, err)
			require.Error(t, apprelease.OverlayVerifiedDist(t.Context(), source, destination, digest, "."))

			var preserved string

			switch scenario {
			case "missing destination":
				require.NoFileExists(t, destination)
				require.NoDirExists(t, destination)
			case "file destination":
				preserved = destination
			case "file over directory":
				preserved = filepath.Join(destination, "collision", "keep")
			case "directory over file":
				preserved = filepath.Join(destination, "collision")
			}

			if preserved != "" {
				body, err := os.ReadFile(preserved)
				require.NoError(t, err)
				require.Equal(t, "owned", string(body))
			}
		})
	}
}

func TestReadDistDigestFileIsStrictData(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "digest")
	for _, data := range []string{"$(touch exploit)", strings.Repeat("A", 64), " " + strings.Repeat("a", 64), strings.Repeat("a", 64) + " extra"} {
		require.NoError(t, os.WriteFile(file, []byte(data), 0o600))
		_, err := apprelease.ReadDistDigestFile(file)
		require.Error(t, err)
	}

	require.NoError(t, os.WriteFile(file, []byte(strings.Repeat("a", 64)+"\n"), 0o600))
	digest, err := apprelease.ReadDistDigestFile(file)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("a", 64), digest)

	link := file + ".link"
	require.NoError(t, os.Symlink(file, link))
	_, err = apprelease.ReadDistDigestFile(link)
	require.Error(t, err)
}
