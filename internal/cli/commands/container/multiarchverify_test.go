// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	containercmd "github.com/diggsweden/forge-ci/internal/cli/commands/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestMultiarchCommandRejectsConflictingTagAndMalformedLabelsBeforeIO(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"auths":{}}`), 0o600))
	base := []string{"container", "multiarch-image", "assemble", "--auth-file", auth, "--source-sha", strings.Repeat("a", 64), "--server-url", "https://forge.invalid", "--repository", "acme/widget", "--image-repository", "registry.invalid/acme/widget"}

	for _, extra := range [][]string{{"--tag", "staging-v1.2.3", "--tag-prefix", "conflict-"}, {"--require-label", "missing-equals"}} {
		args := append(append([]string{}, base...), extra...)
		err := containercmd.New().Run(t.Context(), args)
		require.ErrorIs(t, err, errs.ErrUsage)
	}

	err := containercmd.New().Run(t.Context(), []string{"container", "multiarch-image", "verify", "--ref", "registry.invalid/acme/widget:mutable"})
	require.ErrorIs(t, err, errs.ErrUsage)
}

func TestMultiarchCommandsRejectStdoutSnapshotsBeforeReadingMetadata(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"auths":{}}`), 0o600))

	for _, command := range []string{"assemble", "verify"} {
		args := []string{"container", "multiarch-image", command, "--auth-file", auth,
			"--source-sha", strings.Repeat("a", 40), "--server-url", "https://forge.invalid",
			"--repository", "acme/widget", "--image-repository", "registry.invalid/acme/widget",
			"--manifest-output", "-"}
		if command == "verify" {
			args = append(args, "--ref", "registry.invalid/acme/widget@sha256:"+strings.Repeat("1", 64))
		}

		err := containercmd.New().Run(t.Context(), args)
		require.ErrorIs(t, err, errs.ErrUsage)
		require.Contains(t, err.Error(), "stdout (-) is not supported")
	}
}
