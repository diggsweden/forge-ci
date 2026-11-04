// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/domain/enforcement"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

func testWorkflowAdmin(t *testing.T) *workflowAdmin {
	t.Helper()

	root := reporoot.Path(t)
	commit := stageTestGit(t, githubStageGit(), root, "rev-parse", "HEAD")

	admin, err := newWorkflowAdmin(t.Context(), root, commit, func(key string) string {
		if key == adminInputEnvironment(enforcement.ReproducibleBuilds) {
			return ""
		}

		return "false"
	})
	if err != nil {
		t.Fatal(err)
	}

	return admin
}

func TestAdminInputsDefaultEnabledAndCanBeDisabledIndependently(t *testing.T) {
	t.Parallel()

	defaults, err := readAdminInputs(func(string) string { return "" })
	if err != nil || len(defaults) != len(enforcement.Checks) {
		t.Fatalf("defaults=%v err=%v", defaults, err)
	}

	for _, disabled := range enforcement.Checks {
		values, err := readAdminInputs(func(key string) string {
			if key == adminInputEnvironment(disabled) {
				return "false"
			}

			return ""
		})
		if err != nil {
			t.Fatal(err)
		}

		for _, check := range enforcement.Checks {
			if !defaults[enforcement.Setting(check)] || values[enforcement.Setting(check)] != (check != disabled) {
				t.Fatalf("settings are not independent: disabled=%s defaults=%v values=%v", disabled, defaults, values)
			}
		}
	}

	allDisabled, err := readAdminInputs(func(string) string { return "false" })
	if err != nil {
		t.Fatal(err)
	}

	for key, enabled := range allDisabled {
		if enabled {
			t.Fatalf("administrator could not disable %s", key)
		}
	}

	for _, value := range []string{"1", "FALSE", "false\n", "${VALUE}"} {
		if _, err := readAdminInputs(func(string) string { return value }); !errors.Is(err, errs.ErrValidation) {
			t.Fatalf("accepted administrator boolean %q: %v", value, err)
		}
	}
}

func TestAdminInputsWritesOnlyTheControllerOwnedInclude(t *testing.T) {
	for _, check := range enforcement.Checks {
		t.Setenv(adminInputEnvironment(check), "false")
	}

	source := map[string]any{"job-image": "registry.invalid/tools", "commands": []string{"go test ./..."}}

	bound := AdminInputs(t, source)
	if len(source) != 2 || len(bound) != 2+len(enforcement.Checks) {
		t.Fatalf("administrator input binding modified its source or lost fields: source=%v bound=%v", source, bound)
	}

	for _, check := range enforcement.Checks {
		if value, ok := bound[enforcement.Setting(check)].(bool); !ok || value {
			t.Fatalf("administrator could not disable %s on its include", check)
		}
	}
}

func TestConsumerPlanBindsAdminInputsOnlyInItsGeneratedWrappers(t *testing.T) {
	t.Parallel()

	layer := sampleLayer()
	layer.admin = testWorkflowAdmin(t)
	layer.Commit = layer.admin.commit
	text := "on: [push]\njobs:\n  image:\n    uses: " + layer.workflowBase() + ".forgejo/workflows/snapshot-images.yml@" + layer.Commit + "\n"

	plan, err := layer.PlanConsumer(map[string]string{"first.yml": text, "second.yml": text}, ConsumerFacts{})
	if err != nil {
		t.Fatal(err)
	}

	if rendered := plan.Render(text); !strings.Contains(rendered, "enforce-application-tests: false") || !strings.Contains(rendered, "enforce-vulnerabilities: false") {
		t.Fatalf("controller's wrapper lost administrator inputs: %s", rendered)
	}

	if plan.Render("ordinary source text") != "ordinary source text" || strings.Contains(text, "enforce-") {
		t.Fatal("administrator inputs escaped the generated wrappers")
	}
}

