// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func TestIOSConsumerUsesReusableNativeBuildWithoutSimulator(t *testing.T) {
	t.Parallel()
	require.True(t, slices.ContainsFunc(GitHubCells, func(cell GitHubCell) bool {
		return cell.Demo == "hello-ios" && cell.Event == TrialDevRelease && cell.Set == SetMedium &&
			slices.Equal(cell.Settings, []string{"workflow=build-workflow.yml"})
	}), "the native build must remain repeatable through the normal hosted case table")
	const root = "test/live/fixtures/consumers/hello-ios/consumer/test/live/github/workflows/"
	type job struct {
		Uses    string
		With    map[string]any
		Secrets any
		Steps   []any
	}
	var build struct {
		On          map[string]any
		Permissions map[string]string
		Jobs        map[string]job
	}
	body := reporoot.ReadFile(t, root+"build-workflow.yml")
	require.NoError(t, yaml.Unmarshal(body, &build))
	require.Contains(t, build.On, "workflow_dispatch")
	require.Len(t, build.On, 1, "branch dispatch uses the common development-build trial without duplicate push runs")
	require.NotContains(t, build.On, "pull_request")
	require.Equal(t, map[string]string{"contents": "read"}, build.Permissions)
	require.Len(t, build.Jobs, 1)
	archive := build.Jobs["build"]
	workflow, ref, pinned := strings.Cut(archive.Uses, "@")
	require.True(t, pinned)
	require.Equal(t, "diggsweden/forge-ci/.github/workflows/build-xcode-ios.yml", workflow)
	require.NotEmpty(t, ref)
	require.Equal(t, ref, archive.With["forge-ci-binary-ref"])
	require.Equal(t, true, archive.With["use-xcodegen"])
	require.Equal(t, "generic/platform=iOS", archive.With["destination"])
	require.Equal(t, false, archive.With["enable-code-signing"])
	require.Empty(t, archive.Steps, "consumer delegates the build to reusable CI")
	require.Nil(t, archive.Secrets, "unsigned build needs no signing secrets")
	var pr struct{ Jobs map[string]job }
	require.NoError(t, yaml.Unmarshal(reporoot.ReadFile(t, root+"pullrequest-workflow.yml"), &pr))
	require.Len(t, pr.Jobs, 1)
	require.Contains(t, pr.Jobs["pr-checks"].Uses, "/pullrequest-orchestrator.yml@")
	for _, name := range []string{"pullrequest-workflow.yml", "build-workflow.yml", "release-workflow.yml"} {
		workflow := strings.ToLower(string(reporoot.ReadFile(t, root+name)))
		for _, forbidden := range []string{"simctl", "downloadplatform", "iphonesimulator", "xcodebuild test", "ios simulator"} {
			require.NotContains(t, workflow, forbidden, name)
		}
	}
}
