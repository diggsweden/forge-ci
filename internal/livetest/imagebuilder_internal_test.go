// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"errors"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

// The trial labels a kaniko job image with the layer it runs from, and only an
// image titled as containers/kaniko-job titles it, or one built from it.
func TestLabelKanikoJobImageNamesTheLayerOfAKanikoJobImageOnly(t *testing.T) {
	t.Parallel()

	base, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}

	file, err := base.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}

	config := file.Config
	config.Labels = map[string]string{
		"org.opencontainers.image.title":  kanikoJobImageTitle,
		"org.opencontainers.image.source": "https://forgejo.invalid/forgeci/fci-consumer-trial-layer",
	}

	kaniko, err := mutate.Config(base, config)
	if err != nil {
		t.Fatal(err)
	}

	const source = "https://gitlab.invalid/forgeci/fci-release-engine"

	labelled, err := labelKanikoJobImage(kaniko, source)
	if err != nil {
		t.Fatal(err)
	}

	got, err := labelled.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}

	if got.Config.Labels["org.opencontainers.image.source"] != source || got.Config.Labels["org.opencontainers.image.title"] != kanikoJobImageTitle {
		t.Fatalf("labels = %v", got.Config.Labels)
	}

	if original, _ := kaniko.ConfigFile(); original.Config.Labels["org.opencontainers.image.source"] == source {
		t.Fatal("labelling changed the archive's own image")
	}

	if _, err := labelKanikoJobImage(base, source); !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("an image that is not a kaniko job image was labelled: %v", err)
	}
}

func TestGitLabKanikoReadsTheTrialsSetting(t *testing.T) {
	for value, want := range map[string]bool{"": false, "buildah": false, "kaniko": true} {
		t.Setenv(GitLabImageBuilderEnv, value)

		if got := GitLabKaniko(t); got != want {
			t.Errorf("%s=%q: GitLabKaniko() = %v, want %v", GitLabImageBuilderEnv, value, got, want)
		}
	}
}

func TestSelectedImageBuilderReadsEachForgesSetting(t *testing.T) {
	t.Setenv(GitLabImageBuilderEnv, ImageBuilderKaniko)
	t.Setenv(ForgejoImageBuilderEnv, "")

	if got := SelectedImageBuilder(t, provider.ForgeGitLab); got != ImageBuilderKaniko {
		t.Errorf("GitLab builder = %q, want kaniko", got)
	}

	if got := SelectedImageBuilder(t, provider.ForgeForgejo); got != ImageBuilderBuildah {
		t.Errorf("Forgejo builder = %q, want buildah: the GitLab setting must not select it", got)
	}

	t.Setenv(ForgejoImageBuilderEnv, ImageBuilderKaniko)

	if got := SelectedImageBuilder(t, provider.ForgeForgejo); got != ImageBuilderKaniko {
		t.Errorf("Forgejo builder = %q, want kaniko", got)
	}

	if env := ImageBuilderEnv(provider.ForgeGitHub); env != "" {
		t.Errorf("GitHub has no live builder setting, got %q", env)
	}
}
