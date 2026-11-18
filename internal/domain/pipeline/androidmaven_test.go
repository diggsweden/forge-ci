// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package pipeline_test

import (
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/config"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/domain/projecttype"
	"github.com/stretchr/testify/require"
)

func TestReleaseAndroidMavenArtifacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                string
		library, publish, handoff, accepted bool
	}{
		{"library publication", true, true, true, true},
		{"file-only library", true, false, false, true},
		{"application", false, true, true, false},
		{"missing handoff", true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := pipeline.PlannedArtifact{Name: "library", ProjectType: projecttype.GradleAndroid, BuildType: config.BuildTypeApplication}
			if tc.library {
				a.BuildType = config.BuildTypeLibrary
			}
			if tc.publish {
				a.PublishTo = []config.PublishTarget{config.PublishForgePackages}
			}
			if tc.handoff {
				a.PublicationArtifactName = "library-maven-publications"
			}
			build := pipeline.ReleaseBuildStagePlan{Targets: pipeline.ReleaseBuildTargets{GradleAndroid: pipeline.TargetPlan[pipeline.PlannedArtifact]{Runs: true, Items: []pipeline.PlannedArtifact{a}}}}
			got, err := pipeline.ReleaseAndroidMavenArtifacts(build)
			if !tc.accepted {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.publish {
				require.Len(t, got, 1)
			} else {
				require.Empty(t, got)
			}
		})
	}
}
