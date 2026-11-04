// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build live

package githubconsumer_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"codefloe.com/itiquette/forge-lab-contract/githubcontext"
	"github.com/diggsweden/forge-ci/internal/livetest"
)

// Subprocesses let the real runTrial and native SDK terminate via testing.Fatal
// without weakening production error handling or introducing a trial interface.
func TestRunTrialSigningAdmission(t *testing.T) {
	check := func(name string, creates int, failure string) {
		t.Run(name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRunTrialSigningBoundaryChild$", "-test.v")
			command.Env = append(os.Environ(), "FORGE_CI_SIGNING_BOUNDARY_CASE="+name)
			body, err := command.CombinedOutput()
			output := string(body)
			if err == nil || !strings.Contains(output, failure) ||
				!strings.Contains(output, fmt.Sprintf("BOUNDARY creates=%d unexpected=0", creates)) {
				t.Fatalf("runTrial admission did not reach the expected boundary: %v\n%s", err, output)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		creates int
		failure string
	}{
		{"missing", 0, "read the fixture's release workflow"},
		{"selected-missing", 0, "read the fixture's release workflow"},
		{"read-error", 0, "read the fixture's release workflow"},
		{"wildcard", 0, "literal bot-email"},
		{"expression", 0, "literal bot-email"},
		{"valid", 1, "livetest: create"},
		{"selected", 1, "livetest: create"},
		{"pull-request", 1, "livetest: create"},
		{"dev-release", 1, "livetest: create"},
		{"pull-request-alias", 1, "livetest: create"},
		{"dev-release-alias", 1, "livetest: create"},
	} {
		check(tc.name, tc.creates, tc.failure)
	}
	for _, tc := range signingAliasCases {
		if tc.failure != "" {
			check("alias-"+tc.name, 0, tc.failure)
		} else {
			check("alias-"+tc.name, 1, "livetest: create")
		}
	}
}

func TestRunTrialSigningBoundaryChild(t *testing.T) {
	name := os.Getenv("FORGE_CI_SIGNING_BOUNDARY_CASE")
	if name == "" {
		t.Skip("inert subprocess only")
	}

	var creates, unexpected atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /orgs/test-org":
			_, _ = w.Write([]byte(`{"id":42,"login":"test-org"}`))
		case "GET /user":
			_, _ = w.Write([]byte(`{"id":7,"login":"actor"}`))
		case "POST /orgs/test-org/repos":
			creates.Add(1)
			// Structured refusal stops the native client before any remote allocation,
			// SSH key generation, source push or artifact/runner activity.
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"inert-create-stop"}`))
		default:
			unexpected.Add(1)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()
	t.Cleanup(func() { fmt.Printf("BOUNDARY creates=%d unexpected=%d\n", creates.Load(), unexpected.Load()) })

	// OpenGitHubTrial clones the default *http.Transport. Confine that clone
	// to this TLS fixture, verifying its test certificate rather than disabling TLS.
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()

	root := t.TempDir()
	fixtures := filepath.Join(root, "fixtures/workflows")
	if err := os.MkdirAll(fixtures, 0o700); err != nil {
		t.Fatal(err)
	}
	event, workflow, email := livetest.TrialRelease, "release.yml", "release@example.org"
	if name == "wildcard" {
		email = "*"
	}
	if name == "expression" {
		email = "${{ vars.RELEASE_EMAIL }}"
	}
	flow := strings.TrimSuffix(name, "-alias")
	if flow == "pull-request" || flow == "dev-release" {
		event, workflow, email = flow, "pullrequest.yml", "*"
	}
	if name == "read-error" {
		if err := os.Mkdir(filepath.Join(fixtures, workflow), 0o700); err != nil {
			t.Fatal(err)
		}
	} else if name != "missing" {
		if err := os.WriteFile(filepath.Join(fixtures, "release.yml"), signingWorkflow(t, email, true, false), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if name == "selected-missing" {
		workflow = "absent.yaml"
	}
	if name == "selected" {
		workflow = "stable.yaml"
		if err := os.WriteFile(filepath.Join(fixtures, "release.yml"), signingWorkflow(t, "*", true, false), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixtures, workflow), signingWorkflow(t, "release@example.org", true, false), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range signingAliasCases {
		if name == "alias-"+tc.name || (event != livetest.TrialRelease && strings.HasSuffix(name, "-alias") && tc.name == "secrets-map-invalid") {
			if err := os.WriteFile(filepath.Join(fixtures, "release.yml"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if event != livetest.TrialRelease {
		if err := os.WriteFile(filepath.Join(fixtures, workflow), []byte("jobs: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := livetest.ParseGitHubTrialSettings(event, []string{"workflow=" + workflow})
	if err != nil {
		t.Fatal(err)
	}

	document := githubcontext.Context{Version: githubcontext.Version, Session: strings.Repeat("a", 32),
		Organization: githubcontext.Identity{ID: 42, Login: "test-org"}, Actor: githubcontext.Identity{ID: 7, Login: "actor"},
		Prefix: "ex-", Token: "INERT-test-token", FixtureManifestFile: filepath.Join(root, "manifest.json"), Capabilities: []string{"repositories"}}
	body, err := json.Marshal(struct {
		githubcontext.Context
		Repositories []githubcontext.Repository `json:"repositories"`
	}{document, []githubcontext.Repository{}})
	if err != nil {
		t.Fatal(err)
	}
	contextFile := filepath.Join(root, "context.json")
	if err := os.WriteFile(contextFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(livetest.GitHubContextEnv, contextFile)
	t.Setenv("FORGE_CI_LIVE_DEMO", "")
	trial := livetest.OpenGitHubTrial(t, 42)
	runTrial(t, trial, livetest.GitHubEngine{}, root, "fixtures", settings, livetest.ConsumerVisibility{}, "")
	t.Fatal("inert create refusal or invalid signing input must stop runTrial")
}
