// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	appbuild "github.com/diggsweden/forge-ci/internal/app/build"
	"github.com/stretchr/testify/require"
)

type locateWorkspaceTool struct {
	manifest string
	input    appbuild.CargoRunInput
}

func (tool *locateWorkspaceTool) Run(_ context.Context, in appbuild.CargoRunInput) error {
	tool.input = in
	_, err := fmt.Fprintln(in.Stdout, tool.manifest)

	return err
}

func TestCargoCacheUsesWorkspaceLockfile(t *testing.T) {
	t.Setenv("ACTIONS_CACHE_URL", "http://cache.invalid")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "test-token")
	t.Setenv("ACTIONS_CACHE_SERVICE_V2", "")
	root := t.TempDir()
	member := filepath.Join(root, "crates", "member")
	require.NoError(t, os.MkdirAll(member, 0o700))

	lock := "version = 4\n[[package]]\nname = 'demo'\nversion = '1.0.0'\nsource = 'registry+https://github.com/rust-lang/crates.io-index'\nchecksum = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.lock"), []byte(lock), 0o600))
	t.Setenv("CARGO_HOME", t.TempDir())

	tool := &locateWorkspaceTool{manifest: filepath.Join(root, "Cargo.toml")}
	cache, err := cargoFetchCache(t.Context(), member, tool)
	require.NoError(t, err)
	require.NotNil(t, cache)
	require.Equal(t, member, tool.input.Dir)
	require.Equal(t, []string{"locate-project", "--workspace", "--message-format", "plain"}, tool.input.Args)
}

func TestCargoCacheUnavailableSkipsWorkspaceDiscovery(t *testing.T) {
	for _, scenario := range []struct{ name, url, token, v2 string }{
		{"missing_url", "", "token", ""},
		{"missing_token", "http://cache.invalid", "", ""},
		{"invalid_url", "file:///cache", "token", ""},
		{"v2", "http://cache.invalid", "token", "true"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("ACTIONS_CACHE_URL", scenario.url)
			t.Setenv("ACTIONS_RUNTIME_TOKEN", scenario.token)
			t.Setenv("ACTIONS_CACHE_SERVICE_V2", scenario.v2)

			tool := &locateWorkspaceTool{}
			cache, err := cargoFetchCache(t.Context(), "missing-workspace", tool)
			require.Error(t, err)
			require.Nil(t, cache)
			require.Empty(t, tool.input.Args)
		})
	}
}
