// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package plan_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/diggsweden/forge-ci/internal/cli"
	"github.com/diggsweden/forge-ci/internal/cli/planfile"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/isolatedenv"
	"github.com/stretchr/testify/require"
	urfavecli "github.com/urfave/cli/v3"
)

func TestPlanWrite_RejectsActualUnwiredFlagsBeforeWriting(t *testing.T) {
	for _, tc := range []struct{ scope, key string }{
		{"config expand-sboms", "value"},
		{"config expand-sboms", "as"},
		{"config expand-sboms", "exclude"},
		{"container build", "image-builder"},
	} {
		t.Run(tc.scope+"/"+tc.key, func(t *testing.T) {
			for _, existing := range []bool{false, true} {
				path := filepath.Join(t.TempDir(), "plan.json")

				const original = `{"container build":{"context":"untouched"}}`
				if existing {
					require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
				}

				args := []string{"--new", "--plan-file", path, "--scope", tc.scope}
				if tc.scope == "container build" {
					args = append(args, "--set", "context=would-change")
				}

				err := runPlanWrite(t, append(args, "--set", tc.key+"=all")...)
				require.ErrorIs(t, err, errs.ErrUsage)
				require.ErrorContains(t, err, "has no plan source")

				body, err := os.ReadFile(path)
				if existing {
					require.NoError(t, err)
					require.Equal(t, []byte(original), body) //nolint:testifylint // Refusal must preserve bytes, not just equivalent JSON.
				} else {
					require.True(t, os.IsNotExist(err))
				}
			}
		})
	}
}

// Intercept only the leaf action, retaining real registrations and root checks.
func TestPlanWrite_ActualRegistrationsPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, env, plan, flag, want string
		wantCache                   bool
	}{
		{name: "default", want: "."},
		{name: "env beats default", env: "from-env", want: "from-env", wantCache: true},
		{name: "plan beats env", env: "from-env", plan: "from-plan", want: "from-plan"},
		{name: "flag beats plan", env: "from-env", plan: "from-plan", flag: "from-flag", want: "from-flag", wantCache: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			t.Setenv(planfile.EnvVar, path)
			t.Setenv("BUILD_CONTEXT", tc.env)
			t.Setenv("BUILD_CACHE_PUSH", "false")

			if tc.env != "" {
				t.Setenv("BUILD_CACHE_PUSH", "true")
			} else {
				isolatedenv.Unsetenv(t, "BUILD_CONTEXT")
			}

			if tc.plan != "" {
				require.NoError(t, runPlanWrite(t, "--scope", "container build",
					"--set", "context="+tc.plan, "--set", "cache-push=false"))
			}

			root := cli.New(cli.BuildInfo{Version: "dev"})

			var target *urfavecli.Command

			for _, group := range root.Commands {
				if group.Name == "container" {
					for _, sub := range group.Commands {
						if sub.Name == "build" {
							target = sub
						}
					}
				}
			}

			require.NotNil(t, target)

			called := false
			target.Action = func(_ context.Context, cmd *urfavecli.Command) error {
				called = true

				require.Equal(t, tc.want, cmd.String("context"))
				require.Equal(t, tc.wantCache, cmd.Bool("cache-push"))

				return nil
			}

			args := []string{"forge-ci", "container", "build", "--mode", "local"}
			if tc.flag != "" {
				args = append(args, "--context", tc.flag, "--cache-push=true")
			}

			require.NoError(t, root.Run(context.Background(), args))
			require.True(t, called)
		})
	}
}
