// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package publish_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	publishcmd "github.com/diggsweden/forge-ci/internal/cli/commands/publish"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestReleaseMavenDeployRejectsInvalidOptionsBeforeRegistryResolution(t *testing.T) {
	for _, args := range [][]string{
		{"--release-manifest", "dist/release-files.json", "--project-type", "npm"},
		{"--release-manifest", "dist/release-files.json", "--project-type", "maven"},
		{"--release-manifest", "dist/release-files.json", "--project-type", "gradle-android", "--version", "1.2.3"},
		{"--release-manifest", "dist/release-files.json", "--project-type", "gradle-android", "--dist-dir", "dist"},
		{"--release-manifest", "dist/release-files.json", "--project-type", "gradle-android", "--version", "1.2.3", "--dist-dir", "dist", "--snapshot"},
		{"--project-type", "gradle-android", "--dist-dir", "dist"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			err := publishcmd.New().Run(t.Context(), append([]string{"publish", "forge-packages", "deploy"}, args...))
			require.ErrorIs(t, err, errs.ErrUsage)
		})
	}
}
