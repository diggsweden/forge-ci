// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func authOf(t *testing.T, body []byte, registry string) string {
	t.Helper()

	var doc struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}

	return doc.Auths[registry].Auth
}

func TestMergeAuth_NewConfig(t *testing.T) {
	t.Parallel()

	for _, existing := range [][]byte{nil, {}, []byte(" \n\t"), []byte(`{}`)} {
		out, err := container.MergeAuth(existing, "ghcr.io", "publisher", "s3cret")
		if err != nil {
			t.Fatal(err)
		}

		assertAuthJSON(t, out, `{"auths":{"ghcr.io":{"auth":"cHVibGlzaGVyOnMzY3JldA=="}}}`) // gitleaks:allow -- base64 of publisher:s3cret, a synthetic auth fixture.
	}
}

func TestMergeAuth_PreservesOtherRegistries(t *testing.T) {
	t.Parallel()

	existing := []byte(`{"auths":{"ghcr.io":{"auth":"keep"}},"credsStore":"x"}`)

	out, err := container.MergeAuth(existing, "forge.example", "bot", "tok")
	if err != nil {
		t.Fatal(err)
	}

	if got := authOf(t, out, "ghcr.io"); got != "keep" {
		t.Errorf("existing ghcr.io entry clobbered: %q", got)
	}

	if got := authOf(t, out, "forge.example"); got != base64.StdEncoding.EncodeToString([]byte("bot:tok")) {
		t.Errorf("forge.example not added: %q", got)
	}

	// Unrelated top-level fields survive.
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}

	if doc["credsStore"] != "x" {
		t.Errorf("credsStore dropped: %v", doc["credsStore"])
	}
}

// TestMergeAuth_ReauthenticatingReplacesTheCredential covers the case the
// other merge tests do not: a registry that already has an entry. Logging
// in again must replace the credential rather than add a second one.
//
// It also verifies that mutually exclusive authentication forms are not kept
// together: clients prefer identitytoken, so it must not shadow the new auth.
func TestMergeAuth_ReauthenticatingReplacesTheCredential(t *testing.T) {
	t.Parallel()

	existing := []byte(`{"auths":{"ghcr.io":{"auth":"b2xkOnBhc3M=","identitytoken":"stale-token","email":"a@b.c"}}}`)

	out, err := container.MergeAuth(existing, "ghcr.io", "publisher", "newpass")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := authOf(t, out, "ghcr.io"), base64.StdEncoding.EncodeToString([]byte("publisher:newpass")); got != want {
		t.Errorf("auth = %q, want the new credential %q", got, want)
	}

	var doc struct {
		Auths map[string]map[string]any `json:"auths"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}

	// Exactly one entry for the registry, not a duplicate alongside it.
	if got := len(doc.Auths); got != 1 {
		t.Errorf("auths holds %d registries, want 1: %v", got, doc.Auths)
	}

	// Unrelated sibling fields survive, while the stale alternate credential is
	// removed so clients use the newly supplied username/password.
	entry := doc.Auths["ghcr.io"]
	if entry["email"] != "a@b.c" {
		t.Errorf("email dropped: %v", entry["email"])
	}

	if _, ok := entry["identitytoken"]; ok {
		t.Errorf("stale identitytoken was retained: %v", entry["identitytoken"])
	}
}

func TestMergeAuth_RejectsMissingFields(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		registry string
		username string
		password string
	}{
		{"", "u", "p"},
		{"ghcr.io", "", "p"},
		{"ghcr.io", "u", ""},
	} {
		out, err := container.MergeAuth(nil, tc.registry, tc.username, tc.password)
		if out != nil || !errors.Is(err, errs.ErrUsage) {
			t.Errorf("missing field: out=%q err=%v, want nil/ErrUsage", out, err)
		}
	}
}

func TestAuth_RejectsNonObjectAndMalformedDocuments(t *testing.T) {
	t.Parallel()

	for _, existing := range []string{"null", "[]", `"text"`, "42", "true", "{not json", `{} {}`, `{} null`, `{} trailing`, "{}\v", "{}\u00a0", "{}\x00"} {
		t.Run(existing, func(t *testing.T) {
			t.Parallel()

			out, err := container.MergeAuth([]byte(existing), "ghcr.io", "u", "p")
			if out != nil || !errors.Is(err, errs.ErrMalformedInput) {
				t.Errorf("merge: out=%q err=%v, want nil/ErrMalformedInput", out, err)
			}
		})
	}
}

func TestAuth_NonObjectNestedValues(t *testing.T) {
	t.Parallel()

	for _, shape := range []string{"null", "[]", `"text"`, "42", "true"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			// Merge repairs a non-object auths value.
			existing := []byte(`{"auths":` + shape + `,"counter":9007199254740993}`)

			out, err := container.MergeAuth(existing, "ghcr.io", "u", "p")
			if err != nil {
				t.Fatal(err)
			}

			assertAuthJSON(t, out, `{"auths":{"ghcr.io":{"auth":"dTpw"}},"counter":9007199254740993}`)

			// A present target is replaced, whatever its shape.
			existing = []byte(`{"auths":{"forge.example":{"auth":"keep"},"ghcr.io":` + shape + `}}`)

			out, err = container.MergeAuth(existing, "ghcr.io", "u", "p")
			if err != nil {
				t.Fatal(err)
			}

			assertAuthJSON(t, out, `{"auths":{"forge.example":{"auth":"keep"},"ghcr.io":{"auth":"dTpw"}}}`)
		})
	}
}

func TestAuth_PreservesExactNumbers(t *testing.T) {
	t.Parallel()

	const existing = `{"auths":{"ghcr.io":{"auth":"old","identitytoken":"stale","counter":9007199254740993},"forge.example":{"auth":"keep","counter":-9007199254740993}},"counter":9007199254740993,"metadata":[1e400,0.12345678901234567890123456789,null,true]}`

	t.Run("merge", func(t *testing.T) {
		t.Parallel()

		body := []byte(existing)

		out, err := container.MergeAuth(body, "ghcr.io", "u", "p")
		if err != nil {
			t.Fatal(err)
		}

		assertAuthJSON(t, out, `{"auths":{"forge.example":{"auth":"keep","counter":-9007199254740993},"ghcr.io":{"auth":"dTpw","counter":9007199254740993}},"counter":9007199254740993,"metadata":[1e400,0.12345678901234567890123456789,null,true]}`)

		if string(body) != existing {
			t.Fatal("merge mutated its input")
		}
	})
}

func assertAuthJSON(t *testing.T, body []byte, want string) {
	t.Helper()

	// Compact without decoding, so the oracle cannot round numbers through float64.
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		t.Fatal(err)
	}

	if got := compact.String(); got != want {
		t.Errorf("auth JSON = %s, want %s", got, want)
	}
}
