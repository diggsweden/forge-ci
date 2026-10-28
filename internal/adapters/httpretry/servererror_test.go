// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package httpretry_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/diggsweden/forge-ci/internal/adapters/httpretry"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestInternalServerErrorRetryPolicy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, method string
		optIn        bool
		failures     int
		attempts     int
		exhausted    bool
	}{
		{"default_is_terminal", http.MethodGet, false, 10, 1, false},
		{"read_recovers", http.MethodGet, true, 2, 3, false},
		{"head_recovers", http.MethodHead, true, 2, 3, false},
		{"read_exhausts", http.MethodGet, true, 10, 3, true},
		{"post_once", http.MethodPost, true, 10, 1, false},
		{"put_once", http.MethodPut, true, 10, 1, false},
		{"patch_once", http.MethodPatch, true, 10, 1, false},
		{"delete_once", http.MethodDelete, true, 10, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			inner := serveInMemory(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls <= tc.failures {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(http.StatusInternalServerError)
				}
			})
			clock := newFakeClock()
			base := httpretry.NewTransport(httpretry.Config{Inner: inner, Clock: clock, MaxAttempts: 3, MaxDelay: time.Second, MaxCumulativeDelay: 2 * time.Second})

			transport := base
			if tc.optIn {
				transport = httpretry.WithInternalServerErrorRetries(base)
			}

			req, err := http.NewRequestWithContext(t.Context(), tc.method, testURL, nil)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := transport.RoundTrip(req)
			if resp != nil {
				defer func() { _ = resp.Body.Close() }()
			}

			if tc.exhausted {
				if !errors.Is(err, errs.ErrDependencyUnavailable) || resp != nil {
					t.Fatalf("expected exhausted dependency failure, got response=%v error=%v", resp, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}

			if calls != tc.attempts || len(clock.Waited()) != tc.attempts-1 {
				t.Fatalf("attempts=%d waits=%v, expected %d attempts", calls, clock.Waited(), tc.attempts)
			}

			// Deriving the artifact policy must not alter other users of the
			// original transport, or wrap its retry loop inside another one.
			calls = 0

			originalResponse, originalErr := base.RoundTrip(req)
			if originalErr != nil {
				t.Fatal(originalErr)
			}
			defer func() { _ = originalResponse.Body.Close() }()

			if calls != 1 || originalResponse.StatusCode != http.StatusInternalServerError {
				t.Fatalf("original transport's policy changed: calls=%d status=%d", calls, originalResponse.StatusCode)
			}
		})
	}
}

func TestInternalServerErrorRetryHonoursCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	clock := newFakeClock()
	calls := 0
	inner := serveInMemory(func(w http.ResponseWriter, _ *http.Request) {
		calls++

		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	})
	transport := httpretry.WithInternalServerErrorRetries(httpretry.NewTransport(httpretry.Config{Inner: inner, Clock: clock}))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := transport.RoundTrip(req)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}

	if !errors.Is(err, context.Canceled) || resp != nil || calls != 1 || len(clock.Waited()) != 0 {
		t.Fatalf("cancellation must stop retries: response=%v error=%v calls=%d waits=%v", resp, err, calls, clock.Waited())
	}
}
