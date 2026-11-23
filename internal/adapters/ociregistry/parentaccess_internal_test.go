// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package ociregistry

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// Repository visibility says nothing about package visibility. In particular,
// successful producer verification with a publishing credential does not prove
// that the anonymous (or separately authenticated) native builder can pull.
func TestParentAccessIsIndependentOfPublicationCredentials(t *testing.T) {
	manifest, err := empty.Image.RawManifest()
	require.NoError(t, err)
	digest, err := empty.Image.Digest()
	require.NoError(t, err)

	var (
		public atomic.Bool
		server *httptest.Server
	)

	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			user, password, _ := request.BasicAuth()

			allowed := public.Load() || (user == "publisher" && password == "publish-fixture") || (user == "reader" && password == "pull-fixture")
			if !allowed {
				writer.WriteHeader(http.StatusForbidden)
				_, _ = writer.Write([]byte(`{"errors":[{"code":"DENIED","message":"package access denied"}]}`))

				return
			}

			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"token":"read-session"}`))

			return
		}

		if request.Header.Get("Authorization") != "Bearer read-session" {
			writer.Header().Set("Www-Authenticate", `Bearer realm="`+server.URL+`/token",service="registry"`)
			writer.WriteHeader(http.StatusUnauthorized)

			return
		}

		if request.URL.Path == "/v2/" {
			return
		}

		if request.URL.Path != "/v2/org/parent/manifests/"+digest.String() {
			writer.WriteHeader(http.StatusNotFound)

			return
		}

		writer.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		_, _ = writer.Write(manifest)
	}))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	repository := host + "/org/parent"
	ref := repository + "@" + digest.String()
	configDir := t.TempDir()
	config := filepath.Join(configDir, "config.json")
	login := base64.StdEncoding.EncodeToString([]byte("publisher:publish-fixture"))
	require.NoError(t, os.WriteFile(config, fmt.Appendf(nil, `{"auths":{%q:{"auth":%q}}}`, host, login), 0o600))
	t.Setenv("DOCKER_CONFIG", configDir)

	_, err = WithAuthFile(config).Manifest(t.Context(), ref)
	require.NoError(t, err, "producer verification can read the private package")
	_, err = NewAnonymous().Manifest(t.Context(), ref)
	require.ErrorIs(t, err, errs.ErrPermissionDenied, "builder must not inherit publication authority")
	require.Contains(t, err.Error(), "anonymous registry pull denied")

	for _, tc := range []struct {
		repository, password string
		allowed              bool
	}{
		{repository, "pull-fixture", true},
		{repository, "wrong-fixture", false},
		{host + "/org/other", "pull-fixture", false},
	} {
		reader, err := WithRepositoryPullCredentials(tc.repository, "reader", tc.password, "")
		require.NoError(t, err)
		// Local HTTP transport fixture only; production scoped pulls retain
		// verified TLS and the origin restrictions tested in pullauth tests.
		reader.explicitTLS = false

		got, err := reader.Manifest(t.Context(), ref)
		if tc.allowed {
			require.NoError(t, err)
			require.Equal(t, manifest, got)
		} else {
			require.Error(t, err)
			require.NotContains(t, err.Error(), tc.password)
		}
	}

	public.Store(true)

	got, err := NewAnonymous().Manifest(t.Context(), ref)
	require.NoError(t, err, "making the package public enables anonymous builder access")
	require.Equal(t, manifest, got)
}
