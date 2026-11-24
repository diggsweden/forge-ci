// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/ast"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

const selfBuildWorkflow = ".forgejo/workflows/self-pullrequest.yml"

// selfEntryPointExamples are the callers a fork copies to .forgejo/workflows
// to release Forge CI itself and publish its runtime images.
var selfEntryPointExamples = []string{"examples/forgejo/self-release.yml", "examples/forgejo/self-runtime-images.yml"}

// isSelfEntryPoint reports whether a repository-relative path is one of the
// hosting repository's own entry points: a self-*.yml workflow the repository
// starts itself and no consumer calls. Upstream ships the self-build; a fork
// adds its own, the release and runtime callers from the examples among them,
// with no change here. Such a file is a consumer of the layer, so it may name
// its own organization and pin its own images. Name and trigger are held to
// agree (TestOwnPipelineNamesMatchTheirTriggers), so a layer workflow cannot
// become one without a rename.
func isSelfEntryPoint(path string) bool {
	path = filepath.ToSlash(path)

	return filepath.ToSlash(filepath.Dir(path)) == ".forgejo/workflows" && validate.OwnPipeline(path)
}

// The repository's own entry points are consumers, not callable layer components.
func reusableWorkflows(t *testing.T) []string {
	t.Helper()

	var paths []string

	for _, path := range allWorkflows(t) {
		if !isSelfEntryPoint(path) {
			paths = append(paths, path)
		}
	}

	return paths
}

// allWorkflows lists the workflows a Forgejo instance discovers in this
// repository, as repository-relative paths.
func allWorkflows(t *testing.T) []string {
	t.Helper()

	var paths []string

	for _, entry := range reporoot.ReadDir(t, ".forgejo/workflows") {
		if extension := filepath.Ext(entry.Name()); extension != ".yml" && extension != ".yaml" {
			t.Errorf(".forgejo/workflows/%s is not a workflow file; Forgejo discovers this directory", entry.Name())

			continue
		}

		paths = append(paths, ".forgejo/workflows/"+entry.Name())
	}

	if len(paths) == 0 {
		t.Fatal("found no Forgejo workflows to inspect")
	}

	return paths
}

// callableOnlyViolation reports why a workflow could start without a caller.
func callableOnlyViolation(data []byte) string {
	root, err := parseDocument(data)
	if err != nil {
		return err.Error()
	}

	events := mappingEntries(mappingValue(root, "on"))
	if len(events) != 1 || scalarString(events[0].Key) != "workflow_call" {
		return "`on:` must be a mapping with workflow_call as its only event"
	}

	return ""
}

// A Forgejo instance that hosts this repository runs whatever it discovers
// under .forgejo/workflows, and prefers that directory over .github/workflows.
// Every layer component is a reusable workflow for consumers: it starts only when
// another workflow calls it, so a push, a pull request or a schedule in this
// repository starts none of them, and none can reach a signing secret of the
// hosting organization.
func TestLayerWorkflowsStartOnlyWhenCalled(t *testing.T) {
	t.Parallel()

	for _, path := range reusableWorkflows(t) {
		if violation := callableOnlyViolation(reporoot.ReadFile(t, path)); violation != "" {
			t.Errorf("%s: %s", path, violation)
		}
	}
}

func TestSelfBuildIsTheOnlyUnprivilegedLocalEntryPoint(t *testing.T) {
	t.Parallel()
	body := reporoot.ReadFile(t, selfBuildWorkflow)

	root, err := parseDocument(body)
	if err != nil {
		t.Fatal(err)
	}

	events := mappingEntries(mappingValue(root, "on"))
	if len(events) != 2 || mappingValue(mappingValue(root, "on"), "pull_request") == nil || mappingValue(mappingValue(root, "on"), "workflow_dispatch") == nil {
		t.Fatal("self-build may start only for a pull request or manual dispatch")
	}

	if strings.Contains(string(body), "secrets.") || mappingValue(root, "permissions") != nil {
		t.Fatal("self-build must not request secrets or imply Forgejo enforces GitHub permissions")
	}

	jobs := loadWorkflow(t, selfBuildWorkflow).jobs

	const runner = "${{ fromJSON(vars.CI_RUNNER_LABEL) }}"
	if len(jobs) != 2 || jobs["go"].runsOn() != runner || jobs["blackbox"].runsOn() != runner {
		t.Fatal("self-build must use the administrator's ordinary native runner")
	}
	// The suite is optional and named by the administrator alone: a pull
	// request cannot choose the repository, the commit or the image.
	blackbox := jobs["blackbox"]
	if scalarString(mappingValue(blackbox.node, "if")) != "forgejo.event_name == 'pull_request' && vars.FORGE_CI_TESTSUITE_REPOSITORY != '' && vars.FORGE_CI_TESTSUITE_IMAGE != ''" ||
		blackbox.containerImage() != "${{ vars.FORGE_CI_TESTSUITE_IMAGE }}" {
		t.Fatal("the black-box job must run for pull requests only, where an administrator configured the suite and its image")
	}

	if mentions(blackbox.node, "forgejo.event.") || mentions(blackbox.node, "inputs.") {
		t.Fatal("the black-box job must not read its coordinates from the event")
	}
}

