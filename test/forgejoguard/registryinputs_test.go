// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"strings"
	"testing"
)

// registryInputWorkflows are the workflows that fetch a pinned registry input
// in a job of its own and build from it in another: the download of the
// fetched layouts in the build job, and the build that consumes them.
var registryInputWorkflows = []struct{ file, download, build string }{
	{releaseImagesWorkflow, `-inputs-${IMAGE_NAME:?}-${IMAGE_ARCH:?}`, "release-images build-prepared"},
}

const (
	releaseImagesWorkflow = ".forgejo/workflows/release-images.yml"
	baseImagesWorkflow    = ".forgejo/workflows/base-images.yml"
)

func TestRegistryInputFetchSerializesArtifactFinalization(t *testing.T) {
	t.Parallel()

	for _, tc := range registryInputWorkflows {
		workflow := loadWorkflow(t, tc.file)

		strategy := mappingValue(workflow.jobs["fetch-inputs"].node, "strategy")
		if scalarString(mappingValue(strategy, "max-parallel")) != "1" {
			t.Errorf("%s: registry input uploads must avoid concurrent Forgejo chunk finalization", tc.file)
		}

		if scalarString(mappingValue(strategy, "matrix")) != `${{ fromJSON(needs.plan.outputs.fetch-required == 'true' && needs.plan.outputs.fetch-matrix || '{"include":[{"arch":""}]}') }}` {
			t.Errorf("%s: fetch must use the filtered matrix when required and retain one skipped dependency row otherwise", tc.file)
		}
		if got := scalarString(mappingValue(workflow.jobs["fetch-inputs"].node, "if")); got != needsGate([]string{"coordinates", "plan"}, "!cancelled()", "needs.plan.outputs.fetch-required == 'true'") {
			t.Errorf("%s: fetch must run only when the successful plan requires it: %s", tc.file, got)
		}
		const buildGate = "${{ !cancelled() && needs.coordinates.result == 'success' && needs.plan.result == 'success' && ((needs.plan.outputs.fetch-required == 'true' && needs.fetch-inputs.result == 'success') || (needs.plan.outputs.fetch-required == 'false' && (needs.fetch-inputs.result == 'skipped' || needs.fetch-inputs.result == 'success'))) }}"
		if got := scalarString(mappingValue(workflow.jobs["build"].node, "if")); got != buildGate {
			t.Errorf("%s: builds may accept skipped fetch only when the plan explicitly requires none; failures and cancellation must block: %s", tc.file, got)
		}

		buildStrategy := mappingValue(workflow.jobs["build"].node, "strategy")
		if mappingValue(buildStrategy, "max-parallel") != nil {
			t.Errorf("%s: registry input serialization must not serialize image builds", tc.file)
		}
	}
}

func TestPrivateRegistryFetchIsSeparatedFromConsumerBuild(t *testing.T) {
	t.Parallel()

	for _, tc := range registryInputWorkflows {
		workflow := loadWorkflow(t, tc.file)

		fetch, exists := workflow.jobs["fetch-inputs"]
		if !exists {
			t.Fatalf("%s: missing isolated registry fetch job", tc.file)
		}

		build := workflow.jobs["build"]
		if anyString(build.node, secretsWord.MatchString) {
			t.Fatalf("%s: consumer build must remain secret-free", tc.file)
		}

		fetchCommand := false

		for _, step := range fetch.steps() {
			run := scalarString(mappingValue(step, "run"))
			for _, forbidden := range []string{"platform checkout", "build-arch", "build-prepared", "base-images", "container build", "git -C source", "source/"} {
				if strings.Contains(run, forbidden) {
					t.Errorf("%s: credential fetch job executes consumer build/source: %s", tc.file, forbidden)
				}
			}

			if mappingValue(step, "uses") != nil {
				t.Errorf("%s: isolated fetch must not execute consumer actions", tc.file)
			}

			if strings.Contains(run, "release-images fetch-build-inputs") {
				fetchCommand = true

				if !strings.Contains(run, `unset REGISTRY_TOKEN`) || !strings.Contains(run, `unset registry_token`) || !strings.Contains(run, `trap 'rm -f -- "${auth_file}"' EXIT`) {
					t.Errorf("%s: fetch must isolate and clean its credential", tc.file)
				}
			}
		}

		if !fetchCommand {
			t.Errorf("%s: isolated fetch does not call the read-only CLI boundary", tc.file)
		}

		prepared := false
		restored := false

		for _, step := range build.steps() {
			run := scalarString(mappingValue(step, "run"))
			if strings.Contains(run, tc.download) {
				restored = true

				if tc.file == releaseImagesWorkflow && !strings.Contains(run, `--blob-store "${IMAGE_ARTIFACT_NAMESPACE}-input-blob"`) {
					t.Error("prepared input downloads must restore their upload blob store")
				}
			}

			if strings.Contains(run, tc.build) {
				prepared = true

				if tc.file == releaseImagesWorkflow && !strings.Contains(run, "container builder-identity") {
					t.Error("prepared builds must preserve native builder identity checks")
				}

			}
		}

		if !prepared {
			t.Errorf("%s: consumer build does not consume isolated inputs", tc.file)
		}

		if !restored {
			t.Errorf("%s: consumer build does not download prepared registry inputs", tc.file)
		}
	}
}

