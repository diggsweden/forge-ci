// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"gopkg.in/yaml.v3"
)

type githubReleaseStep struct {
	Name             string            `yaml:"name"`
	Run              string            `yaml:"run"`
	If               string            `yaml:"if"`
	ContinueOnError  any               `yaml:"continue-on-error"`
	WorkingDirectory string            `yaml:"working-directory"`
	Env              map[string]string `yaml:"env"`
	With             map[string]any    `yaml:"with"`
}

func TestGitHubPublisherReusesDigestBoundSignedPayload(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Needs       []string            `yaml:"needs"`
			Permissions map[string]string   `yaml:"permissions"`
			Outputs     map[string]string   `yaml:"outputs"`
			Steps       []githubReleaseStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(reporoot.ReadFile(t, ".github/workflows/release-create-github.yml"), &workflow); err != nil {
		t.Fatal(err)
	}

	signer, publisher := workflow.Jobs["sign-release"], workflow.Jobs["create-release"]
	if !slices.Contains(publisher.Needs, "sign-release") || publisher.Permissions["id-token"] != "" || publisher.Permissions["contents"] != "read" || signer.Permissions["contents"] != "read" {
		t.Fatal("signing/publication authority is not separated")
	}

	find := func(steps []githubReleaseStep, name string) githubReleaseStep {
		t.Helper()

		for _, step := range steps {
			if step.Name == name {
				return step
			}
		}

		t.Fatalf("workflow step missing: %s", name)

		return githubReleaseStep{}
	}

	for _, step := range publisher.Steps {
		if strings.Contains(step.Run, "forge-ci release sign") {
			t.Fatal("publisher retry would re-sign artifacts")
		}

		for _, value := range step.Env {
			if strings.Contains(value, "secrets.RELEASE_GPG_") || strings.Contains(value, "secrets.RELEASE_COSIGN_") {
				t.Fatal("publisher receives signing material")
			}
		}
	}

	for _, step := range signer.Steps {
		for _, value := range step.Env {
			if strings.Contains(value, "secrets.RELEASE_TOKEN") {
				t.Fatal("signer receives publication authority")
			}
		}
	}

	stage := find(signer.Steps, "Stage signed release payload")
	verify := find(publisher.Steps, "Verify signed release payload")
	download := find(publisher.Steps, "Download signed release payload")
	lease := find(publisher.Steps, "Revalidate release tag before publication")
	publish := find(publisher.Steps, "Create GitHub Release")

	if signer.Outputs["digest"] != "${{ steps.signed-state.outputs.digest }}" || verify.Env["EXPECTED_DIGEST"] != "${{ needs.sign-release.outputs.digest }}" || download.Env["ARTIFACT_NAME"] != "${{ needs.sign-release.outputs.artifact-name }}" {
		t.Fatal("handoff is not bound to signer outputs")
	}

	if lease.Env["RELEASE_SHA"] != "${{ needs.assemble-release.outputs.release-sha }}" || publish.WorkingDirectory != "signed-release" {
		t.Fatal("publisher is not bound to prepared source and verified payload")
	}

	for _, guard := range []githubReleaseStep{verify, lease} {
		if guard.ContinueOnError != nil && guard.ContinueOnError != false {
			t.Fatal("publication guard ignores failure")
		}
	}

	positions := map[string]int{}
	for index, step := range publisher.Steps {
		positions[step.Name] = index
	}

	if positions[verify.Name] >= positions[publish.Name] || positions[lease.Name] >= positions[publish.Name] || publish.If != "" {
		t.Fatal("publication can bypass integrity or source verification")
	}

	upload := find(signer.Steps, "Upload signed release payload")
	if upload.With["include-hidden-files"] != true {
		t.Fatal("signed assembly metadata would be omitted from upload")
	}

	engine := forgeCIEngine(t)
	signed := newSandbox(t, "github-sign")
	signed.write("bin/forge-ci", "#!/bin/sh\nexec \"${FORGE_CI_ENGINE:?}\" \"$@\"\n", 0o700)
	signed.write(".forge-ci/release-assembly.json", `{"version":1}`, 0o600)
	signed.write("release-files/app.tgz", "artifact", 0o600)
	signed.write("release-files/app.tgz.bundle", "original-signature-material", 0o600)
	signed.write("release-notes.md", "release notes", 0o600)
	signed.write("home/signing.key", "private material", 0o600)

	env := []string{"PATH=" + filepath.Join(signed.root, "bin") + ":" + os.Getenv("PATH"), "FORGE_CI_ENGINE=" + engine, "GITHUB_OUTPUT=" + signed.outputs, "GITHUB_RUN_ID=42", "GITHUB_RUN_ATTEMPT=1"}
	if got := signed.run(stage.Run, env...); got.status != 0 {
		t.Fatalf("staging failed: %s", got.stderr)
	}

	outputs := map[string]string{}

	for line := range strings.SplitSeq(signed.output(), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			outputs[key] = value
		}
	}

	if outputs["digest"] == "" || outputs["artifact-name"] != "release-signed-42-1" {
		t.Fatalf("missing signed checkpoint identity: %v", outputs)
	}

	if _, err := os.Stat(filepath.Join(signed.root, "signed-release/home/signing.key")); !os.IsNotExist(err) {
		t.Fatal("signing key entered release payload")
	}

	published := newSandbox(t, "github-publish")
	published.write("bin/forge-ci", "#!/bin/sh\nexec \"${FORGE_CI_ENGINE:?}\" \"$@\"\n", 0o700)

	for _, path := range []string{".forge-ci/release-assembly.json", "release-files/app.tgz", "release-files/app.tgz.bundle", "release-notes.md"} {
		body, err := os.ReadFile(filepath.Join(signed.root, "signed-release", path))
		if err != nil {
			t.Fatal(err)
		}

		published.write("signed-release/"+path, string(body), 0o600)
	}

	pubEnv := []string{"PATH=" + filepath.Join(published.root, "bin") + ":" + os.Getenv("PATH"), "FORGE_CI_ENGINE=" + engine, "EXPECTED_DIGEST=" + outputs["digest"], "GITHUB_RUN_ATTEMPT=2"}
	for range 2 {
		if got := published.run(verify.Run, pubEnv...); got.status != 0 {
			t.Fatalf("publisher retry did not reuse signed bytes: %s", got.stderr)
		}
	}

	published.write("signed-release/release-files/app.tgz.bundle", "new-signature-material", 0o600)

	if got := published.run(verify.Run, pubEnv...); got.status == 0 {
		t.Fatal("replaced signature passed publication boundary")
	}
}
