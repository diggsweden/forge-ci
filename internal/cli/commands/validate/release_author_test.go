// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/cli/commands/validate"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestReleaseAuthorPreflight(t *testing.T) {
	pin := "SHA256:" + strings.Repeat("A", 43)

	for _, tc := range []struct {
		label, name, email, pin string
		sshPush                 bool
		fields                  []string
	}{
		{"no SSH push", "Release Bot", "bot@example.org", "", false, nil},
		{"SSH push", "Release Bot", "bot@example.org", pin, true, nil},
		{"padded pin", "Release Bot", "bot@example.org", pin + "=", true, nil},
		{"missing values", "", "", "", true, []string{"COMMIT_AUTHOR_NAME", "COMMIT_AUTHOR_EMAIL", "RELEASE_GIT_HOST_KEY_FINGERPRINT"}},
		{"invalid values", "private\nname", "Private Label <bot@example.org>", "private-invalid-pin", true, []string{"COMMIT_AUTHOR_NAME", "COMMIT_AUTHOR_EMAIL", "RELEASE_GIT_HOST_KEY_FINGERPRINT"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			t.Setenv("COMMIT_AUTHOR_NAME", tc.name)
			t.Setenv("COMMIT_AUTHOR_EMAIL", tc.email)
			t.Setenv("RELEASE_GIT_HOST_KEY_FINGERPRINT", tc.pin)

			args := []string{"validate", "release-author"}
			if tc.sshPush {
				args = append(args, "--require-ssh-host-pin")
			}

			err := validate.New().Run(t.Context(), args)
			if len(tc.fields) == 0 {
				if err != nil {
					t.Fatal(err)
				}

				return
			}

			if !errors.Is(err, errs.ErrValidation) {
				t.Fatalf("expected a blocking validation error, got %v", err)
			}

			for _, field := range tc.fields {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("error should name %s", field)
				}
			}

			for _, value := range []string{tc.name, tc.email, tc.pin} {
				if value != "" && strings.Contains(err.Error(), value) {
					t.Fatal("validation error disclosed an input")
				}
			}
		})
	}
}
