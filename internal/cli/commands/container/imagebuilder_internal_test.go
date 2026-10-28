// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/adapters/buildah"
	"github.com/diggsweden/forge-ci/internal/adapters/kaniko"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestNewImageBuilder_SelectsTheAdapterByName(t *testing.T) {
	t.Parallel()

	defaultBuilder, err := newImageBuilder("", "")
	require.NoError(t, err)
	require.IsType(t, &buildah.Adapter{}, defaultBuilder, "the default is Buildah")

	named, err := newImageBuilder("Buildah", "")
	require.NoError(t, err)
	require.IsType(t, &buildah.Adapter{}, named)

	kan, err := newImageBuilder("kaniko", "/opt/kaniko/executor")
	require.NoError(t, err)
	require.Equal(t, &kaniko.Adapter{Executor: "/opt/kaniko/executor"}, kan)

	_, err = newImageBuilder("docker", "")
	require.ErrorIs(t, err, errs.ErrUsage)
}

func TestImageBuilderIdentity_UsesTheSelectedAdapterExecutable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	binary := filepath.Join(root, "builder")
	body := []byte("#!/bin/sh\nexit 99\n")
	require.NoError(t, os.WriteFile(binary, body, 0o700))
	expected := fmt.Sprintf("%x", sha256.Sum256(body))

	for kind, builder := range map[string]imageBuilder{
		"buildah": &buildah.Adapter{Bin: binary},
		"kaniko":  &kaniko.Adapter{Executor: binary},
	} {
		identity, err := builder.Identity()
		require.NoError(t, err)
		require.Equal(t, kind, identity.Kind)
		require.Equal(t, expected, identity.SHA256)
	}
}
