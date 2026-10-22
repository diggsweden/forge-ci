// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/app/validate"
)

// realArmorBlock is a real GPG armored public key block (ed25519,
// generated with gpg --batch --gen-key for testing; not a real
// credential). The test file is allowlisted in
// development/gitleaks/gitleaks.toml.
const realArmorBlock = `-----BEGIN PGP PUBLIC KEY BLOCK-----

mDMEatoqQhYJKwYBBAHaRw8BAQdA6J7V7z1+yuqChs73KDn8BIltMjNxBpRABQXC
3zs77T60IFRlc3QgT25seSA8dGVzdEBpbnZhbGlkLmV4YW1wbGU+iJAEExYKADgW
IQQe8FYTZb8zfMG+P3EKGLsvKLyF+AUCatoqQgIbIwULCQgHAgYVCgkICwIEFgID
AQIeAQIXgAAKCRAKGLsvKLyF+MEeAP986g5tE4yd2dHnGbuArWAEKt4xCuyrFJr5
NzCxpoGdRAEApxaVECJ9f4HppYsGU0o62pqjiYCF3aJoX8L81zmJGgI=
=513Q
-----END PGP PUBLIC KEY BLOCK-----
`

func TestValidateGPGKeysFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr int
	}{
		{
			name:    "valid single block",
			content: realArmorBlock,
			wantErr: 0,
		},
		{
			name:    "valid multiple blocks",
			content: realArmorBlock + "\n" + realArmorBlock,
			wantErr: 0,
		},
		{
			name:    "empty file",
			content: "",
			wantErr: 1,
		},
		{
			name:    "only comments no armor",
			content: "# just a comment\n",
			wantErr: 1,
		},
		{
			name:    "truncated — missing end marker",
			content: "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmDMEatoqQhYJKwYBBAHaRw8BAQdA\n",
			wantErr: 1,
		},
		{
			name:    "wrong block type",
			content: "-----BEGIN PGP SIGNATURE-----\n\ndGVzdA==\n=dGVzdA==\n-----END PGP SIGNATURE-----\n",
			wantErr: 1,
		},
		{
			name:    "corrupted base64 body",
			content: "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\n!!!not-base64!!!\n=dGVzdA==\n-----END PGP PUBLIC KEY BLOCK-----\n",
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
