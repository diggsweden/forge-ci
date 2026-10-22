// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// sshKeyTypes is the set of SSH public-key algorithm names accepted in
// an OpenSSH allowed_signers entry (see ssh-keygen(1), ALLOWED SIGNERS).
// The list matches `ssh -Q key` output — plain key types and their
// certificate variants.
var sshKeyTypes = map[string]bool{
	"ssh-ed25519":                        true,
	"ssh-ed25519-cert-v01@openssh.com":   true,
	"ssh-rsa":                            true,
	"ssh-rsa-cert-v01@openssh.com":       true,
	"ssh-dss":                            true,
	"ecdsa-sha2-nistp256":                true,
	"ecdsa-sha2-nistp256-cert-v01@openssh.com": true,
	"ecdsa-sha2-nistp384":                true,
	"ecdsa-sha2-nistp384-cert-v01@openssh.com": true,
	"ecdsa-sha2-nistp521":                true,
	"ecdsa-sha2-nistp521-cert-v01@openssh.com": true,
	"sk-ssh-ed25519@openssh.com":         true,
	"sk-ssh-ed25519-cert-v01@openssh.com": true,
	"sk-ecdsa-sha2-nistp256@openssh.com": true,
	"sk-ecdsa-sha2-nistp256-cert-v01@openssh.com": true,
}

// ValidateAllowedSignersFormat reads an OpenSSH allowed_signers file and
// returns one human-readable error per malformed line. An empty slice
// means every entry is well-formed.
//
// Each non-comment line must follow the ssh-keygen(1) ALLOWED SIGNERS
// format:
//
//	principals, [options,] key-type base64-encoded-key
//
// where principals is a comma-separated pattern-list (ssh_config(5)
// PATTERNS, supports * and ? wildcards), options is an optional
// comma-separated list of key=value pairs (cert-authority,
// namespaces=…, valid-after=…, valid-before=…), key-type is a
// recognised SSH key algorithm (matching `ssh -Q key`), and the final
// field is the base64-encoded public key. Empty lines and lines
// starting with '#' are ignored.
func ValidateAllowedSignersFormat(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("cannot read %s: %v", path, err)}
	}

	var errs []string

	for i, line := range strings.Split(string(data), "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 3 {
			errs = append(errs, fmt.Sprintf(
				"line %d: expected at least 3 fields (principals [options] key-type key), got %d — "+
					"the OpenSSH allowed_signers format is 'principal [options] key-type base64-key'",
				lineNo, len(fields)))

			continue
		}

		principals := fields[0]
		if principals == "" {
			errs = append(errs, fmt.Sprintf("line %d: principals field is empty", lineNo))
		}

		// Find the key-type field: scan from index 1 for the first
		// field that matches a known SSH key algorithm. Fields between
		// principals and key-type are options (key=value pairs).
		keyTypeIdx := -1

		for j := 1; j < len(fields); j++ {
			if sshKeyTypes[fields[j]] {
				keyTypeIdx = j

				break
			}
		}

		if keyTypeIdx == -1 {
			errs = append(errs, fmt.Sprintf(
				"line %d: no recognised key type found — expected a type from `ssh -Q key` "+
					"(e.g. ssh-ed25519, ssh-rsa, ecdsa-sha2-nistp256, or a -cert-v01@openssh.com variant)",
				lineNo))

			continue
		}

		// The field after key-type is the base64 key.
		if keyTypeIdx+1 >= len(fields) {
			errs = append(errs, fmt.Sprintf("line %d: missing base64 key after key type %q", lineNo, fields[keyTypeIdx]))

			continue
		}

		keyB64 := fields[keyTypeIdx+1]
		if _, err := base64.StdEncoding.DecodeString(keyB64); err != nil {
			errs = append(errs, fmt.Sprintf(
				"line %d: key field is not valid base64: %v", lineNo, err))
		}
	}

	return errs
}
