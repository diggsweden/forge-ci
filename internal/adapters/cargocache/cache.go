// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package cargocache transfers only crates.io archives authenticated by Cargo.lock.
// Registry indexes, extracted sources, Git checkouts and credentials never enter it.
package cargocache

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const maxArchive = 512 << 20
const maxCrate = 64 << 20

var errInvalid = errors.New("invalid crate cache")

// Transport stores opaque archives under immutable exact keys.
type Transport interface {
	Restore(ctx context.Context, key, destination string) (bool, error)
	Save(ctx context.Context, key, source string) error
}

// Cache binds cache contents to one lockfile and Cargo home.
type Cache struct {
	transport Transport
	home      string
	key       string
	checksums map[string]string
}

// New derives a dependency-only key, so a workspace release-version change
// reuses downloads. Custom registries and Git dependencies use normal Cargo fetch.
func New(lockfile, home string, transport Transport) (*Cache, error) {
	//nolint:gosec // G304: lockfile is the caller-selected workspace input.
	f, err := os.Open(lockfile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return nil, fmt.Errorf("%w: cannot read bounded Cargo.lock", errInvalid)
	}

	var lock struct {
		Packages []struct{ Name, Version, Source, Checksum string } `toml:"package"`
	}
	if err := toml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("%w: invalid Cargo.lock", errInvalid)
	}

	cache := &Cache{transport: transport, home: filepath.Join(home, "registry", "cache"), checksums: map[string]string{}}

	for _, pkg := range lock.Packages {
		if pkg.Source != "registry+https://github.com/rust-lang/crates.io-index" {
			continue
		}

		name := pkg.Name + "-" + pkg.Version + ".crate"

		digest, err := hex.DecodeString(pkg.Checksum)
		if !plainName(pkg.Name) || !plainName(pkg.Version) || err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("%w: invalid locked crate identity", errInvalid)
		}

		if previous, ok := cache.checksums[name]; ok && previous != pkg.Checksum {
			return nil, fmt.Errorf("%w: conflicting locked crate checksums", errInvalid)
		}

		cache.checksums[name] = pkg.Checksum
	}

	if len(cache.checksums) == 0 {
		return nil, fmt.Errorf("%w: no cacheable crates.io dependencies", errInvalid)
	}

	var entries []string
	for name, digest := range cache.checksums {
		entries = append(entries, name+" "+digest+"\n")
	}

	sort.Strings(entries)
	// v2 admits only complete dependency sets; v1 could publish partial entries.
	cache.key = fmt.Sprintf("forge-ci-cargo-crates-v2-%x", sha256.Sum256([]byte(strings.Join(entries, ""))))

	return cache, nil
}

func plainName(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}

	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && !strings.ContainsRune("-_.+", char) {
			return false
		}
	}

	return true
}

// crateDir is where Cargo 1.85 and later keeps crates.io downloads under the
// sparse protocol, its default. Cargo derives the name from the registry URL;
// a Cargo that uses another name does not see a restore and cannot save, and
// fetches as if there were no cache.
const crateDir = "index.crates.io-1949cf8c6b5b557f"

// Restore verifies every crate before making that file visible to Cargo.
// A partial restore can contain only verified archives; fetch still runs locked.
func (c *Cache) Restore(ctx context.Context) (bool, error) {
	file, err := os.CreateTemp("", "forge-ci-cargo-*.tar")
	if err != nil {
		return false, err
	}

	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()

	hit, err := c.transport.Restore(ctx, c.key, file.Name())
	if err != nil || !hit {
		return false, err
	}

	info, err := file.Stat()
	if err != nil || info.Size() > maxArchive {
		return false, fmt.Errorf("%w: archive exceeds size limit", errInvalid)
	}

	if err := os.MkdirAll(c.home, 0o700); err != nil {
		return false, err
	}

	root, err := os.OpenRoot(c.home)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()

	if err := c.extract(ctx, root, tar.NewReader(file)); err != nil {
		return false, err
	}

	return true, nil
}

// Save includes only regular, checksum-verified archives named by the lockfile.
func (c *Cache) Save(ctx context.Context) error {
	root, err := os.OpenRoot(c.home)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	file, err := os.CreateTemp("", "forge-ci-cargo-*.tar")
	if err != nil {
		return err
	}

	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()

	writer := tar.NewWriter(file)

	count, err := c.writeArchive(ctx, root, writer)
	if err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	if count == 0 {
		return nil
	}

	return c.transport.Save(ctx, c.key, file.Name())
}

func (c *Cache) valid(name string, data []byte) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[0] != crateDir {
		return false
	}

	want, ok := c.checksums[parts[1]]

	return ok && fmt.Sprintf("%x", sha256.Sum256(data)) == want
}

func (c *Cache) extract(ctx context.Context, root *os.Root, reader *tar.Reader) error {
	seen := map[string]bool{}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			if len(seen) != len(c.checksums) {
				return fmt.Errorf("%w: archive is missing locked crates", errInvalid)
			}

			return nil
		}

		if err != nil {
			return err
		}

		name := filepath.Base(header.Name)
		if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > maxCrate || seen[name] || len(seen) >= 20000 {
			return fmt.Errorf("%w: invalid entry", errInvalid)
		}

		data, err := io.ReadAll(reader)
		if err != nil || !c.valid(header.Name, data) {
			return fmt.Errorf("%w: checksum or path mismatch", errInvalid)
		}

		seen[name] = true

		if err := root.MkdirAll(crateDir, 0o700); err != nil {
			return err
		}

		if err := publish(root, crateDir+"/"+name, data); err != nil {
			return err
		}
	}
}

func publish(root *os.Root, name string, data []byte) error {
	stage := name + ".forge-ci.tmp"

	f, err := root.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(stage) }()

	_, err = f.Write(data)
	closeErr := f.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	return root.Rename(stage, name)
}

func (c *Cache) writeArchive(ctx context.Context, root *os.Root, writer *tar.Writer) (int, error) {
	paths := make([]string, 0, len(c.checksums))
	for name := range c.checksums {
		paths = append(paths, crateDir+"/"+name)
	}

	sort.Strings(paths)

	var total int64

	count := 0

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		data, err := c.readCrate(root, path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return 0, err
		}

		total += int64((len(data)+511)/512*512 + 512)
		if total+1024 > maxArchive || count >= 20000 {
			return 0, fmt.Errorf("%w: archive exceeds size limit", errInvalid)
		}

		if err := writer.WriteHeader(&tar.Header{Name: path, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return 0, err
		}

		if _, err := writer.Write(data); err != nil {
			return 0, err
		}

		count++
	}

	if count != len(c.checksums) {
		return 0, fmt.Errorf("%w: cannot save without every locked crate", errInvalid)
	}

	return count, nil
}

func (c *Cache) readCrate(root *os.Root, path string) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCrate {
		return nil, fmt.Errorf("%w: invalid crate file", errInvalid)
	}

	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxCrate+1))
	if err != nil || len(data) > maxCrate || !c.valid(path, data) {
		return nil, fmt.Errorf("%w: crate checksum mismatch", errInvalid)
	}

	return data, nil
}
