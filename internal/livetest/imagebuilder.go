// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"os"

	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

// GitLabImageBuilderEnv selects the image builder of the GitLab scenarios that
// build images: buildah, the default, on the contract's privileged runner, or
// kaniko, on the ordinary runner in a kaniko job image. On GitLab the builder
// is the release policy's input, so it is the trial's setting and no consumer
// variable; Forgejo trials select theirs with the consumer variables
// CI_IMAGE_BUILDER and CI_IMAGE_BUILDER_JOB_IMAGE.
const GitLabImageBuilderEnv = "FORGE_CI_LIVE_GITLAB_IMAGE_BUILDER"

// ForgejoImageBuilderEnv selects, as GitLabImageBuilderEnv does on GitLab,
// the image builder of the Forgejo scenarios that build images and take no
// consumer settings, such as the snapshot scenarios. A Forgejo consumer trial
// selects its builder with CI_IMAGE_BUILDER, and refuses this setting when the
// two differ.
const ForgejoImageBuilderEnv = "FORGE_CI_LIVE_FORGEJO_IMAGE_BUILDER"

// KanikoJobImageArchiveEnv names the saved kaniko job image a scenario pushes
// and labels with the layer it runs from when the run selects kaniko.
const KanikoJobImageArchiveEnv = "FORGE_CI_LIVE_KANIKO_JOB_IMAGE_ARCHIVE"

// The image builders a scenario can select.
const (
	ImageBuilderBuildah = "buildah"
	ImageBuilderKaniko  = "kaniko"
)

// GitLabKaniko reports whether GitLabImageBuilderEnv selects kaniko. Any value
// but buildah, kaniko or none fails the test.
func GitLabKaniko(tb TB) bool {
	tb.Helper()

	return SelectedImageBuilder(tb, provider.ForgeGitLab) == ImageBuilderKaniko
}

// SelectedImageBuilder is the image builder the run selects for the forge's
// scenarios: buildah unless its setting says kaniko. Any other value fails
// the test.
func SelectedImageBuilder(tb TB, forge provider.ForgeAPI) string {
	tb.Helper()

	env := ImageBuilderEnv(forge)
	if env == "" {
		tb.Fatalf("livetest: no image builder is selectable on %s", forge)

		return ""
	}

	switch value := os.Getenv(env); value {
	case "", ImageBuilderBuildah:
		return ImageBuilderBuildah
	case ImageBuilderKaniko:
		return ImageBuilderKaniko
	default:
		tb.Fatalf("livetest: %s must be %s or %s, got %q", env, ImageBuilderBuildah, ImageBuilderKaniko, value)

		return ""
	}
}

// ImageBuilderEnv is the setting that selects the forge's image builder, or ""
// for a forge whose scenarios select none.
func ImageBuilderEnv(forge provider.ForgeAPI) string {
	switch forge {
	case provider.ForgeGitLab:
		return GitLabImageBuilderEnv
	case provider.ForgeForgejo:
		return ForgejoImageBuilderEnv
	case provider.ForgeGitHub, provider.ForgeLocal:
	}

	return ""
}

// ImageBuilderChoice is where and with what a scenario's image builds run:
// Buildah on the contract's runner that permits user namespaces, or kaniko on
// the ordinary runner, in a kaniko job image.
type ImageBuilderChoice struct {
	// Builder is ImageBuilderBuildah or ImageBuilderKaniko.
	Builder string
	// RunnerLabels are the labels (GitLab tags) the image builds run on:
	// the privileged label for Buildah; for kaniko the ordinary runner's on
	// Forgejo and none on GitLab, whose untagged runner is the ordinary one.
	RunnerLabels []string

	kanikoArchive string
}

// ChooseImageBuilder resolves the run's builder selection for a scenario that
// builds images, and reports the scenario not covered when the lab cannot run
// it: Buildah without the contract's privileged runner, or kaniko without a
// saved kaniko job image. Call it before creating any resource.
func ChooseImageBuilder(tb TB, target Target) ImageBuilderChoice {
	tb.Helper()

	env := ImageBuilderEnv(target.Forge)

	if SelectedImageBuilder(tb, target.Forge) == ImageBuilderKaniko {
		archive := os.Getenv(KanikoJobImageArchiveEnv)
		if archive == "" {
			NotCoveredf(tb, "the run selects kaniko (%s=kaniko), whose image builds need %s, a saved kaniko job image", env, KanikoJobImageArchiveEnv)
		}

		labels := []string{}
		if target.Forge == provider.ForgeForgejo {
			labels = []string{RunnerLabel(tb, target)}
		}

		return ImageBuilderChoice{Builder: ImageBuilderKaniko, RunnerLabels: labels, kanikoArchive: archive}
	}

	label := PrivilegedRunnerLabel(tb, target)
	if label == "" {
		NotCoveredf(tb, "the run selects Buildah (%s unset or buildah), whose image builds need the contract's runner.privileged_label, a runner that permits user namespaces, and the lab names none; select kaniko with %s=kaniko", env, env)
	}

	return ImageBuilderChoice{Builder: ImageBuilderBuildah, RunnerLabels: []string{label}}
}

// JobImage is the builder's job image for the image builds of a layer served
// from layer: for kaniko, the saved kaniko job image pushed to repo and
// labelled with that layer, which admits it; for Buildah, none, since Buildah
// builds in the job's own image.
func (c ImageBuilderChoice) JobImage(tb TB, target Target, repo string, layer Layer) string {
	tb.Helper()

	if c.Builder != ImageBuilderKaniko {
		return ""
	}

	return PushKanikoJobImage(tb, target, repo, c.kanikoArchive, layer)
}
