// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package bootstrap

import (
	"os"
	"regexp"
	"testing"

	"github.com/diggsweden/forge-ci/internal/app/snapshotimage"
)

// A Forgejo build job installs the Build SBOM generator from these scripts;
// GitHub and GitLab jobs run it from the runtime image. One forge-ci release
// must write its SBOMs with one version of each generator wherever it runs.
func TestSBOMGeneratorPinsMatchTheRuntimeImage(t *testing.T) {
	t.Parallel()

	containerfile, err := os.ReadFile("../../containers/runtime/Containerfile")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ script, scriptVar, imageArg string }{
		{"install-cyclonedx-gomod.sh", "CYCLONEDX_GOMOD_VERSION", "CYCLONEDX_GOMOD_VERSION"},
		{"install-cargo-cyclonedx.sh", "CARGO_CYCLONEDX_VERSION", "CARGO_CYCLONEDX_VERSION"},
	} {
		script, err := os.ReadFile(tc.script)
		if err != nil {
			t.Fatal(err)
		}

		pinned := regexp.MustCompile(`(?m)^readonly ` + tc.scriptVar + `="([^"]+)"$`).FindSubmatch(script)
		image := regexp.MustCompile(`(?m)^ARG ` + tc.imageArg + `=(\S+)$`).FindSubmatch(containerfile)

		if pinned == nil || image == nil {
			t.Fatalf("%s: pin not found (script %q, image %q)", tc.script, pinned, image)
		}

		if string(pinned[1]) != string(image[1]) {
			t.Errorf("%s pins %s, the runtime image %s", tc.script, pinned[1], image[1])
		}
	}
}

// A snapshot image's build writes its SBOM with the generator version the
// release build of the same project pins on GitHub and GitLab.
func TestSnapshotSBOMGeneratorPinsMatchTheReleaseBuilds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ file, pin, want string }{
		{"../../.github/workflows/build-gradle-app.yml", `CYCLONEDX_GRADLE_VERSION: "([^"]+)"`, snapshotimage.CycloneDXGradleVersion},
		{"../../templates/build-gradle.yml", `sbom-tool-version:\s+# renovate:[^\n]*\n\s+default: "([^"]+)"`, snapshotimage.CycloneDXGradleVersion},
		{"../../.github/workflows/build-npm.yml", `CYCLONEDX_VERSION: "([^"]+)"`, snapshotimage.CycloneDXNPMVersion},
		{"../../templates/build-npm.yml", `sbom-tool-version:\s+# renovate:[^\n]*\n\s+default: "([^"]+)"`, snapshotimage.CycloneDXNPMVersion},
		{"../../.github/workflows/build-maven.yml", `CYCLONEDX_MAVEN_VERSION: "([^"]+)"`, snapshotimage.CycloneDXMavenVersion},
		{"../../templates/build-maven.yml", `sbom-tool-version:\s+# renovate:[^\n]*\n\s+default: "([^"]+)"`, snapshotimage.CycloneDXMavenVersion},
	} {
		body, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}

		pinned := regexp.MustCompile(tc.pin).FindSubmatch(body)
		if pinned == nil {
			t.Fatalf("%s: pin %s not found", tc.file, tc.pin)
		}

		if string(pinned[1]) != tc.want {
			t.Errorf("%s pins %s, the snapshot build %s", tc.file, pinned[1], tc.want)
		}
	}
}
