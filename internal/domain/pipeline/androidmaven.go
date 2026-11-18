// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package pipeline

import (
	"fmt"
	"slices"

	"github.com/diggsweden/forge-ci/internal/domain/config"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// ReleaseAndroidMavenArtifacts selects the Android library repositories the
// policy release must publish. The directory is the existing publication
// artifact name, below dist; SDK producers export there without credentials.
func ReleaseAndroidMavenArtifacts(build ReleaseBuildStagePlan) ([]PlannedArtifact, error) {
	var artifacts []PlannedArtifact
	if !build.Targets.GradleAndroid.Runs {
		return nil, nil
	}
	for _, artifact := range build.Targets.GradleAndroid.Items {
		if !slices.Contains(artifact.PublishTo, config.PublishForgePackages) {
			continue
		}
		if artifact.BuildType != config.BuildTypeLibrary || artifact.PublicationArtifactName != artifact.Name+"-maven-publications" {
			return nil, fmt.Errorf("Android forge-packages artifact %q requires an explicit library and its planned Maven publication handoff: %w", artifact.Name, errs.ErrUnsupported)
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}
