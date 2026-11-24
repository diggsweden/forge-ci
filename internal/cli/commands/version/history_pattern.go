// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package version

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/diggsweden/forge-ci/internal/cli/commonflags"
	domainversion "github.com/diggsweden/forge-ci/internal/domain/version"
)

// historyPatternCmd wires `forge-ci version history-pattern`: print the tag
// regex that bounds changelog history under the release policy, so a workflow
// that runs git-cliff itself keeps no copy of the rule.
func historyPatternCmd() *cli.Command {
	return &cli.Command{
		Name:  "history-pattern",
		Usage: "print the tag regex that bounds changelog history under the release policy",
		Description: `EXAMPLE:
   forge-ci version history-pattern --release-policy fork:example`,
		Flags: []cli.Flag{commonflags.ReleasePolicy()},
		Action: func(_ context.Context, cmd *cli.Command) error {
			policy := cmd.String("release-policy")
			if err := domainversion.ValidateReleasePolicy(policy); err != nil {
				return err
			}

			_, err := fmt.Fprintln(cmd.Root().Writer, domainversion.ReleaseHistoryPattern(policy))

			return err
		},
	}
}
