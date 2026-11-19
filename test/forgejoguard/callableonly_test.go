// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

const selfBuildWorkflow = ".forgejo/workflows/self-pullrequest.yml"

// The repository's own gate is a consumer, not a callable layer component.
func reusableWorkflows(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, path := range allWorkflows(t) {
		if path != selfBuildWorkflow {
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
	if len(jobs) != 1 || jobs["go"].runsOn() != "${{ fromJSON(vars.CI_RUNNER_LABEL) }}" {
		t.Fatal("self-build must use the administrator's ordinary native runner")
	}
}

func TestSelfReleaseExampleSeparatesBuildFromPublication(t *testing.T) {
	t.Parallel()
	const file = "examples/forgejo/self-release.yml"
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
		if pin != "PIN_FORGE_CI_SHA" || !strings.HasSuffix(scalarString(mappingValue(job, "uses")), "/.forgejo/workflows/"+workflow+"@"+pin) {
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
		scalarString(mappingValue(with, "publish-container-images")) != "false" {
		t.Fatal("the CLI release must verify its request and publish no images")
	}
	body := string(reporoot.ReadFile(t, file))
	if !strings.Contains(body, ".release.disable = true | .checksum.disable = true | del(.signs)") ||
		!strings.Contains(body, "--object-format sha1 --fetch-all-refs") {
		t.Fatal("build from the prepared tagged checkout and disable the independent GoReleaser publisher")
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
