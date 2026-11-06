// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"strings"
	"testing"
)

func TestRegistryInputFetchSerializesArtifactFinalization(t *testing.T) {
	t.Parallel()
	workflow := loadWorkflow(t, ".forgejo/workflows/release-images.yml")

	strategy := mappingValue(workflow.jobs["fetch-inputs"].node, "strategy")
	if scalarString(mappingValue(strategy, "max-parallel")) != "1" {
		t.Error("registry input uploads must avoid concurrent Forgejo chunk finalization")
	}

	matrix := mappingValue(strategy, "matrix")
	if scalarString(mappingValue(matrix, "build")) != "${{ fromJSON(needs.plan.outputs.builds) }}" {
		t.Error("serialized fetch must still cover every planned image and architecture")
	}

	buildStrategy := mappingValue(workflow.jobs["build"].node, "strategy")
	if mappingValue(buildStrategy, "max-parallel") != nil {
		t.Error("registry input serialization must not serialize image builds")
	}
}

func TestPrivateRegistryFetchIsSeparatedFromConsumerBuild(t *testing.T) {
	t.Parallel()
	workflow := loadWorkflow(t, ".forgejo/workflows/release-images.yml")

	fetch, exists := workflow.jobs["fetch-inputs"]
	if !exists {
		t.Fatal("missing isolated registry fetch job")
	}

	build := workflow.jobs["build"]
	if anyString(build.node, secretsWord.MatchString) {
		t.Fatal("consumer build must remain secret-free")
	}

	fetchCommand := false

	for _, step := range fetch.steps() {
		run := scalarString(mappingValue(step, "run"))
		for _, forbidden := range []string{"platform checkout", "build-arch", "build-prepared", "container build", "git -C source", "source/"} {
			if strings.Contains(run, forbidden) {
				t.Errorf("credential fetch job executes consumer build/source: %s", forbidden)
			}
		}

		if mappingValue(step, "uses") != nil {
			t.Error("isolated fetch must not execute consumer actions")
		}

		if strings.Contains(run, "release-images fetch-build-inputs") {
			fetchCommand = true

			if !strings.Contains(run, `unset REGISTRY_TOKEN`) || !strings.Contains(run, `unset registry_token`) || !strings.Contains(run, `trap 'rm -f -- "${auth_file}"' EXIT`) {
				t.Error("fetch must isolate and clean its credential")
			}
		}
	}

	if !fetchCommand {
		t.Error("isolated fetch does not call the read-only CLI boundary")
	}

	prepared := false
	restored := false

	for _, step := range build.steps() {
		run := scalarString(mappingValue(step, "run"))
		if strings.Contains(run, `-inputs-${IMAGE_NAME:?}-${IMAGE_ARCH:?}`) {
			restored = true

			if !strings.Contains(run, `--blob-store "${IMAGE_ARTIFACT_NAMESPACE}-input-blob"`) {
				t.Error("prepared input downloads must restore their upload blob store")
			}
		}

		if strings.Contains(run, "release-images build-prepared") {
			prepared = true

			if !strings.Contains(run, "container builder-identity") {
				t.Error("prepared builds must preserve native builder identity checks")
			}
		}
	}

	if !prepared {
		t.Error("consumer build does not consume isolated inputs")
	}

	if !restored {
		t.Error("consumer build does not download prepared registry inputs")
	}
}
