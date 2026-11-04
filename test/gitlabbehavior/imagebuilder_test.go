// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabbehavior

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// The image stage's settings are the policy's inputs, forwarded unchanged.
// Buildah stays the default; kaniko's job image is an input of its own, empty
// by default, and no image job exists unless release-images is set.
func TestReleaseImageStageTakesItsSettingsFromThePolicy(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"templates/release.yml", "templates/release-jobs.yml"} {
		header, _ := releaseRouting(t, path)
		inputs := header["spec"].(map[string]any)["inputs"].(map[string]any)

		builder := inputs["image-builder"].(map[string]any)
		if builder["default"] != "buildah" || !reflect.DeepEqual(builder["options"], []any{"buildah", "kaniko"}) {
			t.Fatalf("%s image-builder must offer buildah, the default, and kaniko: %+v", path, builder)
		}

		image := inputs["builder-job-image"].(map[string]any)
		if image["default"] != "" || image["regex"] != `^$|^[^[:space:]]+@sha256:[0-9a-f]{64}$` {
			t.Fatalf("%s builder-job-image must be empty or pinned by digest: %+v", path, image)
		}

		images := inputs["release-images"].(map[string]any)
		if images["type"] != "boolean" || images["default"] != false {
			t.Fatalf("%s release-images must be an opt-in boolean: %+v", path, images)
		}

		arm64 := inputs["build-runner-tags-arm64"].(map[string]any)
		if arm64["type"] != "array" || !reflect.DeepEqual(arm64["default"], []any{}) {
			t.Fatalf("%s build-runner-tags-arm64 must default to no arm64 runner: %+v", path, arm64)
		}
	}

	root := releaseDocumentBody(t, "templates/release.yml")
	forwarded := root["release"].(map[string]any)["trigger"].(map[string]any)["include"].([]any)[0].(map[string]any)["inputs"].(map[string]any)

	for _, name := range []string{"release-images", "artifacts-config", "release-platforms", "image-builder", "builder-job-image", "build-runner-tags-arm64"} {
		if forwarded[name] != "$[[ inputs."+name+" ]]" {
			t.Fatalf("root component does not forward %s", name)
		}
	}

	body := releaseDocumentBody(t, "templates/release-jobs.yml")

	// The builder reaches the image jobs as literal script arguments, which a
	// project variable cannot replace, and no job names it as a variable.
	for name, job := range body {
		fields, isJob := job.(map[string]any)
		if !isJob {
			continue
		}

		if variables, ok := fields["variables"].(map[string]any); ok && (variables["CI_IMAGE_BUILDER"] != nil || variables["CI_IMAGE_BUILDER_JOB_IMAGE"] != nil) {
			t.Errorf("%s names the image builder as a variable", name)
		}
	}

	build := body["Build: release files"].(map[string]any)
	if !reflect.DeepEqual(build["script"], []any{"rc_release_checkout", "$[[ inputs.build ]]", "rc_build_handoff"}) {
		t.Fatalf("the consumer build gained image work: %+v", build["script"])
	}

	for arch, script := range map[string]string{
		"amd64": "rc_images_build amd64 '$[[ inputs.image-builder ]]' '$[[ inputs.builder-job-image ]]'",
		"arm64": "rc_images_build arm64 '$[[ inputs.image-builder ]]' '$[[ inputs.builder-job-image ]]'",
	} {
		job := body["Images: build "+arch].(map[string]any)
		if !reflect.DeepEqual(job["script"], []any{script}) ||
			!reflect.DeepEqual(job["extends"], []any{".forge-ci-release-images", ".forge-ci-release-image-builder-$[[ inputs.image-builder ]]"}) {
			t.Fatalf("the %s image job does not take its builder from the policy: %+v", arch, job)
		}
	}

	for builder, image := range map[string]string{"buildah": "$[[ inputs.build-image ]]", "kaniko": "$[[ inputs.builder-job-image ]]"} {
		template := body[".forge-ci-release-image-builder-"+builder].(map[string]any)
		if template["image"].(map[string]any)["name"] != image {
			t.Fatalf("%s image builds run in %v, want %s", builder, template["image"], image)
		}
	}

	if _, tagged := body[".forge-ci-release-image-builder-kaniko"].(map[string]any)["tags"]; tagged {
		t.Fatal("kaniko builds need no tagged runner")
	}

	arm64Rule := body["Images: build arm64"].(map[string]any)["rules"].([]any)[0].(map[string]any)["if"]
	if arm64Rule != `'$[[ inputs.release-images ]]' == 'true' && '$[[ inputs.build-runner-tags-arm64 ]]' != '[]'` {
		t.Fatalf("the arm64 image job is not bound to its runner tags: %v", arm64Rule)
	}

	sign := body["Sign: files and images"].(map[string]any)
	if !reflect.DeepEqual(sign["script"], []any{"rc_sign_release '$[[ inputs.release-images ]]'"}) {
		t.Fatalf("signing does not read the policy's hand-off: %+v", sign["script"])
	}
}

