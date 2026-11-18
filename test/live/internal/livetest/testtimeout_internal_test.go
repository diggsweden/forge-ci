// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveTestTimeout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, event, settings, override, want string
	}{
		{"ordinary", "", "", "", "3h0m0s"},
		{"single release", TrialRelease, "version=0.2.0", "", "3h0m0s"},
		{"two releases", TrialRelease, "version=0.2.0\nnext-version=0.2.1", "", "8h0m0s"},
		{"GitLab single release", TrialRelease, "version=0.2.0\njob-user=65532:65532", "", "3h0m0s"},
		{"GitLab two releases", TrialRelease, "version=0.2.0\nnext-version=0.2.1\njob-user=65532:65532", "", "8h0m0s"},
		{"maximum", "", "", "12h", "12h0m0s"},
		{"above maximum", "", "", "12h1ns", ""},
		{"bounded extension", TrialRelease, "version=0.2.0\nnext-version=0.2.1", "10h", "10h0m0s"},
		{"disabled", "", "", "0", ""},
		{"negative", "", "", "-1h", ""},
		{"too short", "", "", "2h", ""},
		{"two releases too short", TrialRelease, "version=0.2.0\nnext-version=0.2.1", "3h", ""},
		{"too long", "", "", "13h", ""},
		{"malformed", "", "", "forever", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{"FORGE_CI_LIVE_CONSUMER_EVENT": tc.event, "FORGE_CI_LIVE_CONSUMER_SETTINGS": tc.settings, "FORGE_CI_LIVE_TEST_TIMEOUT": tc.override}
			got, err := LiveTestTimeout(func(key string) string { return env[key] })
			if tc.want == "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
