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

// The release-author problems name the field and the expected form, never
// the value: a value may be private.
var (
	errReleaseAuthorName  = errors.New("COMMIT_AUTHOR_NAME (bot-name) must be a nonblank, single-line Git author name without angle brackets")
	errReleaseAuthorEmail = errors.New("COMMIT_AUTHOR_EMAIL (bot-email) must be a bare email address")
	errReleaseHostKeyPin  = errors.New("RELEASE_GIT_HOST_KEY_FINGERPRINT must be a SHA256:<base64> fingerprint without a hostname or comment")
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
			err := errors.Join(
				releaseAuthorNameProblem(cmd.String("author-name")),
				releaseAuthorEmailProblem(cmd.String("author-email")),
				releaseHostKeyPinProblem(cmd.Bool("require-ssh-host-pin"), cmd.String("host-key-fingerprint")),
			)
			if err != nil {
				return fmt.Errorf("release author: %w: %w", err, errs.ErrValidation)
			}

			return nil
		},
	}
}

func releaseAuthorNameProblem(name string) error {
	singleLine := !strings.ContainsFunc(name, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
	})

	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || strings.ContainsAny(name, "<>") || !singleLine {
		return errReleaseAuthorName
	}

	return nil
}

func releaseAuthorEmailProblem(email string) error {
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || address.Name != "" || strings.ContainsFunc(email, unicode.IsSpace) {
		return errReleaseAuthorEmail
	}

	return nil
}

// releaseHostKeyPinProblem reports a malformed pin when an SSH push needs
// one. The parser error may contain the input, so it is not passed on.
func releaseHostKeyPinProblem(required bool, pin string) error {
	if !required {
		return nil
	}

	if _, err := git.ParseHostKeyPin(pin); err != nil {
		return errReleaseHostKeyPin
	}

	return nil
}
