// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"

	"github.com/urfave/cli/v3"

	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/cli/cienv"
	"github.com/diggsweden/forge-ci/internal/cli/deps"
)

// refGroup wires `forge-ci container ref resolve`, which composes a canonical
// image reference from CI repository context.
func refGroup() *cli.Command {
	return &cli.Command{
		Name:  "ref",
		Usage: "image-reference helpers: compose a canonical reference from repo context",
		Commands: []*cli.Command{
			refResolveCmd(),
		},
	}
}

func refResolveCmd() *cli.Command {
	return &cli.Command{
		Name:  "resolve",
		Usage: "compute the canonical image reference and emit name=<value>",
		Description: `EXAMPLES:
   # Canonical ghcr.io reference for a repo (emits name=ghcr.io/owner/app)
   forge-ci container ref resolve --registry ghcr.io --repository org/app --repository-owner org`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: flagRegistry, Required: true, Sources: cli.EnvVars("CONTAINER_REGISTRY"), Usage: "registry hostname (e.g. ghcr.io)"},
			&cli.StringFlag{Name: flagImageName, Sources: cli.EnvVars("IMAGE_NAME"), Usage: "explicit image name override (defaults to <owner>/<repo>)"},
			&cli.StringFlag{Name: flagRepository, Required: true, Sources: cienv.Repository(), Usage: "\"owner/repo\" used to build the default image name"},
			&cli.StringFlag{Name: "repository-owner", Required: true, Sources: cienv.RepositoryOwner(), Usage: "namespace for bare Docker Hub image overrides; resolved OCI repositories are fully qualified and lowercased"},
			&cli.StringFlag{Name: flagName, Sources: cli.EnvVars("CONTAINER_NAME"),
				Usage: "optional sub-name for multi-container projects"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if _, err := deps.ParseRepository(cmd, cmd.String(flagRepository)); err != nil {
				return err
			}

			return deps.FromCmd(ctx, cmd, func(d *deps.Deps) error {
				return appcontainer.ResolveName(ctx, d.OutputSink, appcontainer.ResolveNameInput{
					Registry:        cmd.String(flagRegistry),
					ImageName:       cmd.String(flagImageName),
					Repository:      cmd.String(flagRepository),
					RepositoryOwner: cmd.String("repository-owner"),
					Name:            cmd.String(flagName),
				})
			})
		},
	}
}