// The release image builder is admitted before any Containerfile is built:
// Buildah is set up, kaniko admitted only as the kaniko job image of this
// engine project, read with the job token.
func TestReleaseImageBuilderAdmitsOnlyTheKanikoJobImage(t *testing.T) {
	t.Parallel()

	kanikoImage := "registry.invalid/team/kaniko-job@sha256:" + strings.Repeat("a", 64)
	kaniko := []string{"CI_IMAGE_BUILDER=kaniko", "CI_IMAGE_BUILDER_JOB_IMAGE=" + kanikoImage}

	for name, tc := range map[string]struct {
		env       []string
		usable    string
		admitted  bool
		calls     []string
		wantError string
	}{
		"buildah by default":        {admitted: true, calls: []string{"container setup-buildah"}},
		"buildah named":             {env: []string{"CI_IMAGE_BUILDER=buildah"}, admitted: true, calls: []string{"container setup-buildah"}},
		"another builder":           {env: []string{"CI_IMAGE_BUILDER=docker"}, wantError: "image-builder must be buildah or kaniko"},
		"kaniko without its image":  {env: []string{"CI_IMAGE_BUILDER=kaniko"}, wantError: "needs builder-job-image"},
		"kaniko by tag":             {env: []string{"CI_IMAGE_BUILDER=kaniko", "CI_IMAGE_BUILDER_JOB_IMAGE=registry.invalid/team/kaniko-job:latest"}, wantError: "needs builder-job-image"},
		"kaniko job image admitted": {env: kaniko, usable: "0", admitted: true, calls: kanikoAdmission(kanikoImage)},
		"kaniko job image refused":  {env: kaniko, usable: "1", calls: kanikoAdmission(kanikoImage), wantError: "is not a kaniko job image built from https://gitlab.invalid/team/engine for amd64"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			work := t.TempDir()
			script := filepath.Join(reporoot.Path(t), ".gitlab", "bin", "release.sh")
			// The fake engine records its arguments and stdin, and fails the
			// admission when told to.
			command := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-euo", "pipefail", "-c",
				`source "$1"; rc_engine() { printf '%s\n' "$*" >>"${RC_WORK}/engine"; if [ "$2" = login ]; then cat >>"${RC_WORK}/engine"; echo >>"${RC_WORK}/engine"; fi; if [ "$2" = image ]; then return "${USABLE_STATUS}"; fi; }; rc_image_builder`,
				"image-builder", script)

			command.Env = append([]string{"PATH=/usr/bin:/bin", "RC_WORK=" + work, "HELPERS_ENGINE_ARCH=amd64", "USABLE_STATUS=" + tc.usable,
				"FORGE_CI_REPOSITORY_URL=https://gitlab.invalid/team/engine.git", "CI_REGISTRY=registry.invalid", "CI_JOB_TOKEN=job-token-canary"}, tc.env...)

			output, err := command.CombinedOutput()
			if (err == nil) != tc.admitted || !strings.Contains(string(output), tc.wantError) {
				t.Fatalf("admitted=%t, want %t:\n%s", err == nil, tc.admitted, output)
			}

			calls, readErr := os.ReadFile(filepath.Join(work, "engine"))
			if (readErr == nil) != (len(tc.calls) != 0) {
				t.Fatalf("engine called=%t, want %t", readErr == nil, len(tc.calls) != 0)
			}

			for _, want := range tc.calls {
				if !strings.Contains(string(calls), want) {
					t.Fatalf("the admission did not run %q:\n%s", want, calls)
				}
			}
		})
	}
}

