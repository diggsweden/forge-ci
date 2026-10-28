// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejo_test

import (
	"net/http"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/provider"
	"github.com/stretchr/testify/require"
)

// Retention lists every release with all its attachments, and deletes one
// attachment by its release and id, leaving the release.
func TestReleaseRetention_ListsAndDeletesAttachments(t *testing.T) {
	t.Parallel()

	var calls []string

	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.Path)

		switch request.Method + " " + request.URL.Path {
		case "GET /api/v1/repos/itiquette/repo/releases":
			_, _ = response.Write([]byte(`[{"id":99,"tag_name":"v1.2.3","draft":false,"created_at":"2026-01-02T00:00:00Z"}]`))
		case "GET /api/v1/repos/itiquette/repo/releases/99/assets":
			_, _ = response.Write([]byte(`[{"id":1,"name":"app.tar.gz","size":1000,"browser_download_url":"https://forgejo.invalid/a"}]`))
		case "DELETE /api/v1/repos/itiquette/repo/releases/99/assets/1":
			response.WriteHeader(http.StatusNoContent)
		default:
			http.Error(response, "unexpected "+request.Method+" "+request.URL.String(), http.StatusNotFound)
		}
	})

	forgejoProvider := newProvider(handler)

	releases, err := forgejoProvider.ListReleases(t.Context(), "itiquette/repo")
	require.NoError(t, err)
	require.Len(t, releases, 1)
	require.Equal(t, provider.ListedRelease{
		ID: 99, Tag: "v1.2.3", Created: releases[0].Created,
		Assets: []provider.ListedAsset{{ID: 1, Name: "app.tar.gz", Size: 1000, URL: "https://forgejo.invalid/a"}},
	}, releases[0])
	require.Equal(t, 2, releases[0].Created.Day())

	require.NoError(t, forgejoProvider.DeleteReleaseAsset(t.Context(), "itiquette/repo", releases[0], releases[0].Assets[0]))
	require.Equal(t, "DELETE /api/v1/repos/itiquette/repo/releases/99/assets/1", calls[len(calls)-1])
}
