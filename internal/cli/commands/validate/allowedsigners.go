// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	appvalidate "github.com/diggsweden/forge-ci/internal/app/validate"
)

// allowedSignersCmd wires `forge-ci validate allowed-signers`. It is a
// pre-flight format check on the OpenSSH allowed_signers file — catches
// malformed entries (missing principal, unknown key type, invalid base64)
// before they reach CI tag-verification, where the error is less obvious.
func allowedSignersCmd() *cli.Command {
	return &cli.Command{
		Name:  "allowed-signers",
		Usage: "validate the OpenSSH allowed_signers file format",
		Description: `Checks each non-comment line of .forge-ci/allowed_signers
against the OpenSSH allowed_signers format:

  principal [options] key-type base64-key

Exits non-zero with one error per malformed line if any entry is
malformed. Useful as a pre-commit hook or CI pre-flight gate.

EXAMPLE:
   forge-ci validate allowed-signers
   forge-ci validate allowed-signers --path .forge-ci/allowed_signers`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "path",
				Sources: cli.EnvVars("ALLOWED_SIGNERS_PATH"),
				Usage:   "path to the allowed_signers file (default: .forge-ci/allowed_signers)",
				Value:   ".forge-ci/allowed_signers",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			path := cmd.String("path")

			errs := appvalidate.ValidateAllowedSignersFormat(path)
			if len(errs) == 0 {
				_, _ = fmt.Fprintf(os.Stdout, "✓ %s is well-formed\n", path)

				return nil
			}

			for _, e := range errs {
				_, _ = fmt.Fprintf(os.Stderr, "✗ %s\n", e)
			}

			_, _ = fmt.Fprintf(os.Stderr, "\n%d format error(s) in %s — each line must be 'principal [options] key-type base64-key'\n", len(errs), path)
			_, _ = fmt.Fprintf(os.Stderr, "See: ssh-keygen(1) ALLOWED SIGNERS, docs/verification.md#release-authorisation\n")

			return fmt.Errorf("allowed_signers format validation failed: %s", strings.Join(errs, "; "))
		},
	}
}
