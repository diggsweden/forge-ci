// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package actionscache

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV1RoundTrip(t *testing.T) {
	var (
		stored []byte
		mutex  sync.Mutex
	)

	committed := false

	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()

		assert.Equal(t, "Bearer runtime-token", req.Header.Get("Authorization"))

		switch req.Method + " " + req.URL.Path {
		case "POST /_apis/artifactcache/caches":
			var request map[string]any
			assert.NoError(t, json.NewDecoder(req.Body).Decode(&request))
			assert.Equal(t, "key", request["key"])
			assert.Equal(t, version, request["version"])

			_, _ = io.WriteString(writer, `{"cacheId":1}`)
		case "PATCH /_apis/artifactcache/caches/1":
			assert.Equal(t, "bytes 0-4/*", req.Header.Get("Content-Range"))
			assert.Equal(t, int64(5), req.ContentLength)
			stored, _ = io.ReadAll(req.Body)

			writer.WriteHeader(http.StatusNoContent)
		case "POST /_apis/artifactcache/caches/1":
			var request map[string]int64
			assert.NoError(t, json.NewDecoder(req.Body).Decode(&request))
			assert.Equal(t, int64(5), request["size"])

			committed = true

			writer.WriteHeader(http.StatusNoContent)
		case "GET /_apis/artifactcache/cache":
			assert.Equal(t, "key", req.URL.Query().Get("keys"))
			assert.Equal(t, version, req.URL.Query().Get("version"))
			assert.True(t, committed)

			_ = json.NewEncoder(writer).Encode(map[string]string{"cacheKey": "key", "archiveLocation": server.URL + "/archive"})
		case "GET /archive":
			_, _ = writer.Write(stored)
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	var diagnostics bytes.Buffer

	c := Client{URL: server.URL, Token: "runtime-token", Log: &diagnostics}
	source := filepath.Join(t.TempDir(), "source")
	require.NoError(t, os.WriteFile(source, []byte("crate"), 0o600))
	require.NoError(t, c.Save(t.Context(), "key", source))
	destination := filepath.Join(t.TempDir(), "destination")
	require.NoError(t, os.WriteFile(destination, nil, 0o600))
	hit, err := c.Restore(t.Context(), "key", destination)
	require.NoError(t, err)
	require.True(t, hit)

	data, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "crate", string(data))
	require.Contains(t, diagnostics.String(), "uploaded: bytes=5 elapsed=")
	require.Contains(t, diagnostics.String(), "downloaded (pending verification): bytes=5 elapsed=")
	require.NotContains(t, diagnostics.String(), "runtime-token")
	require.NotContains(t, diagnostics.String(), server.URL)
}

func TestChunkFailureDoesNotCommitAndConflictDoesNotUpload(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(strconv.FormatBool(conflict), func(t *testing.T) {
			var (
				mutex  sync.Mutex
				ranges []string
			)

			commits := 0

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
				mutex.Lock()
				defer mutex.Unlock()

				switch {
				case req.URL.Path == "/_apis/artifactcache/caches":
					if conflict {
						writer.WriteHeader(http.StatusConflict)

						return
					}

					_, _ = io.WriteString(writer, `{"cacheId":2}`)
				case req.Method == http.MethodPatch:
					ranges = append(ranges, req.Header.Get("Content-Range"))
					_, _ = io.Copy(io.Discard, req.Body)

					if len(ranges) == 2 {
						writer.WriteHeader(http.StatusInternalServerError)
					} else {
						writer.WriteHeader(http.StatusNoContent)
					}
				case req.Method == http.MethodPost:
					commits++
				default:
					t.Error("unexpected cache request")
				}
			}))
			defer server.Close()

			file := filepath.Join(t.TempDir(), "cache")
			require.NoError(t, os.WriteFile(file, bytes.Repeat([]byte("x"), (4<<20)+1), 0o600))
			err := (Client{URL: server.URL, Token: "token"}).Save(t.Context(), "key", file)

			mutex.Lock()
			defer mutex.Unlock()

			require.Zero(t, commits)

			if conflict {
				require.NoError(t, err)
				require.Empty(t, ranges)
			} else {
				require.Error(t, err)
				require.Equal(t, []string{"bytes 0-4194303/*", "bytes 4194304-4194304/*"}, ranges)
			}
		})
	}
}

func TestMissPrefixErrorAndRedirect(t *testing.T) {
	for _, status := range []int{204, 200, 500, 302} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://example.invalid/token")
				w.WriteHeader(status)

				if status == 200 {
					_, _ = io.WriteString(w, `{"cacheKey":"prefix-match","archiveLocation":"https://example.invalid/archive"}`)
				}
			}))
			defer server.Close()

			c := Client{URL: server.URL, Token: "private-token"}
			hit, err := c.Restore(t.Context(), "exact-key", "unused")
			require.False(t, hit)

			if status < 300 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-token")
			}
		})
	}
}

func TestDownloadDoesNotSendTokenToOtherOrigin(t *testing.T) {
	archive := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))

		_, _ = io.WriteString(w, "data")
	}))
	defer archive.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"cacheKey": "key", "archiveLocation": archive.URL})
	}))
	defer server.Close()

	c := Client{URL: server.URL, Token: "private", HTTP: archive.Client()}
	file := filepath.Join(t.TempDir(), "archive")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	hit, err := c.Restore(t.Context(), "key", file)
	require.NoError(t, err)
	require.True(t, hit)
}
