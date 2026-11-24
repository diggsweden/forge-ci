// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/fakeoutputsink"
)

var errPublicationFixture = errors.New("inert publication failure")

type fakeManifestPushRegistry struct {
	raw           []byte
	failures      int
	queried       []string
	pushFailures  int
	digest        string
	invalidDigest bool
	pushes        []string
	layouts       []string
	onPush        func(string, string)
}

func (f *fakeManifestPushRegistry) Manifest(_ context.Context, ref string) ([]byte, error) {
	f.queried = append(f.queried, ref)
	if f.failures > 0 {
		f.failures--

		return nil, errPublicationFixture
	}

	return f.raw, nil
}

func (f *fakeManifestPushRegistry) PushLayout(_ context.Context, directory, destination string) (string, error) {
	return f.push(directory, destination)
}

func (f *fakeManifestPushRegistry) PushIndexLayout(_ context.Context, directory, destination string) (string, error) {
	return f.push(directory, destination)
}

func (f *fakeManifestPushRegistry) push(directory, destination string) (string, error) {
	f.pushes, f.layouts = append(f.pushes, destination), append(f.layouts, directory)
	if f.onPush != nil {
		f.onPush(directory, destination)
	}

	if f.pushFailures > 0 {
		f.pushFailures--

		return "", errPublicationFixture
	}

	if f.invalidDigest || f.digest != "" {
		return f.digest, nil
	}

	return manifestTestDigest(f.raw), nil
}

func TestResolvePushedManifestDigest_OptionalLegacyFileAndRetries(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"schemaVersion":2}`)

	digest := manifestTestDigest(raw)

	// A digest file that is named and cannot be read fails before the
	// registry is asked: the registry's own answer would verify nothing.
	unread := &fakeManifestPushRegistry{raw: raw}
	if result, err := appcontainer.ResolvePushedManifestDigest(t.Context(), unread, fakeoutputsink.New(t), io.Discard, appcontainer.PushedManifestDigestInput{
		Ref: "registry.example/app:v1", DigestFile: filepath.Join(t.TempDir(), "absent"),
	}); !errors.Is(err, errs.ErrMissingInput) || result != nil || len(unread.queried) != 0 {
		t.Fatalf("an unreadable digest file was passed over: %+v %v queried=%v", result, err, unread.queried)
	}

	// Nor does a file that holds no digest leave the registry to vouch for itself.
	for _, content := range []string{"", "\n", "not-a-digest", "sha256:" + strings.Repeat("a", 63), "sha512:" + strings.Repeat("a", 64), strings.Repeat("a", 64)} {
		file := filepath.Join(t.TempDir(), "digest")
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		registry := &fakeManifestPushRegistry{raw: raw}
		if result, err := appcontainer.ResolvePushedManifestDigest(t.Context(), registry, fakeoutputsink.New(t), io.Discard, appcontainer.PushedManifestDigestInput{
			Ref: "registry.example/app:v1", DigestFile: file,
		}); !errors.Is(err, errs.ErrMalformedInput) || result != nil || len(registry.queried) != 0 {
			t.Fatalf("a digest file holding %q was passed over: %+v %v", content, result, err)
		}
	}

	for _, value := range []string{"", digest + "\n", "sha256:" + strings.Repeat("0", 64)} {
		input := appcontainer.PushedManifestDigestInput{Ref: "registry.example/app:v1", RetryAttempts: 2, RetryDelay: time.Nanosecond}
		if value != "" {
			input.DigestFile = filepath.Join(t.TempDir(), "digest")
			if err := os.WriteFile(input.DigestFile, []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		registry := &fakeManifestPushRegistry{raw: raw, failures: 1}
		sink := fakeoutputsink.New(t)

		var log bytes.Buffer

		result, err := appcontainer.ResolvePushedManifestDigest(t.Context(), registry, sink, &log, input)
		if strings.HasSuffix(value, strings.Repeat("0", 64)) {
			if !errors.Is(err, errs.ErrValidation) || result != nil || len(sink.Keys()) != 0 {
				t.Fatal("mismatched legacy digest accepted")
			}

			continue
		}

		if err != nil || result.Digest != digest || sink.Single("ref") != "registry.example/app:v1@"+digest {
			t.Fatalf("legacy lookup: %+v %v", result, err)
		}

		if !slices.Equal(registry.queried, []string{input.Ref, input.Ref}) {
			t.Fatal("lookup retried another reference")
		}
	}
}

func TestResolvePushedManifestDigest_InputRefusals(t *testing.T) {
	t.Parallel()

	if _, err := appcontainer.ResolvePushedManifestDigest(t.Context(), nil, nil, io.Discard, appcontainer.PushedManifestDigestInput{Ref: "registry.example/app:v1"}); !errors.Is(err, errs.ErrUsage) {
		t.Fatal(err)
	}

	for _, ref := range []string{"", " ", "registry.example/app:v1 extra", "registry.example/app:v1\nflag"} {
		registry := &fakeManifestPushRegistry{}

		_, err := appcontainer.ResolvePushedManifestDigest(t.Context(), registry, nil, io.Discard, appcontainer.PushedManifestDigestInput{Ref: ref, RetryAttempts: 1})
		if !errors.Is(err, errs.ErrUsage) || len(registry.queried) != 0 {
			t.Fatalf("invalid lookup had effects: %v", err)
		}
	}
}

func manifestTestDigest(raw []byte) string {
	sum := sha256.Sum256(raw)

	return "sha256:" + hex.EncodeToString(sum[:])
}

type deniedManifestRegistry struct {
	fakeManifestPushRegistry

	calls int
}

func (d *deniedManifestRegistry) Manifest(context.Context, string) ([]byte, error) {
	d.calls++

	return nil, fmt.Errorf("token refused: %w", errs.ErrPermissionDenied)
}

// A refused credential stops at once: retrying cannot change it, and each
// wait only delays the error a person needs to see.
func TestResolvePushedManifestDigest_DoesNotRetryAPermissionDenial(t *testing.T) {
	t.Parallel()

	registry := &deniedManifestRegistry{}

	_, err := appcontainer.ResolvePushedManifestDigest(t.Context(), registry, nil, io.Discard,
		appcontainer.PushedManifestDigestInput{Ref: "registry.example/app:v1", RetryAttempts: 3, RetryDelay: time.Hour})
	if !errors.Is(err, errs.ErrPermissionDenied) || registry.calls != 1 {
		t.Fatalf("err = %v after %d calls, want one permission-denied call", err, registry.calls)
	}
}