// A base stage separates pull and publication authority, and neither a
// failed plan nor a failed build reaches the next job.
func TestBaseImagesStageKeepsItsTokenAndItsGates(t *testing.T) {
	t.Parallel()

	workflow := loadWorkflow(t, baseImagesWorkflow)

	for name, job := range workflow.jobs {
		holds := anyString(job.node, secretsWord.MatchString)
		if may := name == "build" || name == "publish"; holds != may {
			t.Errorf("job %s: reads a secret = %t, want %t", name, holds, may)
		}
		if anyString(job.node, func(value string) bool { return strings.Contains(value, "secrets.RELEASE_REGISTRY_TOKEN") }) != (name == "publish") {
			t.Errorf("job %s: only publish may read RELEASE_REGISTRY_TOKEN", name)
		}
	}

	// The plan admits a child for either builder: Buildah and kaniko both
	// build it from the fetched parent, so the plan does not learn which.
	if got := mappingValue(mappingValue(workflow.jobs["plan"].node, "env"), "CI_IMAGE_BUILDER"); got != nil {
		t.Errorf("job plan admits the stage for builder %q, want no builder", scalarString(got))
	}

	for job, want := range map[string]string{
		"build":   "${{ needs.coordinates.result == 'success' && needs.plan.result == 'success' }}",
		"publish": "${{ always() && needs.coordinates.result == 'success' && needs.plan.result == 'success' && needs.build.result == 'success' }}",
	} {
		if got := strings.Join(strings.Fields(scalarString(mappingValue(workflow.jobs[job].node, "if"))), " "); got != want {
			t.Errorf("job %s runs if %q, want %q", job, got, want)
		}
	}
}

func TestBaseImagesPullsParentWithoutArtifactHandoff(t *testing.T) {
	t.Parallel()
	for _, file := range []string{baseImagesWorkflow, ".forgejo/workflows/base-image-lifecycle.yml"} {
		t.Run(file, func(t *testing.T) { checkBaseParentPull(t, file) })
	}
}

func checkBaseParentPull(t *testing.T, file string) {
	t.Helper()

	workflow := loadWorkflow(t, file)
	if _, exists := workflow.jobs["fetch-inputs"]; exists {
		t.Fatal("parent layouts must not pass through an artifact job")
	}

	fetch, build, secretRefs := -1, -1, 0
	anyString(workflow.jobs["build"].node, func(value string) bool {
		if secretsWord.MatchString(value) {
			secretRefs++
		}
		return false
	})
	if secretRefs != 1 {
		t.Fatal("the build job may reference only one secret")
	}
	for i, step := range workflow.jobs["build"].steps() {
		run := scalarString(mappingValue(step, "run"))
		switch {
		case strings.Contains(run, "release-images fetch-build-inputs"):
			fetch = i
			env := mappingValue(step, "env")
			if scalarString(mappingValue(step, "if")) != "${{ inputs['parent-base-json'] != '' }}" ||
				scalarString(mappingValue(env, "PARENT_REGISTRY_READ_TOKEN")) != "${{ secrets.PARENT_REGISTRY_READ_TOKEN }}" ||
				!strings.Contains(run, "unset PARENT_REGISTRY_READ_TOKEN") ||
				!strings.Contains(run, `.engine/bin/lib-parent-pull.sh"`) ||
				!strings.Contains(run, `forge_ci_pull_parent "$token" run_forge_ci`) ||
				!strings.Contains(run, `--parent-base-json "${PARENT_BASE_JSON:?}"`) ||
				strings.Contains(run, "--auth-file") || mappingValue(step, "continue-on-error") != nil {
				t.Fatal("the parent pull must be scoped, confined to this step and fail closed")
			}
		case strings.Contains(run, "artifact download"):
			t.Fatal("a base build must not restore a parent artifact")
		case strings.Contains(run, "container base-images child-base build"):
			build = i
			if !strings.Contains(run, "--inputs-dir registry-inputs") || !strings.Contains(run, `--image "${IMAGE_NAME:?}"`) {
				t.Fatal("each matrix build must consume its own checked parent layout")
			}
		}
	}

	if fetch < 0 || build <= fetch {
		t.Fatal("the parent fetch must precede the checked child build")
	}
}

// A child is planned from a parent projection of the verification workflow,
// for a base promoted in the run and for one reused alike. Both outputs come
// from the one step that verified the evidence, and no shell of the workflow
// composes the map of reused parents: the engine writes it, and only for
// authenticated reads.
func TestBaseParentProjectionsComeFromTheVerifyingStep(t *testing.T) {
	t.Parallel()

	const file = ".forgejo/workflows/verify-base-container-images.yml"

	called := mappingValue(mappingValue(mappingValue(loadDocument(t, file), "on"), "workflow_call"), "outputs")
	verify := loadWorkflow(t, file).jobs["verify-base-container-images"]

	for output, name := range map[string]string{"parent-base-json": "parent_base_json", "parent-bases-json": "parent_bases_json"} {
		if got, want := scalarString(mappingValue(mappingValue(called, output), "value")), "${{ jobs.verify-base-container-images.outputs."+name+" }}"; got != want {
			t.Errorf("%s: output %s is %q, want %q", file, output, got, want)
		}

		if got, want := scalarString(mappingValue(mappingValue(verify.node, "outputs"), name)), "${{ steps.verify.outputs."+name+" }}"; got != want {
			t.Errorf("%s: job output %s is %q, want %q", file, name, got, want)
		}
	}

	for _, step := range verify.steps() {
		run := scalarString(mappingValue(step, "run"))
		if strings.Contains(run, "parent_bases_json") {
			t.Errorf("%s: a step writes parent_bases_json itself", file)
		}

		if strings.Contains(run, "parent_base_json") != (scalarString(mappingValue(step, "id")) == "verify") {
			t.Errorf("%s: parent_base_json is written outside the verify step, or not by it", file)
		}
	}
}
