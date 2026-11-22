// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The isolation step names the repository's workflow directory, whose release
// workflows the engine finds itself, and the subject, and turns a
// whitespace-separated signing-secret list into repeated --signing-secret
// flags. The helper-pin input, the literal inputs and the layer's own
// signing-secret names are the engine's defaults, so the step and the
// layer's own workflows run one check. Isolation analysis belongs to the
// engine.
func TestCheckL3Isolation(t *testing.T) {
	t.Parallel()
	action := loadAction(t, "check-l3-isolation")
	action.requireSteps(t, 1)
	script := action.Runs.Steps[0].Run
	base := []string{"validate", "isolation", "--workflows-dir", ".forgejo/workflows", "--single-pin-subject", "served/layer"}

	if value := fmt.Sprint(action.Inputs["signing-secrets"].Default); value != "" {
		t.Fatalf("signing-secrets defaults to %q, want empty: the engine owns the layer's names", value)
	}

	for _, test := range []struct {
		name, secrets, status string
		want                  int
		flags                 []string
	}{
		{"splits on runs of whitespace", " ALPHA \t BETA\n", "0", 0, []string{"--signing-secret", "ALPHA", "--signing-secret", "BETA"}},
		{"leaves the engine its own names for an empty list", "", "0", 0, nil},
		{"propagates the engine status", "ALPHA", "3", 3, []string{"--signing-secret", "ALPHA"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			box := newSandbox(t, "check-l3-isolation")

			got := box.run(script, "GITHUB_ACTION_REPOSITORY=served/layer",
				"ISOLATION_SIGNING_SECRETS="+test.secrets, "FAKE_ENGINE_STATUS="+test.status)
			want := slices.Concat(base, []string{"--contract-root", box.actionDir + "/../../.."}, test.flags)

			if got.status != test.want || !slices.Equal(box.argv(), want) {
				t.Fatalf("status %d argv %q, want %d %q; stderr %s", got.status, box.argv(), test.want, want, got.stderr)
			}
		})
	}
}

// The action takes no workflow, job or output name: the engine derives them.
func TestCheckL3IsolationNamesNoWorkflowOrJob(t *testing.T) {
	t.Parallel()

	action := loadAction(t, "check-l3-isolation")
	for _, input := range []string{"workflow", "sign-job", "prepare-job", "dist-digest-output", "build-job"} {
		if _, ok := action.Inputs[input]; ok {
			t.Errorf("check-l3-isolation declares %s; the engine finds the release workflows and their jobs", input)
		}
	}
}

// The single-pin subject is the input when the caller names one, and otherwise
// the repository the runner fetched the action from. With neither, the step
// stops before the engine: an empty subject switches the pin check off.
func TestCheckL3IsolationSinglePinSubject(t *testing.T) {
	t.Parallel()
	action := loadAction(t, "check-l3-isolation")
	script := action.Runs.Steps[0].Run

	if value := fmt.Sprint(action.Inputs["single-pin-subject"].Default); value != "" {
		t.Fatalf("single-pin-subject defaults to %q, want the empty string that follows the serving repository", value)
	}

	for _, test := range []struct {
		name, input, served, want string
	}{
		{"follows the serving repository", "", "fork/layer", "fork/layer"},
		{"prefers the named subject", "named/subject", "fork/layer", "named/subject"},
		{"takes a named subject for a local-path action", "named/subject", "", "named/subject"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			box := newSandbox(t, "check-l3-isolation")
			got := box.run(script, "ISOLATION_SIGNING_SECRETS=",
				"ISOLATION_SINGLE_PIN_SUBJECT="+test.input, "GITHUB_ACTION_REPOSITORY="+test.served)

			argv := box.argv()
			if at := slices.Index(argv, "--single-pin-subject"); got.status != 0 || at < 0 || argv[at+1] != test.want {
				t.Fatalf("status %d argv %q, want subject %q; stderr %s", got.status, argv, test.want, got.stderr)
			}

			if checked := slices.Contains(argv, "--contract-root"); checked != (test.want == test.served) {
				t.Errorf("input declarations must come from the subject's own serving checkout: %q", argv)
			}
		})
	}

	t.Run("refuses to run without a subject", func(t *testing.T) {
		t.Parallel()

		box := newSandbox(t, "check-l3-isolation")

		got := box.run(script, "ISOLATION_SIGNING_SECRETS=")
		if got.status == 0 || box.argv() != nil || !strings.Contains(got.stderr, "single-pin-subject is empty") {
			t.Fatalf("status %d argv %q stderr %q", got.status, box.argv(), got.stderr)
		}
	})
}