func TestForgejoAdminWrapperBindsOnlyInputsDeclaredByItsPinnedEngine(t *testing.T) {
	t.Parallel()

	layer := sampleLayer()
	layer.admin = testWorkflowAdmin(t)
	layer.Commit = layer.admin.commit
	text := "name: controller wrapper\non: [push]\njobs:\n  image:\n    uses: " + layer.workflowBase() + ".forgejo/workflows/snapshot-images.yml@" + layer.Commit + "\n" +
		"  checks:\n    runs-on: test\n    steps:\n      - uses: " + layer.actionBase() + ".forgejo/actions/lint-nanolinter@" + layer.Commit + "\n" +
		"        with:\n          args: --require secrets\n      - uses: other/tool@" + layer.Commit + "\n        with:\n          argument: retained\n"

	rendered, err := layer.renderAdmin(text)
	if err != nil {
		t.Fatal(err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal([]byte(rendered), &document); err != nil {
		t.Fatal(err)
	}

	jobs := gitlabMappingValue(document.Content[0], "jobs")
	image := gitlabMappingValue(gitlabMappingValue(jobs, "image"), "with")
	assertAdminBooleans(t, image, map[string]bool{"enforce-application-tests": false, "enforce-vulnerabilities": false})

	steps := gitlabMappingValue(gitlabMappingValue(jobs, "checks"), "steps")
	assertAdminBooleans(t, gitlabMappingValue(steps.Content[0], "with"), map[string]bool{"enforce-lint": false, "enforce-vulnerabilities": false})

	foreign := gitlabMappingValue(steps.Content[1], "with")
	if len(foreign.Content) != 2 || gitlabMappingValue(foreign, "argument").Value != "retained" {
		t.Fatal("administrator inputs escaped into a foreign action")
	}

	if strings.Contains(text, "enforce-") {
		t.Fatal("rendering changed the source wrapper")
	}
}

func assertAdminBooleans(t *testing.T, inputs *yaml.Node, wanted map[string]bool) {
	t.Helper()

	got := map[string]bool{}

	if inputs == nil {
		t.Fatal("missing administrator inputs")
	}

	for i := 0; i+1 < len(inputs.Content); i += 2 {
		key, value := inputs.Content[i].Value, inputs.Content[i+1]
		if !strings.HasPrefix(key, "enforce-") {
			continue
		}

		if value.Tag != "!!bool" || value.Value != "true" && value.Value != "false" {
			t.Fatalf("administrator input %s is not a literal boolean: %+v", key, value)
		}

		got[key] = value.Value == "true"
	}

	if !reflect.DeepEqual(got, wanted) {
		t.Fatalf("administrator inputs=%v, want %v", got, wanted)
	}
}

func TestAdminWrapperRejectsConsumerValuesAndMergedInputAuthority(t *testing.T) {
	t.Parallel()

	layer := sampleLayer()
	layer.admin = testWorkflowAdmin(t)
	layer.Commit = layer.admin.commit

	for _, inputs := range []string{
		"      enforce-vulnerabilities: true\n",
		"      enforce-vulnerabilities: ${{ vars.VALUE }}\n",
		"      <<: {enforce-vulnerabilities: false}\n",
	} {
		text := "jobs:\n  image:\n    uses: " + layer.workflowBase() + ".forgejo/workflows/snapshot-images.yml@" + layer.Commit + "\n    with:\n" + inputs
		if _, err := layer.renderAdmin(text); !errors.Is(err, errs.ErrValidation) {
			t.Fatalf("consumer supplied administrator authority: %v", err)
		}
	}
}

func TestGitHubAdminWrapperUsesItsEngineContract(t *testing.T) {
	t.Parallel()

	engine := trialEngine
	engine.admin = testWorkflowAdmin(t)
	engine.Commit = engine.admin.commit
	text := "jobs:\n  quality:\n    uses: diggsweden/forge-ci/.github/workflows/pullrequest-orchestrator.yml@" + strings.Repeat("c", 40) + "\n    with:\n      runtime-image: ghcr.io/diggsweden/forge-ci-runtime:tag\n"

	rendered, _, err := RenderGitHubConsumer("quality.yml", text, engine, nil)
	if err != nil {
		t.Fatal(err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal([]byte(rendered), &document); err != nil {
		t.Fatal(err)
	}

	job := gitlabMappingValue(gitlabMappingValue(document.Content[0], "jobs"), "quality")
	assertAdminBooleans(t, gitlabMappingValue(job, "with"), map[string]bool{"enforce-lint": false, "enforce-vulnerabilities": false})
}

func TestGitLabAdminPolicyKeepsEnforcementOutOfConsumerRendering(t *testing.T) {
	t.Parallel()

	layer := Layer{ServerURL: "https://gitlab.k3s.forgelab", Repository: "forgeci/engine", Commit: strings.Repeat("b", 40), admin: testWorkflowAdmin(t)}
	layer.Commit = layer.admin.commit
	pipeline := "workflow:\n  rules:\n    - if: '$CI_PIPELINE_SOURCE == \"merge_request_event\"'\ninclude:\n" +
		"  - component: $CI_SERVER_FQDN/team/engine/check@" + strings.Repeat("a", 40) + "\n    inputs:\n      forge-ci-repository: team/engine\n      server-url: $CI_SERVER_URL\n      job-image: $CI_JOB_IMAGE\n      commands: [go test ./...]\n"
	images := GitLabGateImages{Job: "registry.invalid/tools@sha256:" + strings.Repeat("c", 64)}

	consumer, err := renderGitLabConsumer([]byte(pipeline), layer, images)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(consumer, "enforce-") {
		t.Fatal("enforcement was written into consumer CI")
	}

	policy, err := renderGitLabAdminPolicy([]byte(pipeline), layer, images)
	if err != nil {
		t.Fatal(err)
	}

	_, entries, err := gitlabGateComponents([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}

	assertAdminBooleans(t, gitlabMappingValue(entries[0], "inputs"), map[string]bool{
		"enforce-lint": false, "enforce-vulnerabilities": false, "enforce-application-tests": false, "enforce-reproducible-builds": true,
	})
}

func TestAdminDeclarationsComeFromThePinnedGitTree(t *testing.T) {
	t.Parallel()

	root, git := stageTestRepo(t, "sha1")
	path := ".github/workflows/check.yml"
	stageTestWrite(t, root, path, "on:\n  workflow_call:\n    inputs:\n      enforce-lint:\n        type: boolean\n", 0o600)
	stageTestCommit(t, git, root)
	commit := stageTestGit(t, git, root, "rev-parse", "HEAD")
	stageTestWrite(t, root, path, "on:\n  workflow_call:\n    inputs:\n      enforce-vulnerabilities:\n        type: boolean\n", 0o600)

	admin, err := newWorkflowAdmin(t.Context(), root, commit, func(string) string { return "false" })
	if err != nil {
		t.Fatal(err)
	}

	text := "jobs:\n  check:\n    uses: owner/engine/.github/workflows/check.yml@" + commit + "\n"

	rendered, err := admin.render(text, map[string]string{"owner/engine/.github/workflows/": githubWorkflowDirectory}, commit)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(rendered, "enforce-lint: false") || strings.Contains(rendered, "enforce-vulnerabilities") {
		t.Fatalf("working-tree edits changed the pinned input contract: %s", rendered)
	}

	if _, err := admin.render(text, nil, strings.Repeat("a", 40)); !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("accepted declarations for a different engine commit: %v", err)
	}
}

func TestDevelopmentAdminPolicyPreservesRootPushAdmissionAndJobs(t *testing.T) {
	t.Parallel()

	root, git := stageTestRepo(t, "sha1")
	pipeline := "workflow:\n  rules:\n    - if: '$CI_COMMIT_BRANCH == \"development\"'\ninclude:\n  - local: .gitlab/merge-request.yml\ndevelopment-job:\n  script: echo development-root-marker\n"
	gate := "workflow:\n  rules:\n    - if: '$CI_PIPELINE_SOURCE == \"merge_request_event\"'\ninclude:\n" +
		"  - component: $CI_SERVER_FQDN/team/engine/check@" + strings.Repeat("a", 40) + "\n    inputs:\n      forge-ci-repository: team/engine\n      server-url: $CI_SERVER_URL\n      job-image: $CI_JOB_IMAGE\n      commands: [go test ./...]\n"

	stageTestWrite(t, root, gitlabCIFile, pipeline, 0o600)
	stageTestWrite(t, root, GitLabGateFile, gate, 0o600)
	stageTestCommit(t, git, root)
	consumer := Consumer{git: git, stage: root, Commit: stageTestGit(t, git, root, "rev-parse", "HEAD")}
	layer := Layer{ServerURL: "https://gitlab.k3s.forgelab", Repository: "forgeci/engine", admin: testWorkflowAdmin(t)}
	layer.Commit = layer.admin.commit
	images := GitLabGateImages{Job: "registry.invalid/tools@sha256:" + strings.Repeat("c", 64)}

	selected, renderedGate := consumer.GitLabDevelopmentPolicy(t, layer, images)
	if !strings.Contains(selected, "development-root-marker") || !strings.Contains(selected, "$CI_COMMIT_BRANCH") || strings.Contains(selected, "merge_request_event") {
		t.Fatalf("development selected the MR-only pipeline or lost root jobs: %s", selected)
	}

	if !strings.Contains(selected, "local: "+GitLabAdminGateFile) || strings.Contains(selected, GitLabGateFile) || !strings.Contains(renderedGate, "enforce-application-tests: false") {
		t.Fatalf("development root did not select its pinned administrator gate: root=%s gate=%s", selected, renderedGate)
	}

	if got := stageTestGit(t, git, root, "show", consumer.Commit+":"+gitlabCIFile); got != strings.TrimSpace(pipeline) {
		t.Fatal("development policy rendering modified the consumer's source tree")
	}

	for _, include := range []string{"include: .gitlab/merge-request.yml\n", "include:\n  local: .gitlab/merge-request.yml\n"} {
		short := strings.Replace(pipeline, "include:\n  - local: .gitlab/merge-request.yml\n", include, 1)

		selected, _, err := renderGitLabDevelopmentPolicy([]byte(short), []byte(gate), layer, images)
		if err != nil || !strings.Contains(selected, "development-root-marker") || strings.Contains(selected, GitLabGateFile) {
			t.Fatalf("valid GitLab include spelling lost its administrator gate: root=%s err=%v", selected, err)
		}
	}
}