func kanikoAdmission(image string) []string {
	return []string{
		"container login --registry registry.invalid --registry-username gitlab-ci-token --registry-password-file - --auth-file ",
		"/builder-auth.json\njob-token-canary\n",
		"container image usable-digest --ref " + image + " --arch amd64 --auth-file ",
		"--require-label org.opencontainers.image.source=https://gitlab.invalid/team/engine --require-label org.opencontainers.image.title=forge-ci kaniko job image\n",
	}
}

// A consumer build no longer pushes or records release images: a ledger in
// its dist stops the hand-off before any engine call, and image-context is
// handed to the image jobs beside dist.
func TestReleaseBuildHandoffRefusesAConsumerLedger(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ ledger, planted bool }{
		"consumer ledger":         {ledger: true},
		"no ledger":               {},
		"hand-off already exists": {planted: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			work := t.TempDir()
			for _, dir := range []string{"dist", "image-context/target", "project"} {
				if err := os.MkdirAll(filepath.Join(work, dir), 0o700); err != nil {
					t.Fatal(err)
				}
			}

			if err := os.WriteFile(filepath.Join(work, "image-context/target/app.jar"), []byte("jar"), 0o600); err != nil {
				t.Fatal(err)
			}

			if tc.ledger {
				if err := os.WriteFile(filepath.Join(work, "dist/release-images.json"), []byte("[]"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// The consumer's build runs in this job and could leave the
			// hand-off with its own files in it, to be merged into.
			if tc.planted {
				if err := os.MkdirAll(filepath.Join(work, "project/_release-build/dist"), 0o700); err != nil {
					t.Fatal(err)
				}
			}

			output, err := runReleaseFunction(t, work, `rc_build_handoff`, "CI_PROJECT_DIR="+filepath.Join(work, "project"), "RC_BASE_IMAGES=false")
			if tc.planted {
				if err == nil || !strings.Contains(output, "reserved for this job") {
					t.Fatalf("a planted hand-off was merged into: %v\n%s", err, output)
				}

				return
			}

			if tc.ledger {
				if err == nil || !strings.Contains(output, "written by the image jobs") || strings.Contains(output, "engine:") {
					t.Fatalf("a consumer ledger was handed off: %v\n%s", err, output)
				}

				return
			}

			if err != nil {
				t.Fatalf("hand-off refused: %v\n%s", err, output)
			}

			if _, err := os.Stat(filepath.Join(work, "project/_release-build/image-context/target/app.jar")); err != nil {
				t.Fatalf("image-context was not handed off: %v", err)
			}
		})
	}
}

// An image build job reads the release commit's time before the build's files
// are laid over the checkout, gives the build none of the job's credentials,
// and hands off its layouts readable without after_script, which a kaniko job
// may have no shell left for.
func TestReleaseImageBuildHandsOffReadableLayouts(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	project := filepath.Join(work, "project")

	if err := os.MkdirAll(filepath.Join(project, "_release-build/image-context"), 0o700); err != nil {
		t.Fatal(err)
	}

	body := `rc_image_builder() { :; }; rc_release_checkout() { mkdir -p "${RC_WORK}/consumer"; cd "${RC_WORK}/consumer"; }; ` +
		`git() { echo "git: $1" >&2; }; cp() { echo "cp: $*"; command cp "$@"; }; ` +
		`rc_engine() { echo "engine: $3 token=${CI_JOB_TOKEN-unset} proxy=${CI_DEPENDENCY_PROXY_PASSWORD-unset}"; ` +
		`mkdir -p staged/app-arch-amd64/layout; touch staged/app-arch-amd64/app-amd64.json; }; ` +
		`umask 077; rc_images_build amd64 buildah ''`

	output, err := runReleaseFunction(t, work, body, "RC_WORK="+work, "CI_PROJECT_DIR="+project,
		"CI_JOB_TOKEN=job-token", "CI_DEPENDENCY_PROXY_PASSWORD=proxy-password", "CI_REGISTRY_IMAGE=registry.example.com/group/app",
		"RELEASE_TAG=v1.2.3", "RELEASE_SHA="+strings.Repeat("a", 40), "CI_SERVER_URL=https://gitlab.example.com", "CI_PROJECT_PATH=group/app")
	if err != nil {
		t.Fatalf("image build failed: %v\n%s", err, output)
	}

	if show, overlay := strings.Index(output, "git: show"), strings.Index(output, "cp: -R -- "+project+"/_release-build/image-context/."); show < 0 || overlay < 0 || show > overlay {
		t.Fatalf("the created label must be read from the release commit before the overlay:\n%s", output)
	}

	if !strings.Contains(output, "engine: build token=unset proxy=unset") {
		t.Fatalf("the build was given a job credential:\n%s", output)
	}

	handoff := filepath.Join(project, "_release-images-amd64")
	requireMode(t, handoff, 0o755)
	requireMode(t, filepath.Join(handoff, "app-arch-amd64/layout"), 0o755)
	requireMode(t, filepath.Join(handoff, "app-arch-amd64/app-amd64.json"), 0o644)
}

// Signing reads the hand-off the policy names and holds it to it: with
// release images the publishing job's, which must carry the ledger; without,
// the build's, which must not.
func TestReleaseSigningReadsThePolicysHandoff(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		images, handoff string
		ledger, signed  bool
	}{
		"images with their ledger": {images: "true", handoff: "_release-images", ledger: true, signed: true},
		"images without a ledger":  {images: "true", handoff: "_release-images"},
		"no images":                {images: "false", handoff: "_release-build", signed: true},
		"no images but a ledger":   {images: "false", handoff: "_release-build", ledger: true},
		"an unknown setting":       {images: "maybe"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			work := t.TempDir()
			ledger := ""

			if tc.ledger {
				ledger = "touch dist/release-images.json; "
			}

			// The checks the signer runs before reading are stubbed; the first
			// engine call after the read ends the run successfully.
			body := `rc_signing_preflight() { :; }; rc_release_checkout() { :; }; ` +
				`rc_read_handoff() { echo "read: $1"; mkdir dist; ` + ledger + `export DIST_DIR=dist RELEASE_FILES_MANIFEST=dist/release-files.json; }; ` +
				`rc_engine() { echo "engine: $*"; exit 0; }; rc_sign_release ` + tc.images

			output, err := runReleaseFunction(t, work, body, "CI_PROJECT_DIR=/project")
			if (err == nil) != tc.signed {
				t.Fatalf("signed=%t, want %t:\n%s", err == nil, tc.signed, output)
			}

			if tc.handoff != "" && !strings.Contains(output, "read: /project/"+tc.handoff+"\n") {
				t.Fatalf("signing read another hand-off than %s:\n%s", tc.handoff, output)
			}
		})
	}
}

// runReleaseFunction runs body after sourcing release.sh in dir, with an
// engine that records its calls and succeeds unless body replaces it.
func runReleaseFunction(t *testing.T, dir, body string, env ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	script := filepath.Join(reporoot.Path(t), ".gitlab", "bin", "release.sh")
	command := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-euo", "pipefail", "-c",
		`source "$1"; rc_engine() { echo "engine: $*"; }; `+body, "release-function", script)
	command.Dir = dir
	command.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + dir}, env...)

	output, err := command.CombinedOutput()

	return string(output), err
}

// releaseDocumentBody decodes the jobs document after a component's spec.
func releaseDocumentBody(t *testing.T, path string) map[string]any {
	t.Helper()

	decoder := yaml.NewDecoder(bytes.NewReader(reporoot.ReadFile(t, path)))

	var header, body map[string]any
	if err := decoder.Decode(&header); err != nil {
		t.Fatal(err)
	}

	if err := decoder.Decode(&body); err != nil {
		t.Fatal(err)
	}

	return body
}
