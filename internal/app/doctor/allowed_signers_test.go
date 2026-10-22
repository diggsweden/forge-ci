// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package doctor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/app/doctor"
)

// fakeKeyB64 is valid base64 but not a real SSH key — safe for tests
// and does not trip gitleaks.
const fakeKeyB64 = "dGVzdA=="

func TestValidateAllowedSignersFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr int // expected number of error strings
	}{
		{
			name:    "valid single entry",
			content: "alice@example.com ssh-ed25519 " + fakeKeyB64 + "\n",
			wantErr: 0,
		},
		{
			name:    "valid multiple entries with comments and blanks",
			content: "# comment\n\nalice@example.com ssh-ed25519 " + fakeKeyB64 + "\nbob@example.com ssh-rsa " + fakeKeyB64 + " bob's key\n",
			wantErr: 0,
		},
		{
			name:    "valid entry with options",
			content: `alice@example.com valid-before="20270101Z" ssh-ed25519 ` + fakeKeyB64 + "\n",
			wantErr: 0,
		},
		{
			name:    "missing principal — fields shifted right",
			content: "ssh-ed25519 " + fakeKeyB64 + " josef.andersson@digg.se\n",
			wantErr: 1, // no recognised key type found (base64 and email are not key types)
		},
		{
			name:    "too few fields",
			content: "alice@example.com ssh-ed25519\n",
			wantErr: 1,
		},
		{
			name:    "unknown key type",
			content: "alice@example.com foo-bar " + fakeKeyB64 + "\n",
			wantErr: 1,
		},
		{
			name:    "invalid base64 key",
			content: "alice@example.com ssh-ed25519 !!!not-base64!!!\n",
			wantErr: 1,
		},
		{
			name:    "multiple errors across lines",
			content: "ssh-ed25519 " + fakeKeyB64 + " alice@example.com\nalice@example.com foo-bar " + fakeKeyB64 + "\n",
			wantErr: 2, // line 1: no key type; line 2: no key type
		},
		{
			name:    "empty file",
			content: "",
			wantErr: 0,
		},
		{
			name:    "only comments",
			content: "# just a comment\n# another\n",
			wantErr: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "allowed_signers")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			errs := doctor.ValidateAllowedSignersFormat(path)
			if len(errs) != tt.wantErr {
				t.Errorf("got %d errors, want %d: %v", len(errs), tt.wantErr, errs)
			}
		})
	}
}

func TestValidateAllowedSignersFormat_MissingPrincipalMessage(t *testing.T) {
	t.Parallel()

	// The exact bug we just fixed: the line starts with the key-type,
	// not the principal. With no principal, the parser scans all
	// remaining fields looking for a key-type and finds none — the
	// base64 blob and the email are not recognised key types.
	path := filepath.Join(t.TempDir(), "allowed_signers")
	content := "ssh-ed25519 " + fakeKeyB64 + " josef.andersson@digg.se\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	errs := doctor.ValidateAllowedSignersFormat(path)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}

	if !strings.Contains(errs[0], "key type") {
		t.Errorf("error should mention 'key type' for shifted fields, got: %s", errs[0])
	}
}

func TestValidateAllowedSignersFormat_FileNotFound(t *testing.T) {
	t.Parallel()

	errs := doctor.ValidateAllowedSignersFormat(filepath.Join(t.TempDir(), "nonexistent"))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}

	if !strings.Contains(errs[0], "cannot read") {
		t.Errorf("error should mention 'cannot read', got: %s", errs[0])
	}
}
