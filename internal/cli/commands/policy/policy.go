// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package policy wires admin policy inspection and owned check gates.
package policy

import (
	"context"
	"os"

	"github.com/diggsweden/forge-ci/internal/adapters/policycommand"
	appenforcement "github.com/diggsweden/forge-ci/internal/app/enforcement"
	"github.com/diggsweden/forge-ci/internal/domain/enforcement"
	"github.com/urfave/cli/v3"
)

// New returns the admin policy command group.
func New() *cli.Command {
	return &cli.Command{Name: "policy", Usage: "administrator check policy: inspect it and run gates under it", Commands: []*cli.Command{
		{Name: "show", Usage: "print the effective admin settings, the source of each (embedded-default or admin-input) and the embedded default's SHA-256", Action: func(ctx context.Context, _ *cli.Command) error { return appenforcement.Show(ctx, os.Stdout) }},
		{Name: "nanolinter", Usage: "run full Nanolinter SARIF and independently gate lint and vulnerability findings", ArgsUsage: "-- <nanolinter verify --report sarif --out - command>", Flags: []cli.Flag{
			&cli.StringFlag{Name: "sarif-file", Usage: "write the invocation's security-only SARIF for provider upload"},
		}, Action: func(ctx context.Context, command *cli.Command) error {
			return appenforcement.Nanolinter(ctx, policycommand.Runner{}, os.Stdout, os.Stderr, command.Args().Slice(), command.String("sarif-file"))
		}},
		{Name: "tests", Usage: "run application tests and apply admin enforcement to recognized assertion failures", ArgsUsage: "-- <native test command>", Flags: []cli.Flag{
			&cli.StringFlag{Name: "protocol", Required: true, Usage: "native report protocol: go-json, maven, cargo, node, gradle, xctest"},
		}, Action: func(ctx context.Context, command *cli.Command) error {
			return appenforcement.Tests(ctx, policycommand.Runner{}, os.Stdout, os.Stderr, enforcement.TestProtocol(command.String("protocol")), command.Args().Slice())
		}},
	}}
}
