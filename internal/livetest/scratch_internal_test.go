// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	gitlabapi "gitlab.com/gitlab-org/api/client-go"
)

func TestPrepareGitLabTagsSeedsOnlyTheFirstRelease(t *testing.T) {
	t.Parallel()

	var requests []string

	seeded := false

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)

		response.Header().Set("Content-Type", "application/json")

		switch request.Method + " " + request.URL.Path {
		case "GET /api/v4/projects/trial/fci-retention/repository/branches/main":
			if !seeded {
				response.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(response, `{"message":"404 Branch Not Found"}`)

				return
			}

			_, _ = io.WriteString(response, `{"name":"main","commit":{"id":"abc"}}`)
		case "POST /api/v4/projects/trial/fci-retention/repository/files/README.md":
			if seeded {
				t.Error("second tag attempted to recreate the existing README")
				response.WriteHeader(http.StatusBadRequest)

				return
			}

			seeded = true

			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"file_path":"README.md","branch":"main"}`)
		case "POST /api/v4/projects/trial/fci-retention/repository/tags":
			var body struct {
				TagName string `json:"tag_name"`
				Ref     string `json:"ref"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}

			if !seeded || body.Ref != "main" || (body.TagName != "v0.0.1" && body.TagName != "v0.0.2") {
				t.Errorf("tag not based on the seeded branch: %+v", body)
			}

			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"name":"`+body.TagName+`"}`)
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := gitlabapi.NewClient("inert", gitlabapi.WithBaseURL(server.URL+"/api/v4/"), gitlabapi.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}

	for _, tag := range []string{"v0.0.1", "v0.0.2"} {
		if err := prepareGitLabTag(t.Context(), client, "trial/fci-retention", tag); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{
		"GET /api/v4/projects/trial/fci-retention/repository/branches/main",
		"POST /api/v4/projects/trial/fci-retention/repository/files/README.md",
		"POST /api/v4/projects/trial/fci-retention/repository/tags",
		"GET /api/v4/projects/trial/fci-retention/repository/branches/main",
		"POST /api/v4/projects/trial/fci-retention/repository/tags",
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%v; want %v", requests, want)
	}
}

func TestPrepareGitLabTagStopsOnBranchOrSeedFailure(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name        string
		seedFailure bool
	}{{"branch denied", false}, {"seed denied", true}} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			var requests []string

			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method)

				response.Header().Set("Content-Type", "application/json")

				if scenario.seedFailure && r.Method == http.MethodGet {
					response.WriteHeader(http.StatusNotFound)
				} else {
					response.WriteHeader(http.StatusForbidden)
				}

				_, _ = io.WriteString(response, `{"message":"fixture rejection"}`)
			}))
			defer server.Close()

			client, err := gitlabapi.NewClient("inert", gitlabapi.WithBaseURL(server.URL+"/api/v4/"), gitlabapi.WithHTTPClient(server.Client()))
			if err != nil {
				t.Fatal(err)
			}

			if err := prepareGitLabTag(t.Context(), client, "trial/fci-retention", "v0.0.1"); err == nil {
				t.Fatal("tag preparation ignored rejected branch/seed")
			}

			want := []string{"GET"}
			if scenario.seedFailure {
				want = append(want, "POST")
			}

			if !reflect.DeepEqual(requests, want) {
				t.Fatalf("requests=%v; want %v", requests, want)
			}
		})
	}
}
