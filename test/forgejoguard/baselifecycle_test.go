// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"slices"
	"strings"
	"testing"
)

const baseLifecycleWorkflow = ".forgejo/workflows/base-image-lifecycle.yml"

func TestBaseLifecycleKeepsDirectJobsAndSeparateAuthorities(t *testing.T) {
	t.Parallel()
	w := loadWorkflow(t, baseLifecycleWorkflow)
	wantNeeds := map[string][]string{
		"coordinates":  nil,
		"plan":         {"coordinates"},
		"build":        {"coordinates", "plan"},
		"publish":      {"coordinates", "plan", "build"},
		"sign":         {"coordinates", "publish"},
		"promote":      {"coordinates", "publish", "sign"},
		"authenticate": {"coordinates", "publish", "promote"},
	}
	if len(w.jobs) != len(wantNeeds) {
		t.Fatal("unexpected lifecycle job set")
	}
	for name, needs := range wantNeeds {
		j, ok := w.jobs[name]
		if !ok || mappingValue(j.node, "uses") != nil {
			t.Fatalf("%s must be a direct job", name)
		}
		if !slices.Equal(jobNeeds(j), needs) {
			t.Errorf("%s has unexpected dependencies: %v", name, jobNeeds(j))
		}
		if len(needs) > 0 {
			want := strings.Replace(needsGate(needs), "${{ ", "${{ !cancelled() && ", 1)
			if scalarString(mappingValue(j.node, "if")) != want {
				t.Errorf("%s must require success of every dependency and reject cancellation", name)
			}
		}
		anyString(j.node, func(value string) bool {
			if strings.Contains(value, "secrets.RELEASE_COSIGN_") && name != "sign" {
				t.Errorf("%s receives signing authority", name)
			}
			if strings.Contains(value, "secrets.RELEASE_REGISTRY_TOKEN") && (name == "plan" || name == "build" || name == "coordinates") {
				t.Errorf("%s receives publication authority", name)
			}
			if strings.Contains(value, "secrets.PARENT_REGISTRY_READ_TOKEN") && name != "build" {
				t.Errorf("%s receives parent pull authority", name)
			}
			return false
		})
	}
	for _, name := range []string{"promote", "authenticate"} {
		found := false
		for _, step := range w.jobs[name].steps() {
			env := mappingValue(step, "env")
			if mappingValue(env, "BASE_INPUTS_JSON") != nil {
				found = true
				if scalarString(mappingValue(env, "BASE_INPUTS_JSON")) != "${{ inputs['expected-inputs-json'] }}" {
					t.Errorf("%s must verify the caller's independent expectations", name)
				}
			}
		}
		if !found {
			t.Errorf("%s does not receive independent expectations", name)
		}
	}
	for _, name := range []string{"plan", "build", "publish"} {
		if !mentions(w.jobs[name].node, `--stage-flavors "${STAGE_FLAVOR:?}"`) {
			t.Errorf("%s must admit exactly the caller's flavor", name)
		}
	}
	for _, step := range w.jobs["sign"].steps() {
		env := mappingValue(step, "env")
		if mappingValue(env, "SBOM_ARTIFACT") != nil && scalarString(mappingValue(env, "SBOM_ARTIFACT")) != "${{ format('{0}-sboms', inputs['artifact-name']) }}" {
			t.Error("signing must consume this lifecycle's namespaced SBOM artifact")
		}
	}
	outputs := mappingValue(mappingValue(mappingValue(loadDocument(t, baseLifecycleWorkflow), "on"), "workflow_call"), "outputs")
	for name, output := range map[string]string{"ledger-json": "ledger_json", "parent-base-json": "parent_base_json"} {
		if scalarString(mappingValue(mappingValue(outputs, name), "value")) != "${{ jobs.authenticate.outputs."+output+" }}" {
			t.Errorf("%s must be exposed only through authentication", name)
		}
	}
}

func TestBaseLifecycleCoordinatesAdmitOneFlavorAndBothBuilders(t *testing.T) {
	t.Parallel()
	script := preflightScript(t, baseLifecycleWorkflow)
	for _, flavor := range []string{"", "a,b", "a b", "../base", "base\nchild"} {
		ok, output, _ := runPreflightWithStderr(t, script, exampleRepository, exampleSigner, "FLAVOR_INPUT="+flavor)
		if ok || output != "" {
			t.Errorf("invalid flavor %q was admitted", flavor)
		}
	}
	for _, builder := range []string{"buildah", "kaniko"} {
		ok, output, stderr := runPreflightWithStderr(t, script, exampleRepository, exampleSigner, "BUILDER_INPUT="+builder, "BUILDER_JOB_IMAGE_INPUT="+exampleSigner)
		if !ok || !strings.Contains(output, "builder="+builder+"\n") {
			t.Fatalf("%s refused: %s", builder, stderr)
		}
	}
	ok, output, _ := runPreflightWithStderr(t, script, exampleRepository, exampleSigner, "BUILDER_INPUT=kaniko", "BUILDER_JOB_IMAGE_INPUT=registry.example/builder:latest")
	if ok || output != "" {
		t.Fatal("mutable builder image admitted")
	}
}
