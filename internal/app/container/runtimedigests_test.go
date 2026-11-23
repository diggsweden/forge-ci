// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	"github.com/stretchr/testify/require"
)

func TestRuntimeDigestsRequireEverySelectedArtifactBeforeMerge(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"complete", "missing arm64", "empty arm64", "extra marker", "repeated digest"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			dest := filepath.Join(t.TempDir(), "digests")

			var names []string

			downloader := runtimeDownloadFunc(func(_ context.Context, in provider.RunArtifactDownload) (provider.RunArtifactInfo, error) {
				names = append(names, in.Name)
				require.Empty(t, in.Pattern)

				if in.Name == "digests-go-arm64" && scenario == "missing arm64" {
					return provider.RunArtifactInfo{}, nil
				}

				require.NoError(t, os.Mkdir(in.Dir, 0700))

				if in.Name == "digests-go-arm64" && scenario == "empty arm64" {
					return provider.RunArtifactInfo{}, nil
				}

				digit := "a"
				if in.Name == "digests-go-arm64" && scenario != "repeated digest" {
					digit = "b"
				}

				require.NoError(t, os.WriteFile(filepath.Join(in.Dir, strings.Repeat(digit, 64)), nil, 0600))

				if scenario == "extra marker" {
					require.NoError(t, os.WriteFile(filepath.Join(in.Dir, strings.Repeat("c", 64)), nil, 0600))
				}

				return provider.RunArtifactInfo{}, nil
			})

			err := appcontainer.DownloadRuntimeDigests(t.Context(), downloader, `{"target":"go","name":"runtime-go","archs":["amd64","arm64"]}`, dest)
			if scenario != "complete" {
				require.Error(t, err)
				require.NoDirExists(t, dest, "partial inputs must never reach manifest merge")

				return
			}

			require.NoError(t, err)
			require.Equal(t, []string{"digests-go-amd64", "digests-go-arm64"}, names)

			markers, err := os.ReadDir(dest)
			require.NoError(t, err)
			require.Len(t, markers, 2)
		})
	}
}
