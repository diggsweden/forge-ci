// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejo_test

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/adapters/httpretry"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

func TestArtifactUploadRetriesWholeFile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                                 string
		body                                 string
		status                               int
		partial, exhaust, cancel, disconnect bool
		longWait                             bool
		attempts                             int
	}{
		{name: "stored then 500", body: "release binary bytes", status: 500, attempts: 3},
		{name: "partial then 500", body: "release binary bytes", status: 500, partial: true, attempts: 3},
		{name: "empty", status: 500, attempts: 3},
		{name: "exhaustion", body: "release binary bytes", status: 500, exhaust: true, attempts: 3},
		{name: "forbidden", body: "release binary bytes", status: 403, attempts: 1},
		{name: "retry-after exceeds budget", body: "release binary bytes", status: 500, longWait: true, attempts: 1},
		{name: "cancelled", body: "release binary bytes", status: 500, cancel: true, attempts: 1},
		{name: "unknown outcome", body: "release binary bytes", disconnect: true, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			puts, creates, finalizes := 0, 0, 0
			var stored []byte
			sum := md5.Sum([]byte(tc.body))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPost:
					creates++
					writeJSON(w, jsonObj{"fileContainerResourceUrl": "http://" + r.Host + "/upload"})
				case http.MethodPut:
					puts++
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != tc.body || r.ContentLength != int64(len(tc.body)) {
						t.Errorf("attempt %d: body=%q length=%d err=%v", puts, body, r.ContentLength, err)
					}
					if r.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-%d/%d", len(tc.body)-1, len(tc.body)) || r.Header.Get("x-actions-results-md5") != base64.StdEncoding.EncodeToString(sum[:]) {
						t.Error("upload range or digest changed")
					}
					stored = body
					if tc.partial && puts < 3 {
						stored = body[:len(body)/2]
					}
					if tc.disconnect {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					if tc.cancel {
						cancel()
					}
					if puts < 3 || tc.exhaust {
						if tc.longWait {
							w.Header().Set("Retry-After", "60")
						}
						w.WriteHeader(tc.status)
					}
				case http.MethodPatch:
					finalizes++
				}
			}))
			defer srv.Close()
			p := uploadProvider(srv)
			p.HTTPClient.Transport = httpretry.NewTransport(httpretry.Config{
				Inner: p.HTTPClient.Transport, MaxAttempts: 3,
				BaseDelay: time.Nanosecond, MaxDelay: time.Nanosecond,
			})
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "binary"), tc.body)
			_, err := p.UploadRunArtifact(ctx, provider.RunArtifactUpload{Name: "dist", Dir: dir})
			srv.Close()
			if tc.exhaust || tc.attempts == 1 {
				require.Error(t, err)
				require.Zero(t, finalizes)
				if tc.exhaust || tc.longWait {
					require.ErrorIs(t, err, errs.ErrDependencyUnavailable)
					require.ErrorContains(t, err, "HTTP 500, retry policy stopped")
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.body, string(stored))
				require.Equal(t, 1, finalizes)
			}
			require.Equal(t, tc.attempts, puts)
			require.Equal(t, 1, creates)
		})
	}
}

func TestArtifactReadRetriesInternalServerError(t *testing.T) {
	t.Parallel()

	for _, permanent := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovery", true: "exhaustion"}[permanent], func(t *testing.T) {
			t.Parallel()

			reads := 0
			srv := serveRuntime(http.HandlerFunc(func(response http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/_apis/pipelines/workflows/7/artifacts":
					writeJSON(response, jsonObj{"value": []jsonObj{{"name": "dist", "fileContainerResourceUrl": runtimeFakeURL + "/container"}}})
				case "/container":
					writeJSON(response, jsonObj{"value": []jsonObj{{"path": "dist/sbom.json", "itemType": "file", "contentLocation": runtimeFakeURL + "/file"}}})
				case "/file":
					reads++
					if reads < 3 || permanent {
						response.WriteHeader(http.StatusInternalServerError)
					} else {
						_, _ = response.Write([]byte("verified evidence"))
					}
				}
			}))
			srv.client.Transport = httpretry.NewTransport(httpretry.Config{
				Inner: srv.client.Transport, MaxAttempts: 3,
				BaseDelay: time.Nanosecond, MaxDelay: time.Nanosecond,
			})
			dir := t.TempDir()

			_, err := runtimeProvider(srv).DownloadRunArtifact(t.Context(), provider.RunArtifactDownload{Name: "dist", Dir: dir})
			if permanent {
				require.ErrorIs(t, err, errs.ErrDependencyUnavailable)
				require.ErrorContains(t, err, `artifact "dist" entry "sbom.json"`)
				require.ErrorContains(t, err, "HTTP 500, retries exhausted")
				require.NoFileExists(t, filepath.Join(dir, "sbom.json"))
			} else {
				require.NoError(t, err)

				body, readErr := os.ReadFile(filepath.Join(dir, "sbom.json"))
				require.NoError(t, readErr)
				require.Equal(t, "verified evidence", string(body))
			}

			require.Equal(t, 3, reads)
		})
	}
}