// The example, and the copy a fork installs from it, keep the same shape. The
// example pins a placeholder; an installed copy pins a full commit.
func TestSelfReleaseSeparatesBuildFromPublication(t *testing.T) {
	t.Parallel()

	const example = "examples/forgejo/self-release.yml"
	checkSelfRelease(t, example, func(pin string) bool { return pin == "PIN_FORGE_CI_SHA" })

	const installed = ".forgejo/workflows/self-release.yml"
	if _, err := os.Stat(filepath.Join(reporoot.Path(t), installed)); err == nil {
		checkSelfRelease(t, installed, func(pin string) bool {
			return len(pin) == 40 && strings.Trim(pin, "0123456789abcdef") == ""
		})
	}
}

func checkSelfRelease(t *testing.T, file string, reviewedPin func(string) bool) {
	t.Helper()
	root := loadDocument(t, file)
	events := mappingValue(root, "on")

	tags := sequenceValues(mappingValue(mappingValue(events, "push"), "tags"))
	if len(mappingEntries(events)) != 1 || len(tags) != 1 || scalarString(tags[0]) != "release-request/v*" {
		t.Fatal("self-release starts only from signed release-request tags")
	}

	jobs := loadWorkflow(t, file).jobs

	const releaseTools = "go aqua:goreleaser/goreleaser aqua:anchore/syft yq"

	for _, name := range []string{"candidate", "build"} {
		found := false

		for _, step := range jobs[name].steps() {
			if !strings.Contains(scalarString(mappingValue(step, "uses")), "/.forgejo/actions/setup-toolchain@") {
				continue
			}

			with := mappingValue(step, "with")
			if scalarString(mappingValue(with, "tools")) != releaseTools || scalarString(mappingValue(with, "mise-locked")) != "true" {
				t.Fatalf("%s must validate the complete locked release toolchain", name)
			}

			found = true
		}

		if !found {
			t.Fatalf("%s must install the release toolchain", name)
		}
	}

	if len(jobs) != 4 {
		t.Fatal("candidate, preparation, build and publication must remain separate")
	}

	for _, name := range []string{"candidate", "build"} {
		if job := jobs[name]; job.node == nil || strings.Contains(job.node.String(), "secrets.") {
			t.Fatalf("%s must not receive release secrets", name)
		}
	}

	for name, workflow := range map[string]string{"prepare": "prepare-release.yml", "sign-and-publish": "sign-and-publish-release.yml"} {
		job := jobs[name].node
		with := mappingValue(job, "with")

		pin := scalarString(mappingValue(with, "forge-ci-sha"))
		if !reviewedPin(pin) || !strings.HasSuffix(scalarString(mappingValue(job, "uses")), "/.forgejo/workflows/"+workflow+"@"+pin) {
			t.Fatalf("%s must use the matching reviewed engine and workflow pin", name)
		}
	}

	with := mappingValue(jobs["prepare"].node, "with")
	if scalarString(mappingValue(with, "bot-name")) != "${{ vars.RELEASE_BOT_NAME }}" ||
		scalarString(mappingValue(with, "bot-email")) != "${{ vars.RELEASE_BOT_EMAIL }}" {
		t.Fatal("self-release must pass the host's configured author identity")
	}

	steps := jobs["candidate"].steps()
	if len(steps) == 0 || scalarString(mappingValue(steps[0], "name")) != "Check required release variables" {
		t.Fatal("check release settings before candidate checkout")
	}

	for _, field := range []string{"RELEASE_BOT_NAME", "RELEASE_BOT_EMAIL", "RELEASE_GIT_HOST_KEY_FINGERPRINT"} {
		if scalarString(mappingValue(mappingValue(steps[0], "env"), field)) != "${{ vars."+field+" }}" ||
			!strings.Contains(mappingValue(steps[0], "run").String(), field) {
			t.Fatalf("preflight must require %s", field)
		}
	}

	if scalarString(mappingValue(with, "verify-release-request-signature")) != "true" ||
		scalarString(mappingValue(with, "publish-containers")) != "false" {
		t.Fatal("the CLI release must verify its request and publish no images")
	}

	body := string(reporoot.ReadFile(t, file))
	if !strings.Contains(body, ".release.disable = true | .checksum.disable = true | del(.signs)") ||
		!strings.Contains(body, "--object-format sha1 --fetch-all-refs") {
		t.Fatal("build from the prepared tagged checkout and disable the independent GoReleaser publisher")
	}
}

