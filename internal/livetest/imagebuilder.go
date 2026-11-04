// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import "os"

// GitLabImageBuilderEnv selects the image builder of the GitLab scenarios that
// build images: buildah, the default, on the contract's privileged runner, or
// kaniko, on the ordinary runner in a kaniko job image. On GitLab the builder
// is the release policy's input, so it is the trial's setting and no consumer
// variable; Forgejo trials select theirs with the consumer variables
// CI_IMAGE_BUILDER and CI_IMAGE_BUILDER_JOB_IMAGE.
const GitLabImageBuilderEnv = "FORGE_CI_LIVE_GITLAB_IMAGE_BUILDER"

// The image builders a GitLab scenario can select.
const (
	ImageBuilderBuildah = "buildah"
	ImageBuilderKaniko  = "kaniko"
)

// GitLabKaniko reports whether GitLabImageBuilderEnv selects kaniko. Any value
// but buildah, kaniko or none fails the test.
func GitLabKaniko(tb TB) bool {
	tb.Helper()

	switch value := os.Getenv(GitLabImageBuilderEnv); value {
	case "", ImageBuilderBuildah:
		return false
	case ImageBuilderKaniko:
		return true
	default:
		tb.Fatalf("livetest: %s must be %s or %s, got %q", GitLabImageBuilderEnv, ImageBuilderBuildah, ImageBuilderKaniko, value)

		return false
	}
}
