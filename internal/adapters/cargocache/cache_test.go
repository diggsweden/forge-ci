// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package cargocache

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type memoryTransport struct {
	data []byte
	key  string
}

func (m *memoryTransport) Restore(_ context.Context, key, destination string) (bool, error) {
	m.key = key
	if m.data == nil {
		return false, nil
	}

	return true, os.WriteFile(destination, m.data, 0o600)
}
func (m *memoryTransport) Save(_ context.Context, key, source string) error {
	m.key = key

	var err error

	m.data, err = os.ReadFile(source)

	return err
}

func lock(t *testing.T, version string, contents []byte) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "Cargo.lock")
	text := fmt.Sprintf("version = 4\n[[package]]\nname = \"workspace\"\nversion = %q\n[[package]]\nname = \"demo\"\nversion = \"1.0.0\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"%x\"\n", version, sha256.Sum256(contents))
	require.NoError(t, os.WriteFile(name, []byte(text), 0o600))

	return name
}

func TestRoundTripOnlyLockedArchives(t *testing.T) {
	data := []byte("compressed crate bytes")
	home := t.TempDir()
	path := "registry/cache/index.crates.io-1949cf8c6b5b557f/demo-1.0.0.crate"
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(home, path)), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, path), data, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "credentials.toml"), []byte("SECRET"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(filepath.Join(home, path)), "other.crate"), []byte("unlocked"), 0o600))

	transport := &memoryTransport{}
	cache, err := New(lock(t, "1.0.0", data), home, transport)
	require.NoError(t, err)
	require.NoError(t, cache.Save(t.Context()))

	reader := tar.NewReader(bytes.NewReader(transport.data))
	header, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, "index.crates.io-1949cf8c6b5b557f/demo-1.0.0.crate", header.Name)

	_, err = reader.Next()
	require.ErrorIs(t, err, io.EOF)
	warm := t.TempDir()
	restored, err := New(lock(t, "2.0.0", data), warm, transport)
	require.NoError(t, err)
	require.Equal(t, cache.key, restored.key, "workspace version must not invalidate dependency downloads")
	hit, err := restored.Restore(t.Context())
	require.NoError(t, err)
	require.True(t, hit)

	got, err := os.ReadFile(filepath.Join(warm, path))
	require.NoError(t, err)
	require.Equal(t, data, got)
	changed, err := New(lock(t, "2.0.0", []byte("other dependency bytes")), warm, transport)
	require.NoError(t, err)
	require.NotEqual(t, cache.key, changed.key)
}

func TestRejectUntrustedEntries(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		kind       byte
		data       string
	}{
		{"traversal", "../../credentials.toml", tar.TypeReg, "valid"},
		{"credentials", "credentials.toml", tar.TypeReg, "valid"},
		{"unknown_registry", "private/demo-1.0.0.crate", tar.TypeReg, "valid"},
		{"unlocked", "index.crates.io-1949cf8c6b5b557f/other.crate", tar.TypeReg, "valid"},
		{"corrupt", "index.crates.io-1949cf8c6b5b557f/demo-1.0.0.crate", tar.TypeReg, "corrupt"},
		{"symlink", "index.crates.io-1949cf8c6b5b557f/demo-1.0.0.crate", tar.TypeSymlink, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			writer := tar.NewWriter(&buf)
			require.NoError(t, writer.WriteHeader(&tar.Header{Name: tc.path, Typeflag: tc.kind, Size: int64(len(tc.data)), Linkname: "../../credentials.toml"}))
			_, err := writer.Write([]byte(tc.data))
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			home := t.TempDir()
			c, err := New(lock(t, "1.0.0", []byte("valid")), home, &memoryTransport{data: buf.Bytes()})
			require.NoError(t, err)
			hit, err := c.Restore(t.Context())
			require.Error(t, err)
			require.False(t, hit)

			files, err := filepath.Glob(filepath.Join(home, "registry/cache/*/*.crate"))
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestSaveRejectsCorruptionAndLinks(t *testing.T) {
	for _, link := range []bool{false, true} {
		home := t.TempDir()
		dir := filepath.Join(home, "registry/cache/index.crates.io-1949cf8c6b5b557f")
		require.NoError(t, os.MkdirAll(dir, 0o700))

		if link {
			require.NoError(t, os.Symlink("../../../credentials.toml", filepath.Join(dir, "demo-1.0.0.crate")))
		} else {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "demo-1.0.0.crate"), []byte("wrong"), 0o600))
		}

		transport := &memoryTransport{}
		c, err := New(lock(t, "1", []byte("valid")), home, transport)
		require.NoError(t, err)
		require.Error(t, c.Save(t.Context()))
		require.Nil(t, transport.data)
	}
}

