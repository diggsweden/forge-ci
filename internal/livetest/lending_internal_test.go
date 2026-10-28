// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

func TestLocalLendingRefusesUnacceptedForeignAndUnknownCasesBeforeEffects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		target   Target
		scenario string
		want     string
	}{
		{"unaccepted", Target{}, "job", "never accepted"},
		{"production", Target{accepted: true, Forge: provider.ForgeForgejo, Owner: labAccount, Host: "codefloe.com"}, "job", "Compose Forgejo"},
		{"foreign account", Target{accepted: true, Forge: provider.ForgeForgejo, Owner: "another", Host: "forgejo.compose.forgelab:8443"}, "job", "Compose Forgejo"},
		{"unknown case", Target{accepted: true, Forge: provider.ForgeForgejo, Owner: labAccount, Host: "forgejo.compose.forgelab:8443"}, "execute-anything", "unknown lending case"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				failure, ok := recover().(acceptanceFailure)
				if !ok || !strings.Contains(string(failure), tc.want) {
					t.Errorf("refusal=%q", failure)
				}
			}()

			CheckLocalLending(stopOnFatalTB{TB: t}, tc.target, tc.scenario)
			t.Error("guard returned")
		})
	}
}

func TestLendingProcessKeepsCredentialsOnStdinAndDropsAmbientAuthority(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ambient-canary")
	t.Setenv("CONTAINER_HOST", "ssh://foreign-engine")
	tool := filepath.Join(t.TempDir(), "inert-podman")
	// This collaborator has no engine/network capability. Its output proves the
	// concrete process boundary, not a reconstruction of the caller's arguments.
	if err := os.WriteFile(tool, []byte(`#!/bin/sh
test "$1" = --remote=false || exit 1
test "$#" = 2 || exit 2
test "$2" = inert-operation || exit 3
test -z "${GITHUB_TOKEN:-}" || exit 4
test -z "${CONTAINER_HOST:-}" || exit 5
IFS= read -r input
test "$input" = credential-canary || exit 6
printf 'stdin-only'
`), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := lendingPodman(t.Context(), tool, []byte("credential-canary\n"), "inert-operation")
	if err != nil || got != "stdin-only" {
		t.Fatalf("got %q, %v", got, err)
	}

	if writeErr := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'credential-canary' >&2\nexit 1\n"), 0o700); writeErr != nil {
		t.Fatal(writeErr)
	}

	got, err = lendingPodman(t.Context(), tool, nil, "inert-operation")
	if err == nil || got != "" || strings.Contains(err.Error(), "canary") {
		t.Fatalf("failure leaked output: %q %v", got, err)
	}
}

func TestLendingPollingPreservesCancellationAndObservationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := lendingPoll(ctx, time.Second, func() (bool, error) {
		t.Error("observed after cancellation")

		return true, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	want := errLendingObservation
	if err := lendingPoll(t.Context(), time.Second, func() (bool, error) { return false, want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
}
