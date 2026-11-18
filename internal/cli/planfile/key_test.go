// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package planfile

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestKey_UsesRegisteredSourceNotFlagName(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag cli.Flag
		want string
	}{
		{"nil", nil, ""},
		{"typed nil", (*cli.StringFlag)(nil), ""},
		{"no sources", &cli.StringFlag{Name: "name"}, ""},
		{"environment only", &cli.StringFlag{Name: "name", Sources: cli.EnvVars("NAME")}, ""},
		{"wrong scope", &cli.StringFlag{Name: "name", Sources: Vars("other", "key")}, ""},
		{"string", &cli.StringFlag{Name: "name", Sources: Vars("scope", "key")}, "key"},
		{"bool", &cli.BoolFlag{Name: "name", Sources: Vars("scope", "enabled")}, "enabled"},
		{"integer", &cli.IntFlag{Name: "name", Sources: Vars("scope", "count")}, "count"},
		{"slice", &cli.StringSliceFlag{Name: "name", Sources: Vars("scope", "items")}, "items"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := Key(tc.flag, "scope")
			require.Equal(t, tc.want, key)
			require.Equal(t, tc.want != "", ok)
		})
	}
}
