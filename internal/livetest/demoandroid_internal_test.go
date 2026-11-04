// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func TestAndroidDemoBindsBothJobsToPreparedEngine(t *testing.T) {
	t.Parallel()

	body := reporoot.ReadFile(t, "test/live/demos/hello-android/consumer/test/live/github/workflows/pullrequest.yml")
	rendered, _, err := RenderGitHubConsumer("pullrequest.yml", string(body), trialEngine, nil)
	require.NoError(t, err)
	var workflow struct {
		Jobs map[string]struct {
			Uses string
			If   string
			With map[string]string
		}
	}
	require.NoError(t, yaml.Unmarshal([]byte(rendered), &workflow))
	for job, leaf := range map[string]string{"pr-checks": "pullrequest-orchestrator.yml", "build": "build-gradle-android.yml"} {
		t.Run(job, func(t *testing.T) {
			call, ok := workflow.Jobs[job]
			require.True(t, ok)
			require.Equal(t, trialEngine.Owner+"/"+trialEngine.Repository+"/.github/workflows/"+leaf+"@"+trialEngine.Commit, call.Uses)
			require.Equal(t, GitHubPrerelease, call.With["forge-ci-binary-ref"], "missing binary ref would resolve latest stable instead of the prepared engine")
		})
	}
	build := workflow.Jobs["build"]
	require.Equal(t, "${{ github.event_name == 'workflow_dispatch' }}", build.If)
	require.Equal(t, "ghcr.io/example-org/forge-ci-runtime-android-35:sha-aaaaaaa", build.With["runtime-image"])
	require.Equal(t, "false", build.With["enable-signing"])
	require.Equal(t, "false", build.With["skip-tests"])
	require.Equal(t, "true", build.With["include-aab"])
}
