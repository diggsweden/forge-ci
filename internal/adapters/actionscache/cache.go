// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package actionscache implements the Forgejo runner's Actions cache v1 transport.
// It handles opaque files, not extraction or trust in their contents.
package actionscache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const version = "forge-ci-cargo-crates-tar-v1"
const maxBytes int64 = 512 << 20
const contentType = "Content-Type"

var errCache = errors.New("actions cache unavailable")

// Client uses only the runner-provided cache endpoint and runtime token.
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
	Log   io.Writer
}

// Validate checks runtime configuration without network or filesystem work.
func (c Client) Validate() error {
	_, err := c.endpoint()

	return err
}

// Restore downloads an exact key into a caller-owned temporary file.
func (c Client) Restore(ctx context.Context, key, destination string) (bool, error) {
	base, err := c.endpoint()
	if err != nil {
		return false, err
	}

	lookup := *base
	lookup.Path += "cache"
	lookup.RawQuery = url.Values{"keys": {key}, "version": {version}}.Encode()

	resp, err := c.request(ctx, http.MethodGet, lookup.String(), nil, true, nil)
	if err != nil {
		return false, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: lookup HTTP %d", errCache, resp.StatusCode)
	}

	var found struct {
		ArchiveLocation string `json:"archiveLocation"`
		CacheKey        string `json:"cacheKey"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&found); err != nil {
		return false, fmt.Errorf("%w: invalid lookup response", errCache)
	}
	// The service also supports prefix matches; we do not.
	if found.CacheKey != key {
		return false, nil
	}

	if err := c.download(ctx, base, found.ArchiveLocation, destination); err != nil {
		return false, err
	}

	return true, nil
}

// Save reserves, uploads bounded chunks and commits an immutable cache entry.
func (c Client) Save(ctx context.Context, key, source string) error {
	started := time.Now()

	base, err := c.endpoint()
	if err != nil {
		return err
	}
	//nolint:gosec // G304: source is a caller-owned temporary archive, not remote input.
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	if !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > maxBytes {
		return fmt.Errorf("%w: invalid upload size", errCache)
	}

	id, err := c.reserve(ctx, base.String()+"caches", key, stat.Size())
	if err != nil || id == 0 {
		if err == nil {
			c.report("save skipped (key already reserved)", 0, started)
		}

		return err
	}

	address := base.String() + "caches/" + strconv.FormatInt(id, 10)

	for start := int64(0); start < stat.Size(); {
		size := min(int64(4<<20), stat.Size()-start)

		resp, err := c.request(ctx, http.MethodPatch, address, io.NewSectionReader(file, start, size), true, map[string]string{
			contentType: "application/octet-stream", "Content-Range": fmt.Sprintf("bytes %d-%d/*", start, start+size-1),
		})
		if err != nil {
			return err
		}

		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("%w: upload HTTP %d", errCache, resp.StatusCode)
		}

		start += size
	}

	if err := c.commit(ctx, address, stat.Size()); err != nil {
		return err
	}

	c.report("uploaded", stat.Size(), started)

	return nil
}

func (c Client) report(status string, size int64, started time.Time) {
	if c.Log != nil {
		_, _ = fmt.Fprintf(c.Log, "Cargo download cache %s: bytes=%d elapsed=%s\n", status, size, time.Since(started).Round(time.Millisecond))
	}
}

func (c Client) endpoint() (*url.URL, error) {
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || c.Token == "" {
		return nil, fmt.Errorf("%w: runtime configuration missing or invalid", errCache)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/_apis/artifactcache/"

	return parsed, nil
}

func (c Client) request(ctx context.Context, method, address string, body io.Reader, auth bool, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid request", errCache)
	}

	req.Header.Set("Accept", "application/json;api-version=6.0-preview.1")

	if section, ok := body.(*io.SectionReader); ok {
		req.ContentLength = section.Size()
	}

	if auth {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := http.Client{Timeout: 2 * time.Minute}
	if c.HTTP != nil {
		client = *c.HTTP
	}

	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: transport failed", errCache)
	} // URLs may contain credentials.

	return response, nil
}

func (c Client) download(ctx context.Context, base *url.URL, address, destination string) error {
	started := time.Now()

	location, err := url.Parse(address)
	if err != nil || location.Host == "" || location.User != nil || location.Fragment != "" {
		return fmt.Errorf("%w: invalid download URL", errCache)
	}

	sameOrigin := location.Scheme == base.Scheme && location.Host == base.Host
	if location.Scheme != "https" && (!sameOrigin || location.Scheme != "http") {
		return fmt.Errorf("%w: unsafe download URL", errCache)
	}

	response, err := c.request(ctx, http.MethodGet, location.String(), nil, sameOrigin, nil)
	if err != nil {
		return err
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: download HTTP %d", errCache, response.StatusCode)
	}
	//nolint:gosec // G304: destination is an existing caller-owned temporary file.
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	n, copyErr := io.Copy(file, io.LimitReader(response.Body, maxBytes+1))
	closeErr := file.Close()

	if copyErr != nil {
		return fmt.Errorf("%w: download interrupted", errCache)
	}

	if n > maxBytes {
		return fmt.Errorf("%w: download exceeds size limit", errCache)
	}

	if closeErr != nil {
		return closeErr
	}

	c.report("downloaded (pending verification)", n, started)

	return nil
}

func (c Client) reserve(ctx context.Context, address, key string, size int64) (int64, error) {
	body, err := json.Marshal(struct {
		Key     string `json:"key"`
		Version string `json:"version"`
		Size    int64  `json:"cacheSize"`
	}{key, version, size})
	if err != nil {
		return 0, err
	}

	resp, err := c.request(ctx, http.MethodPost, address, bytes.NewReader(body), true, map[string]string{contentType: "application/json"})
	if err != nil {
		return 0, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusConflict {
		return 0, nil
	} // Another job already owns this key.

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return 0, fmt.Errorf("%w: reserve HTTP %d", errCache, resp.StatusCode)
	}

	var reserved struct {
		CacheID int64 `json:"cacheId"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&reserved); err != nil || reserved.CacheID <= 0 {
		return 0, fmt.Errorf("%w: invalid reservation", errCache)
	}

	return reserved.CacheID, nil
}

func (c Client) commit(ctx context.Context, address string, size int64) error {
	body, err := json.Marshal(struct {
		Size int64 `json:"size"`
	}{size})
	if err != nil {
		return err
	}

	resp, err := c.request(ctx, http.MethodPost, address, bytes.NewReader(body), true, map[string]string{contentType: "application/json"})
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: commit HTTP %d", errCache, resp.StatusCode)
	}

	return nil
}
