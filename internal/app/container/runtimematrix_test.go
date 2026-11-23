// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/testutil/fakeoutputsink"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"github.com/stretchr/testify/require"
)

func TestRuntimeMatrixSelectsMatchingBuildAndMergeTargets(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		selection      string
		builds, images int
	}{
		{"linux/amd64", 7, 7}, {"linux/arm64", 6, 6}, {"", 13, 7},
	} {
		t.Run(test.selection, func(t *testing.T) {
			t.Parallel()

			sink := fakeoutputsink.New(t)
			require.NoError(t, appcontainer.PlanRuntimeMatrix(t.Context(), sink,
				filepath.Join(reporoot.Path(t), "containers/runtime/images.json"),
				"linux/amd64,linux/arm64", test.selection))

			var (
				builds struct {
					Include []struct {
						Target string `json:"target"`
						Name   string `json:"name"`
						Arch   string `json:"arch"`
					} `json:"include"`
				}
				images struct {
					Include []struct {
						Target string   `json:"target"`
						Name   string   `json:"name"`
						Archs  []string `json:"archs"`
					} `json:"include"`
				}
			)

			require.NoError(t, json.Unmarshal([]byte(sink.Single("build-matrix")), &builds))
			require.NoError(t, json.Unmarshal([]byte(sink.Single("image-matrix")), &images))
			require.Len(t, builds.Include, test.builds)
			require.Len(t, images.Include, test.images)

			for _, image := range images.Include {
				for _, arch := range image.Archs {
					found := false

					for _, build := range builds.Include {
						if build.Target == image.Target && build.Arch == arch && build.Name == image.Name {
							found = true
						}
					}

					require.True(t, found, "every merge input must have a build leg")
				}

				if test.selection == "linux/arm64" {
					require.NotEqual(t, "android", image.Target)
				}
			}

			for _, build := range builds.Include {
				if test.selection != "" {
					require.Equal(t, "linux/"+build.Arch, test.selection)
				}
			}

			require.NotContains(t, sink.Keys(), "candidate-tag", "selection grants no publication authority")
		})
	}
}

func TestRuntimeMatrixRejectsInvalidUnselectedRosterBeforeOutput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "images.json")
	require.NoError(t, os.WriteFile(path, []byte(`[{"target":"go","name":"runtime-go","archs":["arm64"]},{"target":"bad","name":"runtime-bad","archs":["s390x"]}]`), 0600))
	sink := fakeoutputsink.New(t)
	require.Error(t, appcontainer.PlanRuntimeMatrix(t.Context(), sink, path, "linux/amd64,linux/arm64", "linux/arm64"))
	require.Empty(t, sink.Keys())
}
