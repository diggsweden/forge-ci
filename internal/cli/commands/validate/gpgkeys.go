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

// gpgKeysCmd wires `forge-ci validate gpg-keys`. Structural format check
// on the armored GPG public-key bundle — catches truncated or
// marker-less files before they reach CI tag-verification.
func gpgKeysCmd() *cli.Command {
	return &cli.Command{
		Name:  "gpg-keys",
		Usage: "validate the armored GPG public-key bundle format",
		Description: `Checks .forge-ci/allowed_gpg_keys.asc for well-formed
RFC 4880 ASCII armor: at least one PGP PUBLIC KEY BLOCK with
matching BEGIN/END markers and non-empty body.

Exits non-zero with errors if the file is malformed. Useful as
a pre-commit hook or CI pre-flight gate.

EXAMPLE:
   forge-ci validate gpg-keys
   forge-ci validate gpg-keys --path .forge-ci/allowed_gpg_keys.asc`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "path",
				Sources: cli.EnvVars("ALLOWED_GPG_KEYS_PATH"),
				Usage:   "path to the armored GPG public-key bundle (default: .forge-ci/allowed_gpg_keys.asc)",
				Value:   ".forge-ci/allowed_gpg_keys.asc",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			path := cmd.String("path")

			errs := appvalidate.ValidateGPGKeysFormat(path)
			if len(errs) == 0 {
				_, _ = fmt.Fprintf(os.Stdout, "✓ %s is well-formed\n", path)

				return nil
			}

			for _, e := range errs {
				_, _ = fmt.Fprintf(os.Stderr, "✗ %s\n", e)
			}

			_, _ = fmt.Fprintf(os.Stderr, "\n%d format error(s) in %s — expected RFC 4880 ASCII armor with PGP PUBLIC KEY BLOCK markers\n", len(errs), path)
			_, _ = fmt.Fprintf(os.Stderr, "See: docs/verification.md#release-authorisation\n")

			return fmt.Errorf("gpg-keys format validation failed: %s", strings.Join(errs, "; "))
		},
	}
}
