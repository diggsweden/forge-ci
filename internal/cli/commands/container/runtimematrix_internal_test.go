// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeMatrixCommandWritesSelectedNativeMatricesWithoutPublicationAuthority(t *testing.T) {
	root := t.TempDir()
	roster, output := filepath.Join(root, "images.json"), filepath.Join(root, "output")
	require.NoError(t, os.WriteFile(roster, []byte(`[{"target":"go","name":"runtime-go","archs":["amd64","arm64"]},{"target":"android","name":"runtime-android","archs":["amd64"]}]`), 0600))

	for _, key := range []string{"GITLAB_CI", "FORGEJO_ACTIONS", "GITEA_ACTIONS"} {
		t.Setenv(key, "")
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_OUTPUT", output)
	t.Setenv("GITHUB_REF", "refs/pull/1/merge")
	require.NoError(t, runtimeImagesCmd().Run(t.Context(), []string{"runtime-images", "matrix", "--roster", roster, "--release-platforms", "linux/arm64"}))

	body, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Contains(t, string(body), `build-matrix={"include":[{"target":"go","name":"runtime-go","arch":"arm64"}]}`)
	require.Contains(t, string(body), `image-matrix={"include":[{"target":"go","name":"runtime-go","archs":["arm64"]}]}`)
	require.NotContains(t, string(body), "candidate-tag")
}
