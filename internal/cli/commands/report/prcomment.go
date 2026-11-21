// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package report

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	appsummary "github.com/diggsweden/forge-ci/internal/app/summary"
	"github.com/diggsweden/forge-ci/internal/cli/cienv"
	"github.com/diggsweden/forge-ci/internal/cli/deps"
	"github.com/diggsweden/forge-ci/internal/cli/secret"
	"github.com/diggsweden/forge-ci/internal/cliio"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

const (
	prCommentTokenEnv = "PR_COMMENT_TOKEN"
	repositoryFlag    = "repository"
)

func prCommentCmd() *cli.Command {
	return &cli.Command{
		Name:  "pr-comment",
		Usage: "post the PR check results as the request's one comment, replacing the previous one",
		Description: `For GitLab and Forgejo. Run it in a job that checks out and runs no
project code, with a token only that job receives.

EXAMPLE:
   # Token from $PR_COMMENT_TOKEN (or --token-file -)
   forge-ci report pr-comment --repository group/app --pr-number 12 --file forge-ci-pr-check-results.md`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "token-file",
				Usage: "path to a file containing the comment token (use \"-\" for stdin; defaults to $" + prCommentTokenEnv + ")",
			},
			&cli.StringFlag{
				Name:     repositoryFlag,
				Required: true,
				Sources:  cienv.Repository(),
				Usage:    "\"group/project[/sub]\" receiving the request",
			},
			&cli.IntFlag{Name: "pr-number", Required: true, Sources: cli.EnvVars("PR_NUMBER"), Usage: "pull or merge request number"},
			&cli.StringFlag{Name: "file", Required: true, Usage: "Markdown file that forge-ci report pr wrote"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			token, err := secret.Resolve(cmd.String("token-file"), prCommentTokenEnv)
			if err != nil {
				return err
			}

			if token == "" {
				return errs.CredentialRequired(errs.Credential{What: "pull request comment token", Flag: "token-file", Env: prCommentTokenEnv})
			}

			body, err := cliio.ReadFile(cmd.String("file"))
			if err != nil {
				return fmt.Errorf("read the PR check results: %w", err)
			}

			return deps.FromCmd(ctx, cmd, func(d *deps.Deps) error {
				commenter, err := d.RequirePRCommenter()
				if err != nil {
					return err
				}

				return appsummary.PRComment(ctx, commenter, os.Stdout, appsummary.PRCommentInput{
					Repository: cmd.String(repositoryFlag),
					Number:     cmd.Int("pr-number"),
					Body:       string(body),
					Token:      token,
				})
			})
		},
	}
}
