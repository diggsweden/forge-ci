// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestRetentionDigestRequiresExactlyOnePackage(t *testing.T) {
	for _, selection := range [][]string{
		nil,
		{"--repository-prefix", "ghcr.io/acme/runtime"},
		{"--repository", "ghcr.io/acme/one", "--repository", "ghcr.io/acme/two"},
		{"--repository", "ghcr.io/acme/one"},
	} {
		command := packageRetentionCmd()
		command.Action = func(_ context.Context, cmd *cli.Command) error {
			_, err := retentionRuleFromCmd(cmd)

			return err
		}

		err := command.Run(t.Context(), append([]string{"package-retention", "--keep-digest", "sha256:" + strings.Repeat("a", 64)}, selection...))
		if (err == nil) != (len(selection) == 2 && selection[0] == "--repository") {
			t.Fatalf("digest/package binding: %v", err)
		}
	}
}
