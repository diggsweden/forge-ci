// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package publish_test

import (
	publishcmd "github.com/diggsweden/forge-ci/internal/cli/commands/publish"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/ghaenv"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestAndroidReleaseAdmissionIsCredentialFreeAndFailsClosed(t *testing.T) {
	env := ghaenv.Setup(t)
	file := filepath.Join(t.TempDir(), "artifacts.yml")
	write := func(publish bool) {
		target := ""
		if publish {
			target = "    publish-to: [forge-packages]\n"
		}
		require.NoError(t, os.WriteFile(file, []byte("artifacts:\n  - name: library\n    project-type: gradle-android\n    build-type: library\n"+target), 0600))
	}
	run := func(args ...string) error {
		return publishcmd.New().Run(t.Context(), append([]string{"publish", "forge-packages", "android-release", "--file", file}, args...))
	}
	write(false)
	require.NoError(t, run("--admit", "--token-configured", "false"))
	require.Equal(t, "false", env.Output("android-maven-packages"))
	write(true)
	require.ErrorIs(t, run("--admit", "--token-configured", "false"), errs.ErrPermissionDenied)
	require.Equal(t, "false", env.Output("android-maven-packages"))
	require.NoError(t, run("--admit", "--token-configured", "true"))
	require.Equal(t, "true", env.Output("android-maven-packages"))
	require.ErrorIs(t, run("--admit", "--token-configured", "perhaps"), errs.ErrUsage)
	require.ErrorIs(t, run("--list-files"), errs.ErrUsage)
	require.Error(t, run("--admit", "--manifest", "missing.json", "--version", "1.2.3"))
	require.ErrorIs(t, run("--list", "--admit"), errs.ErrUsage)

	// The producer's listing path reads only exported bytes, never Gradle or
	// a registry. Wrong versions must fail before a release can declare them.
	dist := filepath.Join(t.TempDir(), "dist")
	repository := filepath.Join(dist, "library-maven-publications", "org", "example", "library", "1.2.3")
	require.NoError(t, os.MkdirAll(repository, 0700))
	pom := `<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>library</artifactId><version>1.2.3</version><packaging>aar</packaging></project>`
	require.NoError(t, os.WriteFile(filepath.Join(repository, "library-1.2.3.pom"), []byte(pom), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "library-1.2.3.aar"), []byte("producer AAR"), 0600))
	require.NoError(t, run("--list-files", "--version", "1.2.3", "--dist-dir", dist))
	require.Error(t, run("--list-files", "--version", "9.9.9", "--dist-dir", dist))
}
