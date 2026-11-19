// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package ociregistry

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

type pullAuthTestTransport func(*http.Request) (*http.Response, error)

func (f pullAuthTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestScopedPullRejectsCredentialsOutsideTrustedOrigins(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		url        string
		authOrigin string
		authorize  bool
		wantPassed bool
	}{
		{"https://registry.example/v2/", "", true, true},
		{"https://REGISTRY.example:443/v2/", "", true, true},
		{"https://registry.example:8443/v2/", "", true, false},
		{"https://tokens.example:8443/token", "", true, false},
		{"https://tokens.example:8443/token", "https://tokens.example:8443", true, true},
		{"https://tokens.example/token", "https://tokens.example:8443", true, false},
		{"https://other.registry.example/blobs", "", true, false},
		{"https://storage.example/blobs", "", false, true},
		{"http://tokens.example:8443/token", "https://tokens.example:8443", true, false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()

			adapter, err := WithRepositoryPullCredentials("registry.example/org/image", "reader", "fixture", tc.authOrigin)
			require.NoError(t, err)
			forwarded := false
			transport := verifiedRegistryTransport{
				trustedAuth: adapter.pullAuth.trusted,
				base: pullAuthTestTransport(func(*http.Request) (*http.Response, error) {
					forwarded = true
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				}),
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.url, nil)
			require.NoError(t, err)
			if tc.authorize {
				req.SetBasicAuth("reader", "fixture")
			}
			resp, err := transport.RoundTrip(req)
			if tc.wantPassed {
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
			} else {
				require.ErrorIs(t, err, errs.ErrValidation)
			}
			require.Equal(t, tc.wantPassed, forwarded)
		})
	}
}

func TestScopedPullTokenPostsRequireTrustedOrigin(t *testing.T) {
	t.Parallel()
	adapter, err := WithRepositoryPullCredentials("registry.example/org/image", "reader", "fixture", "https://tokens.example")
	require.NoError(t, err)
	transport := verifiedRegistryTransport{trustedAuth: adapter.pullAuth.trusted, base: pullAuthTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})}
	for _, tc := range []struct {
		endpoint string
		trusted  bool
	}{{"https://tokens.example/exchange", true}, {"https://other.example/exchange", false}} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, tc.endpoint, nil)
		require.NoError(t, err)
		resp, err := transport.RoundTrip(req)
		if !tc.trusted {
			require.ErrorIs(t, err, errs.ErrValidation)
			continue
		}
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
}

func TestScopedPullAuthOriginRejectsNonOrigins(t *testing.T) {
	t.Parallel()

	for _, origin := range []string{"http://tokens.example", "//tokens.example", "https://tokens.example/token", "https://tokens.example/?", "https://tokens.example/#", "https://user@tokens.example"} {
		_, err := WithRepositoryPullCredentials("registry.example/org/image", "reader", "fixture", origin)
		require.ErrorIs(t, err, errs.ErrInvalidConfig, origin)
	}
}
