// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package ociregistry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestManifestPullDenialExplainsAccessMode(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		for _, authenticated := range []bool{false, true} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/" {
					w.WriteHeader(http.StatusOK)

					return
				}

				w.WriteHeader(status)
			}))
			repository := strings.TrimPrefix(server.URL, "http://") + "/org/parent"
			adapter := NewAnonymous()

			if authenticated {
				var err error

				adapter, err = WithRepositoryPullCredentials(repository, "reader", "fixture-secret", "")
				require.NoError(t, err)
				// Only this local HTTP fixture bypasses TLS; production scoped pulls require it.
				adapter.explicitTLS = false
			}

			_, err := adapter.Manifest(t.Context(), repository+"@sha256:"+strings.Repeat("a", 64))

			server.Close()
			require.Error(t, err)
			require.NotContains(t, err.Error(), "fixture-secret")

			if status == http.StatusNotFound {
				require.ErrorIs(t, err, errs.ErrMissingInput)
				require.NotContains(t, err.Error(), "pull denied")

				continue
			}

			require.ErrorIs(t, err, errs.ErrPermissionDenied)
			require.Contains(t, err.Error(), repository)

			if authenticated {
				require.Contains(t, err.Error(), "authenticated parent registry pull denied")
				require.Contains(t, err.Error(), "can read this exact package")
			} else {
				require.Contains(t, err.Error(), "anonymous registry pull denied")
				require.Contains(t, err.Error(), "make the registry package public")
				require.Contains(t, err.Error(), "PARENT_REGISTRY_READ_TOKEN")
			}
		}
	}
}