func TestRestoreRejectsOversizedEntryBeforeReadingBody(t *testing.T) {
	var buffer bytes.Buffer

	writer := tar.NewWriter(&buffer)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "index.crates.io-1949cf8c6b5b557f/demo-1.0.0.crate", Typeflag: tar.TypeReg, Size: maxCrate + 1}))
	cache, err := New(lock(t, "1", []byte("valid")), t.TempDir(), &memoryTransport{data: buffer.Bytes()})
	require.NoError(t, err)
	hit, err := cache.Restore(t.Context())
	require.ErrorIs(t, err, errInvalid)
	require.False(t, hit)
}

func TestRestoreRejectsEmptyArchives(t *testing.T) {
	for _, data := range [][]byte{{}, make([]byte, 1024)} {
		cache, err := New(lock(t, "1", []byte("valid")), t.TempDir(), &memoryTransport{data: data})
		require.NoError(t, err)
		hit, err := cache.Restore(t.Context())
		require.ErrorIs(t, err, errInvalid)
		require.False(t, hit)
	}
}

func TestIncompleteCacheCannotBePublishedOrReportedAsHit(t *testing.T) {
	data := []byte("valid crate")
	lockfile := lock(t, "1", data)
	contents, err := os.ReadFile(lockfile)
	require.NoError(t, err)

	contents = append(contents, fmt.Appendf(nil, "\n[[package]]\nname = 'second'\nversion = '1.0.0'\nsource = 'registry+https://github.com/rust-lang/crates.io-index'\nchecksum = '%x'\n", sha256.Sum256(data))...)
	require.NoError(t, os.WriteFile(lockfile, contents, 0o600))
	home := t.TempDir()
	dir := filepath.Join(home, "registry/cache/index.crates.io-1949cf8c6b5b557f")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "demo-1.0.0.crate"), data, 0o600))

	transport := &memoryTransport{}
	cache, err := New(lockfile, home, transport)
	require.NoError(t, err)
	require.Contains(t, cache.key, "forge-ci-cargo-crates-v2-")
	require.ErrorIs(t, cache.Save(t.Context()), errInvalid)
	require.Nil(t, transport.data, "do not reserve or upload partial dependency sets")

	for _, duplicate := range []bool{false, true} {
		var archive bytes.Buffer

		writer := tar.NewWriter(&archive)

		paths := []string{"index.crates.io-1949cf8c6b5b557f/demo-1.0.0.crate"}
		if duplicate {
			paths = append(paths, paths[0])
		}

		for _, path := range paths {
			require.NoError(t, writer.WriteHeader(&tar.Header{Name: path, Typeflag: tar.TypeReg, Size: int64(len(data)), Mode: 0o600}))
			_, err := writer.Write(data)
			require.NoError(t, err)
		}

		require.NoError(t, writer.Close())

		transport.data = archive.Bytes()
		warm := t.TempDir()
		restored, err := New(lockfile, warm, transport)
		require.NoError(t, err)
		hit, err := restored.Restore(t.Context())
		require.ErrorIs(t, err, errInvalid)
		require.False(t, hit, "a repeated entry cannot stand in for a missing crate")

		got, err := os.ReadFile(filepath.Join(warm, "registry/cache", paths[0]))
		require.NoError(t, err)
		require.Equal(t, data, got, "retain verified partial downloads for locked fetch")
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "second-1.0.0.crate"), data, 0o600))
	require.NoError(t, cache.Save(t.Context()))
	complete, err := New(lockfile, t.TempDir(), transport)
	require.NoError(t, err)
	hit, err := complete.Restore(t.Context())
	require.NoError(t, err)
	require.True(t, hit)
}