// enginePins returns every commit a workflow pins the layer at: the ref of
// each reusable workflow and action it uses from a forge-ci repository, and
// each forge-ci-sha input it passes.
func enginePins(w workflow) []string {
	var pins []string

	add := func(node ast.Node) {
		uses := scalarString(mappingValue(node, "uses"))
		if _, ref, ok := strings.Cut(uses, "@"); ok && strings.Contains(uses, "/forge-ci/.forgejo/") {
			pins = append(pins, ref)
		}

		if sha := mappingValue(mappingValue(node, "with"), "forge-ci-sha"); sha != nil {
			pins = append(pins, scalarString(sha))
		}
	}
	for _, job := range w.jobs {
		add(job.node)

		for _, step := range job.steps() {
			add(step)
		}
	}

	return pins
}

// A self entry point runs one reviewed engine: the workflows and actions it
// uses and the engine commit it passes them are the same commit. A caller that
// mixed two would run one commit's workflow with another commit's binary.
func TestSelfEntryPointsPinOneEngineCommit(t *testing.T) {
	t.Parallel()

	files := append([]string(nil), selfEntryPointExamples...)

	for _, path := range allWorkflows(t) {
		if isSelfEntryPoint(path) {
			files = append(files, path)
		}
	}

	pinned := 0

	for _, file := range files {
		pins := enginePins(loadWorkflow(t, file))
		if len(pins) > 0 {
			pinned++
		}

		for _, pin := range pins {
			if pin != pins[0] {
				t.Errorf("%s pins the engine at both %s and %s", file, pins[0], pin)
			}
		}
	}

	if pinned == 0 {
		t.Fatal("found no self entry point that pins the engine, so the check proves nothing")
	}
}

func TestSelfEntryPointsAreSelfWorkflows(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]bool{
		".forgejo/workflows/self-pullrequest.yml":  true,
		".forgejo/workflows/self-release.yml":      true,
		".forgejo/workflows/self-signer-image.yml": true,
		".forgejo/workflows/prepare-release.yml":   false,
		".forgejo/workflows/runtime-images.yml":    false,
		".forgejo/actions/self-release/action.yml": false,
		"examples/forgejo/self-release.yml":        false,
		".github/workflows/self-release.yml":       false,
	} {
		if isSelfEntryPoint(path) != want {
			t.Errorf("isSelfEntryPoint(%q) = %t, want %t", path, !want, want)
		}
	}
}

// The forge calls only a workflow_call workflow, so that trigger is what makes
// a file part of the layer. A self-*.yml name must say the same: otherwise a
// layer workflow whose workflow_call gave way to a push would start here, with
// this repository's secrets, as an own pipeline.
func TestOwnPipelineNamesMatchTheirTriggers(t *testing.T) {
	t.Parallel()

	for _, path := range allWorkflows(t) {
		if mismatch := validate.OwnPipelineMismatch(path, reporoot.ReadFile(t, path)); mismatch != "" {
			t.Errorf("%s: %s", path, mismatch)
		}
	}
}

func TestCallableOnlyRejectsEveryOtherTrigger(t *testing.T) {
	t.Parallel()

	if violation := callableOnlyViolation([]byte("on:\n  workflow_call:\n    inputs: {}\njobs: {}\n")); violation != "" {
		t.Errorf("a workflow_call-only workflow was refused: %s", violation)
	}

	for name, document := range map[string]string{
		"push beside the call": "on:\n  workflow_call:\n  push:\njobs: {}\n",
		"push only":            "on:\n  push:\njobs: {}\n",
		"scalar event":         "on: push\njobs: {}\n",
		"event list":           "on: [workflow_call, push]\njobs: {}\n",
		"manual dispatch":      "on:\n  workflow_dispatch:\njobs: {}\n",
		"no trigger":           "jobs: {}\n",
	} {
		if callableOnlyViolation([]byte(document)) == "" {
			t.Errorf("the guard accepted a workflow with %s", name)
		}
	}
}
