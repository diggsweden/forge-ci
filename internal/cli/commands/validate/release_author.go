// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/urfave/cli/v3"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/git"
)

func releaseAuthorCmd() *cli.Command {
	return &cli.Command{
		Name:  "release-author",
		Usage: "validate the release commit identity and, when required, the SSH host key pin without printing values",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "author-name", Sources: cli.EnvVars("COMMIT_AUTHOR_NAME"), Usage: "release commit author name"},
			&cli.StringFlag{Name: "author-email", Sources: cli.EnvVars("COMMIT_AUTHOR_EMAIL"), Usage: "bare release commit author email address"},
			&cli.BoolFlag{Name: "require-ssh-host-pin", Usage: "also validate RELEASE_GIT_HOST_KEY_FINGERPRINT for an SSH push"},
			&cli.StringFlag{Name: "host-key-fingerprint", Sources: cli.EnvVars("RELEASE_GIT_HOST_KEY_FINGERPRINT"), Usage: "pinned SSH host key fingerprint"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			var problems []error
			name := cmd.String("author-name")
			if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || strings.ContainsAny(name, "<>") || strings.ContainsFunc(name, func(r rune) bool {
				return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
			}) {
				problems = append(problems, errors.New("COMMIT_AUTHOR_NAME (bot-name) must be a nonblank, single-line Git author name without angle brackets"))
			}
			email := cmd.String("author-email")
			address, err := mail.ParseAddress(email)
			if err != nil || address.Address != email || address.Name != "" || strings.ContainsFunc(email, unicode.IsSpace) {
				problems = append(problems, errors.New("COMMIT_AUTHOR_EMAIL (bot-email) must be a bare email address"))
			}
			if cmd.Bool("require-ssh-host-pin") {
				if _, err := git.ParseHostKeyPin(cmd.String("host-key-fingerprint")); err != nil {
					// The parser error may contain the input. Report only the field and the expected format.
					problems = append(problems, errors.New("RELEASE_GIT_HOST_KEY_FINGERPRINT must be a SHA256:<base64> fingerprint without a hostname or comment"))
				}
			}
			if err := errors.Join(problems...); err != nil {
				return fmt.Errorf("release author: %v: %w", err, errs.ErrValidation)
			}

			return nil
		},
	}
}
