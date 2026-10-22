// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/app/validate"
)

func TestValidateGPGKeysFormat(t *testing.T) {
	t.Parallel()

	// Minimal PGP armor block with low-entropy base64 body.
	const validBlock = `-----BEGIN PGP PUBLIC KEY BLOCK-----

dGVzdA==
=dGVzdA==
-----END PGP PUBLIC KEY BLOCK-----
`

	tests := []struct {
		name    string
		content string
		wantErr int
	}{
		{
			name:    "valid single block",
			content: validBlock,
			wantErr: 0,
		},
		{
			name: "valid multiple blocks",
			content: validBlock + "\n" + validBlock,
			wantErr: 0,
		},
		{
			name:    "missing begin marker",
			content: "dGVzdA==\n=dGVzdA==\n-----END PGP PUBLIC KEY BLOCK-----\n",
			wantErr: 1,
		},
		{
			name:    "missing end marker",
			content: "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\ndGVzdA==\n=dGVzdA==\n",
			wantErr: 1,
		},
		{
			name:    "empty body",
			content: "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\n-----END PGP PUBLIC KEY BLOCK-----\n",
			wantErr: 1,
		},
		{
			name:    "empty file",
			content: "",
			wantErr: 1,
		},
		{
			name:    "second block truncated",
			content: validBlock + "\n-----BEGIN PGP PUBLIC KEY BLOCK-----\n\ndGVzdA==\n",
			wantErr: 1,
		},
		{
			name:    "only comments no armor",
			content: "# just a comment\n",
			wantErr: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "allowed_gpg_keys.asc")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			errs := validate.ValidateGPGKeysFormat(path)
			if len(errs) != tt.wantErr {
				t.Errorf("got %d errors, want %d: %v", len(errs), tt.wantErr, errs)
			}
		})
	}
}

func TestValidateGPGKeysFormat_FileNotFound(t *testing.T) {
	t.Parallel()

	errs := validate.ValidateGPGKeysFormat(filepath.Join(t.TempDir(), "nonexistent"))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}

	if !strings.Contains(errs[0], "cannot read") {
		t.Errorf("error should mention 'cannot read', got: %s", errs[0])
	}
}
