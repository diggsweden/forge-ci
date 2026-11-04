// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package bootstrap

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/testutil/evidence"
)

func runSourcedScript(t *testing.T, script, body string, env map[string]string) (string, string, error) {
	t.Helper()
	root := t.TempDir()

	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	// Only file utilities and explicitly supplied fixture binaries are visible;
	// no ambient installer, credentials, or shell initialization is inherited.
	for _, tool := range []string{"bash", "basename", "dirname", "uname", "mktemp", "mkdir", "rm", "cp", "mv", "chmod", "install", "tar", "gzip", "unzip", "sha256sum", "shasum", "awk", "cut", "tr", "grep", "head", "cat", "ldd"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			continue
		}

		if err := os.Symlink(path, filepath.Join(tools, tool)); err != nil {
			t.Fatal(err)
		}
	}

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}

	if writeErr := os.WriteFile(filepath.Join(tools, "curl"), []byte("#!/usr/bin/env bash\nprintf 'unexpected download attempt\\n' >&2\nexit 97\n"), 0o700); writeErr != nil {
		t.Fatal(writeErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bash, "-c", "source \"$1\"; "+body, "_", script)
	cmd.WaitDelay = time.Second
	vars := map[string]string{"HOME": root, "TMPDIR": root, "CI_TEMP_DIR": root, "LC_ALL": "C", "PATH": tools}

	for key, value := range env {
		if key == "PATH" {
			value += string(os.PathListSeparator) + tools
		}

		vars[key] = value
	}

	for key, value := range vars {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	return stdout.String(), stderr.String(), err
}

func TestVerifiedDownloadRejectsIntegrityFailure(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "upstream")
	if err := os.WriteFile(fixture, []byte("tampered archive\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "download")
	body := `curl() { cp "$FIXTURE" "${*: -1}"; }; ci_download_verified https://example.invalid/tool.tar.gz "$DESTINATION" ` + strings.Repeat("0", 64)

	_, stderr, err := runSourcedScript(t, "install-common.sh", body, map[string]string{
		"FIXTURE":     fixture,
		"DESTINATION": destination,
	})
	if err == nil {
		t.Fatal("download with a mismatching pinned checksum succeeded")
	}

	if !strings.Contains(stderr, "SHA-256 mismatch") {
		t.Fatalf("stderr = %q, want SHA-256 mismatch", stderr)
	}

	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("rejected download was not removed: %v", statErr)
	}
}

func TestVerifiedDownloadRejectsMissingPinBeforeNetwork(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "download")
	marker := filepath.Join(t.TempDir(), "curl-called")
	body := `curl() { printf called >"$CURL_MARKER"; }; ci_download_verified https://example.invalid/tool "$DESTINATION" ""`

	_, stderr, err := runSourcedScript(t, "install-common.sh", body, map[string]string{
		"CURL_MARKER": marker,
		"DESTINATION": destination,
	})
	if err == nil || !strings.Contains(stderr, "pinned SHA-256 is required") {
		t.Fatalf("missing pin result: err=%v stderr=%q", err, stderr)
	}

	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("curl ran before pin validation: %v", statErr)
	}
}

// pinnedInstallerCase is one installer on one platform branch. An empty os
// and arch mean Linux x86_64.
type pinnedInstallerCase struct{ tool, url, pin, entry, os, arch string }

func TestPinnedInstallers_VerifyExactDownloadBeforeInstalling(t *testing.T) {
	t.Parallel()

	for _, tc := range []pinnedInstallerCase{
		{"trivy", "https://github.com/aquasecurity/trivy/releases/download/v0.76.1/trivy_0.76.1_Linux-64bit.tar.gz", "c7cbe3137fb8a48a17574bede3dd6beb16742d1e9772929940fc90082c39d9c0", "trivy", "", ""},
		{"syft", "https://github.com/anchore/syft/releases/download/v1.55.0/syft_1.55.0_linux_amd64.tar.gz", "bb65249542db5fedeeb4d3f9b018b75e7264dbfbbb10486f044581a92f90490d", "syft", "", ""},
		{"cargo-cyclonedx", "https://github.com/CycloneDX/cyclonedx-rust-cargo/releases/download/cargo-cyclonedx-0.5.9/cargo-cyclonedx-x86_64-unknown-linux-gnu.tar.xz", "fb8dbee9f182173e062a64a387b21a0badc6fab8b2abf9294973f012972bf6d8", "cargo-cyclonedx-x86_64-unknown-linux-gnu/cargo-cyclonedx", "", ""},
		{"cargo-cyclonedx", "https://github.com/CycloneDX/cyclonedx-rust-cargo/releases/download/cargo-cyclonedx-0.5.9/cargo-cyclonedx-aarch64-unknown-linux-gnu.tar.xz", "7bf131ca5389b07a4f10c182bcf8a5ad339d64408b6f0d8f6834a0bd6120a06a", "cargo-cyclonedx-aarch64-unknown-linux-gnu/cargo-cyclonedx", "Linux", "aarch64"},
		{"cargo-cyclonedx", "https://github.com/CycloneDX/cyclonedx-rust-cargo/releases/download/cargo-cyclonedx-0.5.9/cargo-cyclonedx-x86_64-apple-darwin.tar.xz", "59d2a583fa632f8759456c1b531340331255b277386d23c598a3dbbc916fde63", "cargo-cyclonedx-x86_64-apple-darwin/cargo-cyclonedx", "Darwin", "x86_64"},
		{"cargo-cyclonedx", "https://github.com/CycloneDX/cyclonedx-rust-cargo/releases/download/cargo-cyclonedx-0.5.9/cargo-cyclonedx-aarch64-apple-darwin.tar.xz", "4c53dfa21e70b65bf7f8d2592aadde3bcb02c1a40b6ec63b877e5ca65a29e180", "cargo-cyclonedx-aarch64-apple-darwin/cargo-cyclonedx", "Darwin", "arm64"},
		{"cyclonedx-gomod", "https://github.com/CycloneDX/cyclonedx-gomod/releases/download/v1.12.0/cyclonedx-gomod_1.12.0_linux_amd64.tar.gz", "004b9f5cc595b797fb5423e2ae4c97bcf0f18c712ed2faee1640b09e5efd6d15", "cyclonedx-gomod", "", ""},
		{"cyclonedx-gomod", "https://github.com/CycloneDX/cyclonedx-gomod/releases/download/v1.12.0/cyclonedx-gomod_1.12.0_linux_arm64.tar.gz", "b6dd6424755e61c0f7fcd36074fbb78798a33c52f1bd35dd177ba62eccadf404", "cyclonedx-gomod", "Linux", "aarch64"},
		{"cyclonedx-gomod", "https://github.com/CycloneDX/cyclonedx-gomod/releases/download/v1.12.0/cyclonedx-gomod_1.12.0_darwin_amd64.tar.gz", "94ab2d999341e4bc8d26767bffda2823ac4507c2f17c01af6d3738f5341b9e85", "cyclonedx-gomod", "Darwin", "x86_64"},
		{"cyclonedx-gomod", "https://github.com/CycloneDX/cyclonedx-gomod/releases/download/v1.12.0/cyclonedx-gomod_1.12.0_darwin_arm64.tar.gz", "43dcd58b7a7ef9d84a2d21df9c6eeeb907d5b4ee2b1fe6c1330e5bc9b478281e", "cyclonedx-gomod", "Darwin", "arm64"},
		{"cosign", "https://github.com/sigstore/cosign/releases/download/v3.1.4/cosign-linux-amd64", "424e2c97d0b45fd7a6b608a87f090f35633f4c23ece5f092d48bba11aaf4bbfa", "", "", ""},
		{"gh", "https://github.com/cli/cli/releases/download/v2.103.2/gh_2.103.2_linux_amd64.tar.gz", "62ccf747115ddc61237efffdde66a3c8f70ba249433dc64f70801089b59c59c8", "gh_2.103.2_linux_amd64/bin/gh", "", ""},
		{"glab", "https://gitlab.com/gitlab-org/cli/-/releases/v1.124.0/downloads/glab_1.124.0_linux_amd64.tar.gz", "63df684842b0697e418649dd64374d37ac1da0fbcdcb422d6a5557222f8bb40f", "root/bin/glab", "", ""},
		{"git-cliff", "https://github.com/orhun/git-cliff/releases/download/v2.14.2/git-cliff-2.14.2-x86_64-unknown-linux-gnu.tar.gz", "24f397c733add5390fdceee3a2088588ab0d5f944ce00d34cb7029b888cf2db4", "root/git-cliff", "", ""},
		{"yq", "https://github.com/mikefarah/yq/releases/download/v4.54.1/yq_linux_amd64", "8e34fc298390875de416e6a4afcb8cabeceb25d9aa8506c1a2f9353cf702ea5f", "", "", ""},
		{"mise", "https://github.com/jdx/mise/releases/download/v2026.10.19/mise-v2026.10.19-linux-x64", "3162760f40b6b5f706c60641e4ef5ee7322bc6c54f72fc26a7111b337aa18d9d", "", "", ""},
		{"publiccode-parser", "https://github.com/italia/publiccode-parser-go/releases/download/v5.4.3/publiccode-parser-go_Linux_x86_64.tar.gz", "211847305611417ac0b76af5169086232ea6c75ff6ae686227b6972a41599dd7", "publiccode-parser", "", ""},
		{"forgejo-runner", "https://code.forgejo.org/forgejo/runner/releases/download/v13.2.0/forgejo-runner-13.2.0-linux-amd64", "fadaec897f5e6641c363f87ecaf75f866f99c81b7097ec6b433e4c48123fcad1", "", "", ""},
		{"forgejo-runner", "https://code.forgejo.org/forgejo/runner/releases/download/v13.2.0/forgejo-runner-13.2.0-linux-arm64", "9d3d462ee83e4e629f959faeacceb20eed2f25290bddea402e65ace46a31afd9", "", "Linux", "aarch64"},
		{"trivy", "https://github.com/aquasecurity/trivy/releases/download/v0.76.1/trivy_0.76.1_Linux-ARM64.tar.gz", "e174dff2a6dba5e3ae41f8d0537a6324405bd08132497340304c6b20e0f16a1f", "trivy", "Linux", "aarch64"},
		{"trivy", "https://github.com/aquasecurity/trivy/releases/download/v0.76.1/trivy_0.76.1_macOS-ARM64.tar.gz", "11a9b6cd2752011b54f01ea4bd0dbfa9f0c41dc6538d5a3f9d23955f471bf4e5", "trivy", "Darwin", "arm64"},
		{"syft", "https://github.com/anchore/syft/releases/download/v1.55.0/syft_1.55.0_linux_arm64.tar.gz", "929bd163db2d7cd2255d3bcb8967208079813d7b46b0c952f7b794d8e2c7d304", "syft", "Linux", "aarch64"},
		{"syft", "https://github.com/anchore/syft/releases/download/v1.55.0/syft_1.55.0_darwin_amd64.tar.gz", "e2e5831cbabaec9a36fe65c34755c1113982c842a685edce3ce0b54a0ded8003", "syft", "Darwin", "x86_64"},
		{"syft", "https://github.com/anchore/syft/releases/download/v1.55.0/syft_1.55.0_darwin_arm64.tar.gz", "1217c258bd5eb91ef3189cf22a82e02f9cac07e4ce934350f428e93aff674625", "syft", "Darwin", "arm64"},
		{"cosign", "https://github.com/sigstore/cosign/releases/download/v3.1.4/cosign-linux-arm64", "ade83b7aac62e131cacfe96d958948b2150cc9efec46a570a2649b8e27dedad2", "", "Linux", "aarch64"},
		{"cosign", "https://github.com/sigstore/cosign/releases/download/v3.1.4/cosign-darwin-arm64", "9f9b8cd557cf2e5b3df3373a00baa005a86b02391e3e06dd0ce37b3a1533d3a4", "", "Darwin", "arm64"},
		{"gh", "https://github.com/cli/cli/releases/download/v2.103.2/gh_2.103.2_linux_arm64.tar.gz", "4c3e99a50bd8b181a3fe9d69b25dd978bac768148d88f01507c6b4d1ef61a443", "gh_2.103.2_linux_arm64/bin/gh", "Linux", "arm64"},
		{"glab", "https://gitlab.com/gitlab-org/cli/-/releases/v1.124.0/downloads/glab_1.124.0_linux_arm64.tar.gz", "0716395a0efeb991fc844b33fe55310645afcbd9b9746e50c725e9d1cd1b8d16", "root/bin/glab", "Linux", "aarch64"},
		{"glab", "https://gitlab.com/gitlab-org/cli/-/releases/v1.124.0/downloads/glab_1.124.0_darwin_arm64.tar.gz", "04aad4d989d98cb030318e09359f02e0ba2637ede682ec0dee1f5974dedd44c1", "root/bin/glab", "Darwin", "arm64"},
		{"git-cliff", "https://github.com/orhun/git-cliff/releases/download/v2.14.2/git-cliff-2.14.2-aarch64-unknown-linux-gnu.tar.gz", "69e208f6e4baef3a0f9b759d5f44223aa9336b86c691072a3d0c98d35a816e69", "root/git-cliff", "Linux", "aarch64"},
		{"git-cliff", "https://github.com/orhun/git-cliff/releases/download/v2.14.2/git-cliff-2.14.2-x86_64-apple-darwin.tar.gz", "4e647d2deb152aca1e987f225cf42b080b991dd98f3bce4027ef95446d982ac3", "root/git-cliff", "Darwin", "amd64"},
		{"yq", "https://github.com/mikefarah/yq/releases/download/v4.54.1/yq_linux_arm", "a5d45c07b75fab772193c4a79f4df9f5c8ed33836fa0f36dcc947f9495439b00", "", "Linux", "armv7l"},
		{"yq", "https://github.com/mikefarah/yq/releases/download/v4.54.1/yq_darwin_arm64", "fee511a181bd8b3e6b7da98842b41973bfac8cd3bcd341ed29a584620b5b2844", "", "Darwin", "arm64"},
		{"mise", "https://github.com/jdx/mise/releases/download/v2026.10.19/mise-v2026.10.19-linux-arm64", "4a4ca53b28e5433c527f4f73fac1f98f992a17cf32ca136a58e2e375f790f627", "", "Linux", "aarch64"},
		{"mise", "https://github.com/jdx/mise/releases/download/v2026.10.19/mise-v2026.10.19-macos-x64", "8d5e1591811143f15334f690373cefb58c13bd6cbd0292008748fd777f6c99d8", "", "Darwin", "x86_64"},
		{"publiccode-parser", "https://github.com/italia/publiccode-parser-go/releases/download/v5.4.3/publiccode-parser-go_Linux_arm64.tar.gz", "33363da55ddf38457f6f9081cc8f3b833e833eb3a401d6c9c3feb8e5f2dc3fd7", "publiccode-parser", "Linux", "aarch64"},
		{"publiccode-parser", "https://github.com/italia/publiccode-parser-go/releases/download/v5.4.3/publiccode-parser-go_Darwin_arm64.tar.gz", "61b3754455bfa5c04fe766d549e8b1d01fe804f7d914c93ee19e3ea28e6a46ae", "publiccode-parser", "Darwin", "arm64"},
	} {
		if tc.os == "" {
			tc.os, tc.arch = "Linux", "x86_64"
		}

		t.Run(strings.Join([]string{tc.tool, tc.os, tc.arch}, "/"), func(t *testing.T) {
			t.Parallel()

			for _, mode := range []installerMode{installMatching, installTampered, installDownloadFails} {
				t.Run(string(mode), func(t *testing.T) {
					checkPinnedInstaller(t, tc, mode)
				})
			}
		})
	}
}

type installerMode string

const (
	installMatching      installerMode = "matching checksum"
	installTampered      installerMode = "different checksum"
	installDownloadFails installerMode = "download fails midway"
)

// requireInstallerFailureCleansUp requires a refused install to report its
// reason and to leave nothing in the tool's directory: no installed binary,
// partial download, archive or extraction.
func requireInstallerFailureCleansUp(t *testing.T, mode installerMode, runErr error, stderr, installDir string) {
	t.Helper()

	// Trivy and Syft report a failed download as a failed install.
	want := "SHA-256 mismatch"
	if mode == installDownloadFails {
		want = "ERROR: Failed to "
	}

	if runErr == nil || !strings.Contains(stderr, want) {
		t.Errorf("%s: err=%v stderr=%q, want %q", mode, runErr, stderr, want)
	}

	if entries, readErr := os.ReadDir(installDir); readErr != nil && !errors.Is(readErr, os.ErrNotExist) || len(entries) != 0 {
		t.Errorf("%s left %v in %s (read error %v)", mode, entries, installDir, readErr)
	}
}

func checkPinnedInstaller(t *testing.T, tc pinnedInstallerCase, mode installerMode) {
	t.Helper()
	root, scriptDir, binDir := t.TempDir(), t.TempDir(), t.TempDir()
	fixture := filepath.Join(root, "download")
	binary := []byte("#!/usr/bin/env bash\nprintf 'GitVersion: v3.1.4\\n{\"version\":\"fixture\"}\\n'\n")
	writeFixture := func(body []byte) {
		if tc.entry != "" {
			writeInstallerTarball(t, fixture, tc.entry, body)
		} else if err := os.WriteFile(fixture, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(binary)

	pin := sha256sumFile(t, fixture)
	script := "install-" + tc.tool + ".sh"

	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Count(string(body), tc.pin) != 1 {
		t.Fatal("reviewed platform pin missing or ambiguous")
	}
	// Substitute only the reviewed literal pin in this owned copy. The real
	// download/checksum code must still validate the fixture bytes.
	body = []byte(strings.Replace(string(body), tc.pin, pin, 1))
	if writeErr := os.WriteFile(filepath.Join(scriptDir, script), body, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	common, err := os.ReadFile("install-common.sh")
	if err != nil {
		t.Fatal(err)
	}

	if writeErr := os.WriteFile(filepath.Join(scriptDir, "install-common.sh"), common, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	if mode == installTampered {
		writeFixture(append(bytes.Clone(binary), []byte("# changed\n")...))
	}

	writeFakeCurl(t, binDir)

	ambientMarker := filepath.Join(root, "ambient-called")

	if tc.tool == "cosign" || tc.tool == "syft" {
		ambient := "#!/usr/bin/env bash\nprintf called > \"$AMBIENT_MARKER\"\nprintf 'GitVersion: v3.1.4\\n'\n"
		if writeErr := os.WriteFile(filepath.Join(binDir, tc.tool), []byte(ambient), 0o700); writeErr != nil {
			t.Fatal(writeErr)
		}
	}

	command := `uname() { case "$1" in -s) printf ` + tc.os + ` ;; -m) printf ` + tc.arch + ` ;; esac; }; install_` + strings.ReplaceAll(tc.tool, "-", "_")

	failDownload := ""
	if mode == installDownloadFails {
		failDownload = "1"
	}

	_, stderr, runErr := runSourcedScript(t, filepath.Join(scriptDir, script), command, map[string]string{
		"PATH": binDir, "CI_TEMP_DIR": root, "OS": tc.os, "ARCH": tc.arch,
		"INSTALLER_FIXTURE": fixture, "EXPECTED_DOWNLOAD_URL": tc.url, "AMBIENT_MARKER": ambientMarker,
		"FAKE_CURL_FAIL_AFTER_PARTIAL": failDownload,
	})
	if _, statErr := os.Stat(ambientMarker); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("ambient %s executed: %v", tc.tool, statErr)
	}

	installDir := filepath.Join(root, tc.tool+"-bin")
	installed := filepath.Join(installDir, tc.tool)

	if mode != installMatching {
		requireInstallerFailureCleansUp(t, mode, runErr, stderr, installDir)

		return
	}

	if runErr != nil {
		t.Fatalf("installation failed: %v stderr=%s", runErr, stderr)
	}

	got, err := os.ReadFile(installed)
	if err != nil || !bytes.Equal(got, binary) {
		t.Errorf("installed bytes differ: %v", err)
	}

	if tc.tool == "git-cliff" {
		archives, globErr := filepath.Glob(filepath.Join(filepath.Dir(installed), "*.tar.gz"))
		if globErr != nil || len(archives) != 0 {
			t.Errorf("git-cliff archive cleanup failed: %v %v", archives, globErr)
		}
	}
}

func TestRuntimeInstallerHashesMatchExactAssets(t *testing.T) {
	tests := []struct {
		name   string
		script string
		body   string
		want   string
	}{
		{name: "cosign linux amd64", script: "install-cosign.sh", body: `resolve_cosign_sha256 cosign-linux-amd64`, want: "424e2c97d0b45fd7a6b608a87f090f35633f4c23ece5f092d48bba11aaf4bbfa"},
		{name: "cosign linux arm64", script: "install-cosign.sh", body: `resolve_cosign_sha256 cosign-linux-arm64`, want: "ade83b7aac62e131cacfe96d958948b2150cc9efec46a570a2649b8e27dedad2"},
		{name: "cosign darwin amd64", script: "install-cosign.sh", body: `resolve_cosign_sha256 cosign-darwin-amd64`, want: "9d1e4abe2b6be279ed0d2ff706fde1ccd738747c834e3e6d44039c238867334f"},
		{name: "cosign darwin arm64", script: "install-cosign.sh", body: `resolve_cosign_sha256 cosign-darwin-arm64`, want: "9f9b8cd557cf2e5b3df3373a00baa005a86b02391e3e06dd0ce37b3a1533d3a4"},
		{name: "gh amd64", script: "install-gh.sh", body: `resolve_gh_sha256 gh_2.103.2_linux_amd64.tar.gz`, want: "62ccf747115ddc61237efffdde66a3c8f70ba249433dc64f70801089b59c59c8"},
		{name: "gh arm64", script: "install-gh.sh", body: `resolve_gh_sha256 gh_2.103.2_linux_arm64.tar.gz`, want: "4c3e99a50bd8b181a3fe9d69b25dd978bac768148d88f01507c6b4d1ef61a443"},
		{name: "gh darwin amd64", script: "install-gh.sh", body: `resolve_gh_sha256 gh_2.103.2_macOS_amd64.zip`, want: "2dc7f1fc4761f96732995f6e4f4c6b4d705f2635da4126620c8b30eaa67b9c98"},
		{name: "gh darwin arm64", script: "install-gh.sh", body: `resolve_gh_sha256 gh_2.103.2_macOS_arm64.zip`, want: "5416000f5d17580a187339e0c4068a01ca5c87f7d4fe6ca64933696f967bc418"},
		{name: "glab amd64", script: "install-glab.sh", body: `resolve_glab_sha256 glab_1.124.0_linux_amd64.tar.gz`, want: "63df684842b0697e418649dd64374d37ac1da0fbcdcb422d6a5557222f8bb40f"},
		{name: "glab arm64", script: "install-glab.sh", body: `resolve_glab_sha256 glab_1.124.0_linux_arm64.tar.gz`, want: "0716395a0efeb991fc844b33fe55310645afcbd9b9746e50c725e9d1cd1b8d16"},
		{name: "glab darwin amd64", script: "install-glab.sh", body: `resolve_glab_sha256 glab_1.124.0_darwin_amd64.tar.gz`, want: "6adb7155566bedd6272d8f286e6c75b080d0050a349de4bed79661226d10d954"},
		{name: "glab darwin arm64", script: "install-glab.sh", body: `resolve_glab_sha256 glab_1.124.0_darwin_arm64.tar.gz`, want: "04aad4d989d98cb030318e09359f02e0ba2637ede682ec0dee1f5974dedd44c1"},
		{name: "git-cliff amd64", script: "install-git-cliff.sh", body: `resolve_git_cliff_sha256 git-cliff-2.14.2-x86_64-unknown-linux-gnu.tar.gz`, want: "24f397c733add5390fdceee3a2088588ab0d5f944ce00d34cb7029b888cf2db4"},
		{name: "git-cliff arm64", script: "install-git-cliff.sh", body: `resolve_git_cliff_sha256 git-cliff-2.14.2-aarch64-unknown-linux-gnu.tar.gz`, want: "69e208f6e4baef3a0f9b759d5f44223aa9336b86c691072a3d0c98d35a816e69"},
		{name: "git-cliff darwin amd64", script: "install-git-cliff.sh", body: `resolve_git_cliff_sha256 git-cliff-2.14.2-x86_64-apple-darwin.tar.gz`, want: "4e647d2deb152aca1e987f225cf42b080b991dd98f3bce4027ef95446d982ac3"},
		{name: "git-cliff darwin arm64", script: "install-git-cliff.sh", body: `resolve_git_cliff_sha256 git-cliff-2.14.2-aarch64-apple-darwin.tar.gz`, want: "a0be97e237d440d34c5e508ec21d070b2c85cbb8be48a46a56f9a4e8bd247f05"},
		{name: "yq amd64", script: "install-yq.sh", body: `resolve_yq_sha256 yq_linux_amd64`, want: "8e34fc298390875de416e6a4afcb8cabeceb25d9aa8506c1a2f9353cf702ea5f"},
		{name: "yq arm64", script: "install-yq.sh", body: `resolve_yq_sha256 yq_linux_arm64`, want: "189088da0c6429ec5178dfaab1a114805f6cab0b61b165ab236efedf1d57a71b"},
		{name: "yq arm", script: "install-yq.sh", body: `resolve_yq_sha256 yq_linux_arm`, want: "a5d45c07b75fab772193c4a79f4df9f5c8ed33836fa0f36dcc947f9495439b00"},
		{name: "yq darwin amd64", script: "install-yq.sh", body: `resolve_yq_sha256 yq_darwin_amd64`, want: "3a812fce205a4d67014fb71b7fa297092210e580df3d8f52dddc2cf3a599c22b"},
		{name: "yq darwin arm64", script: "install-yq.sh", body: `resolve_yq_sha256 yq_darwin_arm64`, want: "fee511a181bd8b3e6b7da98842b41973bfac8cd3bcd341ed29a584620b5b2844"},
		{name: "mise amd64", script: "install-mise.sh", body: `resolve_mise_sha256 mise-v2026.10.19-linux-x64`, want: "3162760f40b6b5f706c60641e4ef5ee7322bc6c54f72fc26a7111b337aa18d9d"},
		{name: "mise arm64", script: "install-mise.sh", body: `resolve_mise_sha256 mise-v2026.10.19-linux-arm64`, want: "4a4ca53b28e5433c527f4f73fac1f98f992a17cf32ca136a58e2e375f790f627"},
		{name: "mise darwin amd64", script: "install-mise.sh", body: `resolve_mise_sha256 mise-v2026.10.19-macos-x64`, want: "8d5e1591811143f15334f690373cefb58c13bd6cbd0292008748fd777f6c99d8"},
		{name: "mise darwin arm64", script: "install-mise.sh", body: `resolve_mise_sha256 mise-v2026.10.19-macos-arm64`, want: "7dc61dfa71abbcda1c40d18440bc556b5d58b80e6042bc4f95fd106fa1bb3c66"},
		{name: "publiccode amd64", script: "install-publiccode-parser.sh", body: `resolve_publiccode_parser_sha256 publiccode-parser-go_Linux_x86_64.tar.gz`, want: "211847305611417ac0b76af5169086232ea6c75ff6ae686227b6972a41599dd7"},
		{name: "publiccode arm64", script: "install-publiccode-parser.sh", body: `resolve_publiccode_parser_sha256 publiccode-parser-go_Linux_arm64.tar.gz`, want: "33363da55ddf38457f6f9081cc8f3b833e833eb3a401d6c9c3feb8e5f2dc3fd7"},
		{name: "publiccode darwin amd64", script: "install-publiccode-parser.sh", body: `resolve_publiccode_parser_sha256 publiccode-parser-go_Darwin_x86_64.tar.gz`, want: "38d6bb4be977a1b0ea723e1c4decece852d3c143d15ba98620956e8617354d1f"},
		{name: "publiccode darwin arm64", script: "install-publiccode-parser.sh", body: `resolve_publiccode_parser_sha256 publiccode-parser-go_Darwin_arm64.tar.gz`, want: "61b3754455bfa5c04fe766d549e8b1d01fe804f7d914c93ee19e3ea28e6a46ae"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			stdout, stderr, err := runSourcedScript(t, testCase.script, testCase.body, nil)
			if err != nil || stdout != testCase.want {
				t.Fatalf("resolver result: stdout=%q stderr=%q err=%v, want %q", stdout, stderr, err, testCase.want)
			}
		})
	}
}

func TestRuntimeInstallersFailBeforeNetworkWhenExactAssetHasNoPin(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "curl-called")
	body := `command() {
  if [[ "$1" == -v && "$2" == gh ]]; then return 1; fi
  builtin command "$@"
}
curl() { printf called >"$CURL_MARKER"; }
resolve_gh_dist() { printf 'gh_2.103.2_macOS_ppc64.zip'; }
install_gh`

	_, stderr, err := runSourcedScript(t, "install-gh.sh", body, map[string]string{
		"OS": "Darwin", "ARCH": "ppc64", "CI_TEMP_DIR": t.TempDir(), "CURL_MARKER": marker,
	})
	if err == nil || !strings.Contains(stderr, "no pinned SHA-256") {
		t.Fatalf("unhashed asset result: err=%v stderr=%q", err, stderr)
	}

	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("network ran before hash resolution: %v", statErr)
	}
}

func TestInstallGH_DarwinUsesZipExtraction(t *testing.T) {
	evidence.RequireTool(t, "unzip")

	fixture := filepath.Join(t.TempDir(), "gh.zip")
	writeGHZip(t, fixture, "gh_2.103.2_macOS_arm64")
	installDir := t.TempDir()
	body := `command() {
  if [[ "$1" == -v && "$2" == gh && ! -x "$INSTALL_DIR/gh" ]]; then return 1; fi
  builtin command "$@"
}
ci_install_dir() { printf '%s' "$INSTALL_DIR"; }
ci_download_verified() { cp "$FIXTURE" "$2"; }
install_gh`

	stdout, stderr, err := runSourcedScript(t, "install-gh.sh", body, map[string]string{
		"OS": "Darwin", "ARCH": "arm64", "INSTALL_DIR": installDir, "FIXTURE": fixture,
	})
	if err != nil {
		t.Fatalf("Darwin gh install: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}

	if _, err := os.Stat(filepath.Join(installDir, "gh")); err != nil {
		t.Fatalf("Darwin ZIP binary was not flattened: %v", err)
	}
}

func writeGHZip(t *testing.T, path, root string) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	zw := zip.NewWriter(file)

	entry, err := zw.Create(root + "/bin/gh")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := entry.Write([]byte("#!/usr/bin/env bash\nprintf 'gh test-version\\n'\n")); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallForgeCI_ReleaseRequiresPinnedCosignBootstrap(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cosign-bootstrap-called")
	body := `install_cosign() { printf called >"$BOOTSTRAP_MARKER"; return 1; }; install_forge_ci_release v0.90.0 "$INSTALL_DIR"`

	_, stderr, err := runSourcedScript(t, "install-forge-ci.sh", body, map[string]string{
		"BOOTSTRAP_MARKER": marker,
		"INSTALL_DIR":      filepath.Join(t.TempDir(), "install"),
	})
	if err == nil || !strings.Contains(stderr, "failed to bootstrap") {
		t.Fatalf("bootstrap failure result: err=%v stderr=%q", err, stderr)
	}

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("pinned cosign bootstrap was not called: %v", statErr)
	}
}

func TestInstallForgeCIHasNoUnboundRawDownloadCallers(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		ext := filepath.Ext(path)
		if entry.IsDir() || (ext != ".yml" && ext != ".yaml" && ext != ".sh") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		text := string(body)
		if !strings.Contains(text, `"$raw/install-forge-ci.sh"`) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		t.Errorf("raw installer caller without a single revision-bound checkout: %s", rel)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallForgeCI_SourceUsesRequestedGitRevision(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			checkForgeCISourceRevision(t, format)
		})
	}
}

func checkForgeCISourceRevision(t *testing.T, format string) {
	t.Helper()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()

	repo := filepath.Join(root, "diggsweden", "forge-ci.git")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}

	git := func(args ...string) string {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), gitPath, append([]string{"-C", repo}, args...)...)
		cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}

		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main", "--object-format="+format)
	writeBootstrapFixture(t, filepath.Join(repo, "source-version"), "request-commit")
	git("add", "source-version")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "request")
	requestSHA := git("rev-parse", "HEAD")
	git("tag", "release-request/v0.92.1")
	git("tag", "v0.90.0-pre")
	writeBootstrapFixture(t, filepath.Join(repo, "source-version"), "new-main-commit")
	git("add", "source-version")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "advance main")
	mainSHA := git("rev-parse", "HEAD")
	git("tag", "-f", "v0.90.0-pre")

	for _, tc := range []struct{ ref, sha, version, bound string }{
		{requestSHA, requestSHA, "request-commit", ""},
		{"release-request/v0.92.1", requestSHA, "request-commit", ""},
		{"main", mainSHA, "new-main-commit", ""},
		{"v0.90.0-pre", mainSHA, "new-main-commit", ""},
		{"main", requestSHA, "request-commit", requestSHA},
		{requestSHA[:12], "", "", ""},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			binDir := t.TempDir()
			if err := os.Symlink(gitPath, filepath.Join(binDir, "git")); err != nil {
				t.Fatal(err)
			}
			// Model Go's stale module cache separately from the real Git checkout.
			// The fixture compiler's output identifies the source actually built.
			fakeGo := `#!/bin/bash
[[ "$GOBIN" == /* ]] || exit 96
if [[ "$*" == "install ./cmd/forge-ci" ]]; then
  version="$(cat source-version)"
else
  version=stale-module-cache
fi
printf '#!/bin/bash\nprintf "%%s\\n" "%s"\n' "$version" >"$GOBIN/forge-ci"
chmod +x "$GOBIN/forge-ci"
`
			if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o700); err != nil {
				t.Fatal(err)
			}

			githubPath := filepath.Join(t.TempDir(), "github-path")
			installRoot := t.TempDir()
			installDir := installRoot + "/./install"

			useSource := "0"
			if tc.ref == "v0.90.0-pre" {
				useSource = "1"
			}

			env := map[string]string{
				"PATH":                    binDir,
				"INSTALL_ROOT":            installRoot,
				"FORGE_CI_SERVER_URL":     "file://" + root,
				"FORGE_CI_BINARY_REF":     tc.ref,
				"FORGE_CI_INSTALL_DIR":    "./install",
				"FORGE_CI_USE_GO_INSTALL": useSource,
				"GITHUB_PATH":             githubPath,
			}
			if tc.bound != "" {
				env["FORGE_CI_SOURCE_SHA"] = tc.bound
			}

			stdout, stderr, err := runSourcedScript(t, "install-forge-ci.sh", `cd "$INSTALL_ROOT" && install_forge_ci "$FORGE_CI_BINARY_REF" && forge-ci --version`, env)
			if tc.version == "" {
				if err == nil || !strings.Contains(stderr, "use a full 40- or 64-character commit SHA") {
					t.Fatalf("short SHA refusal: %v %s", err, stderr)
				}

				if _, err := os.Stat(githubPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed source fetch published PATH: %v", err)
				}

				return
			}

			if err != nil || !strings.HasSuffix(stdout, tc.version+"\n") || !strings.Contains(stdout, "at commit "+tc.sha) {
				t.Fatalf("requested source not built: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
			}

			pathBody, err := os.ReadFile(githubPath)
			if err != nil || strings.TrimSpace(string(pathBody)) != installDir {
				t.Fatalf("GITHUB_PATH = %q, err=%v", pathBody, err)
			}
		})
	}
}

func TestInstallForgeCI_RefusesInvalidBootstrapBindingBeforeBuild(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{"local", ".", strings.Repeat("a", 40), strings.Repeat("a", 64)} {
		for _, sha := range []string{"", "deadbeef", strings.Repeat("b", 40), strings.Repeat("b", 64)} {
			t.Run(ref+sha, func(t *testing.T) {
				t.Parallel()

				root := t.TempDir()
				body := `install_forge_ci_go_install() { printf called >"$BUILD_MARKER"; }; install_forge_ci "$FORGE_CI_BINARY_REF"`

				_, stderr, err := runSourcedScript(t, "install-forge-ci.sh", body, map[string]string{
					"FORGE_CI_BINARY_REF": ref,
					"FORGE_CI_SOURCE_SHA": sha,
					"BUILD_MARKER":        filepath.Join(root, "built"),
					"GITHUB_PATH":         filepath.Join(root, "path"),
				})
				if err == nil || !strings.Contains(stderr, "ERROR:") {
					t.Fatalf("invalid source binding accepted: %v %s", err, stderr)
				}

				for _, name := range []string{"built", "path"} {
					if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("refused binding changed %s: %v", name, err)
					}
				}
			})
		}
	}
}

func TestInstallForgeCI_LocalSourceUsesAbsoluteInstallDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	body := `go() {
  [[ "$*" == "install ./cmd/forge-ci" && "$GOBIN" == "$INSTALL_ROOT/./bin" ]] || return 96
  printf '#!/bin/bash\nprintf "local fixture\\n"\n' >"$GOBIN/forge-ci"
  chmod +x "$GOBIN/forge-ci"
}; cd "$INSTALL_ROOT" && install_forge_ci local`

	stdout, stderr, err := runSourcedScript(t, "install-forge-ci.sh", body, map[string]string{
		"INSTALL_ROOT":         root,
		"FORGE_CI_INSTALL_DIR": "./bin",
	})
	if err != nil || !strings.Contains(stdout, "local fixture") {
		t.Fatalf("local relative installation: %v %s %s", err, stdout, stderr)
	}
}

func TestInstallForgeCI_DefaultUsesConcreteStableRelease(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ explicit, redirect, want string }{
		{"", "https://github.com/diggsweden/forge-ci/releases/tag/v0.93.0", "v0.93.0"},
		{"v0.92.1", "must-not-query", "v0.92.1"},
		{"", "https://github.com/diggsweden/forge-ci/releases/tag/v0.93.0-rc.1", ""},
		{"", "https://github.com/foreign/repository/releases/tag/v0.93.0", ""},
	} {
		t.Run(tc.explicit+tc.redirect, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			body := `curl() {
  [[ "$REDIRECT" != must-not-query && "${*: -1}" == https://github.com/diggsweden/forge-ci/releases/latest ]] || return 94
  [[ ! -e "$QUERY_MARKER" ]] || return 95
  printf queried >"$QUERY_MARKER"
  printf '%s' "$REDIRECT"
}; install_forge_ci_release() {
  printf '%s' "$1" >"$SELECTED_TAG"
  mkdir -p "$2"
  printf '#!/bin/bash\nprintf "stable fixture\\n"\n' >"$2/forge-ci"
  chmod +x "$2/forge-ci"
}; install_forge_ci "$EXPLICIT"`

			_, stderr, err := runSourcedScript(t, "install-forge-ci.sh", body, map[string]string{
				"EXPLICIT":             tc.explicit,
				"REDIRECT":             tc.redirect,
				"QUERY_MARKER":         filepath.Join(root, "query"),
				"SELECTED_TAG":         filepath.Join(root, "tag"),
				"FORGE_CI_INSTALL_DIR": filepath.Join(root, "bin"),
			})
			if tc.want == "" {
				if err == nil || !strings.Contains(stderr, "stable version tag") {
					t.Fatalf("invalid stable selection: %v %s", err, stderr)
				}

				return
			}

			if err != nil {
				t.Fatalf("stable selection: %v %s", err, stderr)
			}

			tag, err := os.ReadFile(filepath.Join(root, "tag"))
			if err != nil || string(tag) != tc.want {
				t.Fatalf("selected tag %q, err=%v", tag, err)
			}
		})
	}
}

func TestInstallForgeCI_ReleaseAssetPath(t *testing.T) {
	evidence.RequireTool(t, "tar")

	if _, err := exec.LookPath("sha256sum"); err != nil {
		if _, err := exec.LookPath("shasum"); err != nil {
			evidence.NotProvenf(t, "neither sha256sum nor shasum is on PATH")
		}
	}

	// Stage release fixtures: a tarball containing a fake forge-ci binary
	// plus a checksums.txt with the matching SHA-256.
	releaseRoot := t.TempDir()

	releaseDir := filepath.Join(releaseRoot, "v0.90.0")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tarballName := osArchTarballName(t)
	binaryBody := []byte("#!/usr/bin/env bash\nprintf 'forge-ci test-version\\n'\n")
	tarballPath := filepath.Join(releaseDir, tarballName)
	writeInstallerTarball(t, tarballPath, "forge-ci", binaryBody)

	checksums := sha256sumFile(t, tarballPath) + "  " + tarballName + "\n"
	if err := os.WriteFile(filepath.Join(releaseDir, "checksums.txt"), []byte(checksums), 0o644); err != nil {
		t.Fatal(err)
	}

	// Signature verification is fail-closed: the release must carry a
	// checksums.txt.bundle and cosign must accept it. The fixture bundle
	// plus a fake cosign (exit 0, argv logged) keep the test hermetic
	// while exercising the signed path's plumbing.
	if err := os.WriteFile(filepath.Join(releaseDir, "checksums.txt.bundle"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake curl that returns staged fixtures by filename. Mirrors the
	// `curl -sSfL -o <dest> <url>` invocation in the script.
	binDir := t.TempDir()
	writeFakeCurl(t, binDir)

	cosignLog := filepath.Join(t.TempDir(), "cosign-args")
	writeFakeCosign(t, binDir, cosignLog)

	installDir := filepath.Join(t.TempDir(), "install")
	githubPath := filepath.Join(t.TempDir(), "github-path")

	env := map[string]string{
		"PATH":                     binDir,
		"FORGE_CI_BINARY_REF":      "v0.90.0",
		"FORGE_CI_SOURCE_SHA":      "not-used-for-signed-release-assets",
		"FORGE_CI_INSTALL_DIR":     installDir,
		"RELEASE_FIXTURE_ROOT":     releaseRoot,
		"EXPECTED_COSIGN_IDENTITY": `^https://github\.com/diggsweden/forge-ci/\.github/workflows/release-binary\.yml@refs/tags/release-request/v0\.90\.0$`,
		"EXPECTED_COSIGN_ISSUER":   "https://token.actions.githubusercontent.com",
		"GITHUB_PATH":              githubPath,
	}

	stdout, stderr, err := runSourcedScript(t, "install-forge-ci.sh", `bootstrap_forge_ci_cosign() { :; }; install_forge_ci "$FORGE_CI_BINARY_REF"`, env)
	if err != nil {
		t.Fatalf("install_forge_ci: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}

	if !strings.Contains(stdout, "Downloading forge-ci v0.90.0") {
		t.Errorf("expected download log, got: %s", stdout)
	}

	cosignArgs, err := os.ReadFile(cosignLog)
	if err != nil {
		t.Fatalf("cosign was not invoked: %v", err)
	}

	for _, want := range []string{"verify-blob", "--bundle", "--new-bundle-format"} {
		if !strings.Contains(string(cosignArgs), want) {
			t.Errorf("cosign argv missing %q: %s", want, cosignArgs)
		}
	}

	got, err := os.ReadFile(filepath.Join(installDir, "forge-ci"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, binaryBody) {
		t.Errorf("extracted binary mismatch")
	}

	pathBody, err := os.ReadFile(githubPath)
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(string(pathBody)) != installDir {
		t.Fatalf("GITHUB_PATH = %q", pathBody)
	}

	rejectedDir := filepath.Join(t.TempDir(), "rejected")
	env["FORGE_CI_INSTALL_DIR"] = rejectedDir
	env["COSIGN_VERIFY_FAIL"] = "1"

	_, stderr, err = runSourcedScript(t, "install-forge-ci.sh", `bootstrap_forge_ci_cosign() { :; }; install_forge_ci "$FORGE_CI_BINARY_REF"`, env)
	if err == nil || !strings.Contains(stderr, "cosign verification") {
		t.Errorf("verification refusal: err=%v stderr=%q", err, stderr)
	}

	if _, err := os.Stat(rejectedDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed verification created an install destination: %v", err)
	}
}

func TestInstallForgeCI_ReleaseRefusalsPreserveExistingState(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{"success", "bootstrap", "archive download", "checksum download", "bundle download", "verification", "checksum", "partial extraction", "binary pin", "copy", "mode", "publish"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			checkReleaseInstallState(t, phase)
		})
	}
}

func checkReleaseInstallState(t *testing.T, phase string) {
	t.Helper()
	root, work, installDir, binDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()

	releaseDir := filepath.Join(root, "v0.90.0")
	if err := os.Mkdir(releaseDir, 0o700); err != nil {
		t.Fatal(err)
	}

	dist := osArchTarballName(t)
	archive := filepath.Join(releaseDir, dist)
	binary := []byte("#!/usr/bin/env bash\nprintf 'new fixture version\\n'\n")
	writeInstallerTarball(t, archive, "forge-ci", binary)

	checksums := sha256sumFile(t, archive) + "  " + dist + "\n"
	for name, body := range map[string]string{"checksums.txt": checksums, "checksums.txt.bundle": "{}"} {
		writeBootstrapFixture(t, filepath.Join(releaseDir, name), body)
	}

	target := filepath.Join(installDir, "forge-ci")
	writeBootstrapFixture(t, target, "old binary")

	pathFile := filepath.Join(root, "github-path")
	writeBootstrapFixture(t, pathFile, "existing path\n")
	writeFakeCurl(t, binDir)
	writeFakeCosign(t, binDir, filepath.Join(root, "cosign-args"))
	env := map[string]string{"PATH": binDir, "TMPDIR": work, "CI_TEMP_DIR": work, "RELEASE_FIXTURE_ROOT": root, "FORGE_CI_INSTALL_DIR": installDir, "GITHUB_PATH": pathFile, "PHASE_MARKER": filepath.Join(root, "phase-reached")}
	body := `bootstrap_forge_ci_cosign() { :; }; `
	wantMessage, requireMarker := "", false

	switch phase {
	case "bootstrap":
		body = `bootstrap_forge_ci_cosign() { mkdir -p "$CI_TEMP_DIR"; printf partial >"$CI_TEMP_DIR/partial"; printf reached >"$PHASE_MARKER"; return 1; }; `
		requireMarker = true
	case "archive download", "checksum download", "bundle download":
		name := map[string]string{"archive download": dist, "checksum download": "checksums.txt", "bundle download": "checksums.txt.bundle"}[phase]
		if err := os.Remove(filepath.Join(releaseDir, name)); err != nil {
			t.Fatal(err)
		}

		wantMessage = name
	case "verification":
		env["COSIGN_VERIFY_FAIL"] = "1"
		wantMessage = "cosign verification"
	case "checksum":
		writeBootstrapFixture(t, filepath.Join(releaseDir, "checksums.txt"), strings.Repeat("0", 64)+"  "+dist+"\n")

		wantMessage = "SHA-256 mismatch"
	case "partial extraction":
		body += `tar() { printf partial >"$4/forge-ci"; printf reached >"$PHASE_MARKER"; return 1; }; `
		wantMessage, requireMarker = "failed to extract", true
	case "binary pin":
		env["FORGE_CI_BINARY_SHA256"] = strings.Repeat("0", 64)
		wantMessage = "does not match FORGE_CI_BINARY_SHA256"
	case "copy", "mode", "publish":
		command := map[string]string{"copy": "cp", "mode": "chmod", "publish": "mv"}[phase]
		body += command + `() { printf reached >"$PHASE_MARKER"; return 1; }; `
		requireMarker = true
	}

	_, stderr, runErr := runSourcedScript(t, "install-forge-ci.sh", body+`install_forge_ci v0.90.0`, env)
	if phase == "success" {
		if runErr != nil {
			t.Fatalf("valid installation failed: %v %s", runErr, stderr)
		}
	} else if runErr == nil || !strings.Contains(stderr, wantMessage) {
		t.Errorf("refusal: err=%v stderr=%q", runErr, stderr)
	}

	_, markerErr := os.Stat(env["PHASE_MARKER"])
	if requireMarker && markerErr != nil {
		t.Errorf("intended failure phase was not reached: %v", markerErr)
	}

	want := []byte("old binary")
	if phase == "success" {
		want = binary
	}

	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("installed target changed incorrectly: %v", err)
	}

	entries, err := os.ReadDir(installDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "forge-ci" {
		t.Errorf("unexpected install state: %v %v", entries, err)
	}

	entries, err = os.ReadDir(work)
	if err != nil || len(entries) != 0 {
		t.Errorf("temporary state leaked: %v %v", entries, err)
	}

	if phase == "success" {
		return
	}

	info, statErr := os.Stat(target)
	if statErr != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("refusal changed installed mode: %v", statErr)
	}

	paths, err := os.ReadFile(pathFile)
	if err != nil || string(paths) != "existing path\n" {
		t.Errorf("refusal changed runner paths: %v", err)
	}
}

func writeBootstrapFixture(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallForgeCI_ShellVerificationCases(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSourcedScript(t, "install-forge-ci_test.sh", "", nil)
	if err != nil || !strings.Contains(stdout, "22 passed, 0 failed") {
		t.Fatalf("shell verification: %v stdout=%s stderr=%s", err, stdout, stderr)
	}
}

func TestInstallerHarness_IsolatesPolicyAndCleansOwnedRoot(t *testing.T) {
	root := t.TempDir()

	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "sha256sum"), []byte("#!/bin/sh\nexit 93\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := runSourcedScript(t, "install-forge-ci_test.sh", "", map[string]string{"PATH": tools, "TMPDIR": root, "FORGE_CI_RELEASE_URL_BASE": "https://fixture.invalid/wrong", "FORGE_CI_COSIGN_IDENTITY": "wrong-policy", "FORGE_CI_BINARY_SHA256": "wrong-policy", "HTTP_PROXY": "https://fixture.invalid"})
	if err != nil {
		t.Fatalf("harness: %v: %s", err, stderr)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Errorf("harness left %d entries", len(entries))
	}
}

func TestInstallForgeCI_ReleaseAssetTamperingDetected(t *testing.T) {
	evidence.RequireTool(t, "tar")

	releaseRoot := t.TempDir()

	releaseDir := filepath.Join(releaseRoot, "v0.90.0")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tarballName := osArchTarballName(t)
	tarballPath := filepath.Join(releaseDir, tarballName)
	writeInstallerTarball(t, tarballPath, "forge-ci", []byte("genuine\n"))
	// Checksums file claims a hash for unrelated content; verification must fail.
	bogusChecksum := strings.Repeat("0", 64) + "  " + tarballName + "\n"
	if err := os.WriteFile(filepath.Join(releaseDir, "checksums.txt"), []byte(bogusChecksum), 0o644); err != nil {
		t.Fatal(err)
	}

	// A bundle + fake cosign get the run past the fail-closed signature
	// gate, so the test reaches the SHA-256 check it is actually about.
	if err := os.WriteFile(filepath.Join(releaseDir, "checksums.txt.bundle"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	writeFakeCurl(t, binDir)
	writeFakeCosign(t, binDir, filepath.Join(t.TempDir(), "cosign-args"))
	// Provide a fake go so the test can prove verification failure never falls
	// back to source installation.
	logPath := filepath.Join(t.TempDir(), "go-args")
	writeFakeGo(t, binDir, logPath)

	installDir := filepath.Join(t.TempDir(), "install")

	_, stderr, err := runSourcedScript(t, "install-forge-ci.sh", `bootstrap_forge_ci_cosign() { :; }; install_forge_ci "$FORGE_CI_BINARY_REF"`, map[string]string{
		"PATH":                      binDir,
		"FORGE_CI_BINARY_REF":       "v0.90.0",
		"FORGE_CI_INSTALL_DIR":      installDir,
		"FORGE_CI_RELEASE_URL_BASE": "file://" + releaseRoot,
	})
	if err == nil {
		t.Fatalf("tampered release unexpectedly installed; stderr=%s", stderr)
	}

	if !strings.Contains(stderr, "SHA-256 mismatch") {
		t.Errorf("expected SHA-256 mismatch error, got stderr: %s", stderr)
	}

	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("go install was invoked after checksum failure")
	}
}

// TestInstallForgeCI_UnsignedReleaseRefused pins the fail-closed contract:
// a release without a checksums.txt.bundle terminates installation.
func TestInstallForgeCI_UnsignedReleaseRefused(t *testing.T) {
	evidence.RequireTool(t, "tar")

	releaseRoot := t.TempDir()

	releaseDir := filepath.Join(releaseRoot, "v0.90.0")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tarballName := osArchTarballName(t)
	tarballPath := filepath.Join(releaseDir, tarballName)
	writeInstallerTarball(t, tarballPath, "forge-ci", []byte("genuine\n"))

	checksums := sha256sumFile(t, tarballPath) + "  " + tarballName + "\n"
	if err := os.WriteFile(filepath.Join(releaseDir, "checksums.txt"), []byte(checksums), 0o644); err != nil {
		t.Fatal(err)
	}
	// Deliberately NO checksums.txt.bundle.

	binDir := t.TempDir()
	writeFakeCurl(t, binDir)
	writeFakeCosign(t, binDir, filepath.Join(t.TempDir(), "cosign-args"))

	goLog := filepath.Join(t.TempDir(), "go-args")
	writeFakeGo(t, binDir, goLog)

	installDir := filepath.Join(t.TempDir(), "install")

	stdout, stderr, err := runSourcedScript(t, "install-forge-ci.sh", `bootstrap_forge_ci_cosign() { :; }; install_forge_ci "$FORGE_CI_BINARY_REF"`, map[string]string{
		"PATH":                      binDir,
		"FORGE_CI_BINARY_REF":       "v0.90.0",
		"FORGE_CI_INSTALL_DIR":      installDir,
		"FORGE_CI_RELEASE_URL_BASE": "file://" + releaseRoot,
	})
	if err == nil {
		t.Fatalf("unsigned release unexpectedly installed\nstdout=%s\nstderr=%s", stdout, stderr)
	}

	if !strings.Contains(stderr, "failed to download") || !strings.Contains(stderr, "checksums.txt.bundle") {
		t.Errorf("expected fail-closed unsigned refusal, got stderr: %s", stderr)
	}

	if _, err := os.Stat(goLog); !os.IsNotExist(err) {
		t.Errorf("go install was invoked after signature download failure")
	}
}

// osArchTarballName names the release tarball for the fixture version
// v0.90.0 on the host OS/arch (every installer test stages that version).
func osArchTarballName(t *testing.T) string {
	t.Helper()

	const ref = "v0.90.0"

	osName := "linux"
	if runtime.GOOS == "darwin" {
		osName = "darwin"
	}

	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}

	return "forge-ci_" + strings.TrimPrefix(ref, "v") + "_" + osName + "_" + arch + ".tar.gz"
}

func writeInstallerTarball(t *testing.T, path, entryName string, body []byte) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	gz := gzip.NewWriter(file)

	defer func() { _ = gz.Close() }()

	tw := tar.NewWriter(gz)

	defer func() { _ = tw.Close() }()

	hdr := &tar.Header{Name: entryName, Mode: 0o755, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}

	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
}

func sha256sumFile(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

func writeFakeCurl(t *testing.T, dir string) {
	t.Helper()
	// Recognize only the configured fixture URL; never delegate to a real client.
	body := `#!/usr/bin/env bash
set -euo pipefail
dest=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) dest="$2"; shift 2 ;;
    file://*) url="$1"; shift ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
if [ -z "$dest" ] || [ -z "$url" ]; then
  echo "fake curl: missing -o or url" >&2
  exit 2
fi
if [[ -n "${INSTALLER_FIXTURE:-}" ]]; then
  [[ "$url" == "$EXPECTED_DOWNLOAD_URL" ]] || { printf 'unexpected download URL\n' >&2; exit 2; }
  src="$INSTALLER_FIXTURE"
elif [[ -n "${RELEASE_FIXTURE_ROOT:-}" ]]; then
  base=https://github.com/diggsweden/forge-ci/releases/download/
  [[ "$url" == "$base"* ]] || { printf 'unexpected release URL\n' >&2; exit 2; }
  src="$RELEASE_FIXTURE_ROOT/${url#"$base"}"
else
  [[ "$url" == file://* ]] || { printf 'only owned file fixtures are supported\n' >&2; exit 2; }
  src="${url#file://}"
fi
if [[ -n "${FAKE_CURL_FAIL_AFTER_PARTIAL:-}" ]]; then
  head -c 3 "$src" >"$dest"
  exit 18
fi
cp "$src" "$dest"
`
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInstallForgeCI_LocalRef(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "go-args")
	writeFakeGo(t, binDir, logPath)
	installDir := filepath.Join(t.TempDir(), "install")

	stdout, stderr, err := runSourcedScript(t, "install-forge-ci.sh", `install_forge_ci local`, map[string]string{
		"PATH":                 binDir,
		"FORGE_CI_INSTALL_DIR": installDir,
	})
	if err != nil {
		t.Fatalf("install_forge_ci local: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}

	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(args)); got != "install ./cmd/forge-ci" {
		t.Fatalf("go args = %q", got)
	}
}

// writeFakeCosign records arguments, optionally checks the full verification
// contract, and returns the fixture's configured result without external I/O.
func writeFakeCosign(t *testing.T, dir, logPath string) {
	t.Helper()

	body := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" > "` + logPath + `"
if [[ -n "${EXPECTED_COSIGN_IDENTITY:-}" ]]; then
  [[ "$#" == 9 && "$1" == verify-blob && "$2" == --bundle && "$3" == "${9}.bundle" &&
     "$4" == --new-bundle-format && "$5" == --certificate-identity-regexp &&
     "$6" == "$EXPECTED_COSIGN_IDENTITY" && "$7" == --certificate-oidc-issuer &&
     "$8" == "$EXPECTED_COSIGN_ISSUER" && "$9" == */checksums.txt && -f "$3" && -f "$9" ]] || exit 2
fi
[[ "${COSIGN_VERIFY_FAIL:-0}" != 1 ]]
`
	if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeGo(t *testing.T, dir, logPath string) {
	t.Helper()

	body := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" > "` + logPath + `"
mkdir -p "$GOBIN"
cat > "$GOBIN/forge-ci" <<'EOS'
#!/usr/bin/env bash
printf 'forge-ci test-version\n'
EOS
chmod +x "$GOBIN/forge-ci"
`

	path := filepath.Join(dir, "go")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestInstallerResolvers_EveryPlatformAndAliasHasAPinnedAsset resolves every
// supported OS and architecture spelling for each resolver-based installer to
// its exact release asset and requires that asset to have a pinned SHA-256
// (the pin values themselves are held by TestRuntimeInstallerHashesMatchExactAssets).
// An unsupported architecture on a supported OS, and an unsupported OS, are
// refused before any pin lookup.
func TestInstallerResolvers_EveryPlatformAndAliasHasAPinnedAsset(t *testing.T) {
	t.Parallel()

	type platform struct{ os, arch string }

	linuxAMD64 := []platform{{"Linux", "x86_64"}, {"Linux", "amd64"}}
	linuxARM64 := []platform{{"Linux", "aarch64"}, {"Linux", "arm64"}}
	darwinAMD64 := []platform{{"Darwin", "x86_64"}, {"Darwin", "amd64"}}
	darwinARM64 := []platform{{"Darwin", "arm64"}, {"Darwin", "aarch64"}}

	for _, tool := range []struct {
		script, resolve, pin, refusal string
		assets                        map[string][]platform
		unsupported                   []platform
	}{
		{script: "install-gh.sh", resolve: "resolve_gh_dist", pin: "resolve_gh_sha256", refusal: "ERROR: Unsupported gh platform: ", assets: map[string][]platform{
			"gh_2.103.2_linux_amd64.tar.gz": linuxAMD64, "gh_2.103.2_linux_arm64.tar.gz": linuxARM64,
			"gh_2.103.2_macOS_amd64.zip": darwinAMD64, "gh_2.103.2_macOS_arm64.zip": darwinARM64,
		}},
		{script: "install-glab.sh", resolve: "resolve_glab_dist", pin: "resolve_glab_sha256", refusal: "ERROR: Unsupported glab platform: ", assets: map[string][]platform{
			"glab_1.124.0_linux_amd64.tar.gz": linuxAMD64, "glab_1.124.0_linux_arm64.tar.gz": linuxARM64,
			"glab_1.124.0_darwin_amd64.tar.gz": darwinAMD64, "glab_1.124.0_darwin_arm64.tar.gz": darwinARM64,
		}},
		{script: "install-git-cliff.sh", resolve: "resolve_git_cliff_dist", pin: "resolve_git_cliff_sha256", refusal: "ERROR: Unsupported git-cliff platform: ", assets: map[string][]platform{
			"git-cliff-2.14.2-x86_64-unknown-linux-gnu.tar.gz": linuxAMD64, "git-cliff-2.14.2-aarch64-unknown-linux-gnu.tar.gz": linuxARM64,
			"git-cliff-2.14.2-x86_64-apple-darwin.tar.gz": darwinAMD64, "git-cliff-2.14.2-aarch64-apple-darwin.tar.gz": darwinARM64,
		}},
		{script: "install-yq.sh", resolve: "resolve_yq_dist", pin: "resolve_yq_sha256", refusal: "ERROR: Unsupported yq platform: ", assets: map[string][]platform{
			"yq_linux_amd64": linuxAMD64, "yq_linux_arm64": linuxARM64,
			"yq_linux_arm":    {{"Linux", "armv6l"}, {"Linux", "armv7l"}, {"Linux", "arm"}},
			"yq_darwin_amd64": darwinAMD64, "yq_darwin_arm64": darwinARM64,
		}, unsupported: []platform{{"Darwin", "armv7l"}}},
		{script: "install-mise.sh", resolve: "resolve_mise_dist", pin: "resolve_mise_sha256", refusal: "ERROR: Unsupported mise platform: ", assets: map[string][]platform{
			"mise-v2026.10.19-linux-x64": linuxAMD64, "mise-v2026.10.19-linux-arm64": linuxARM64,
			"mise-v2026.10.19-macos-x64": darwinAMD64, "mise-v2026.10.19-macos-arm64": darwinARM64,
		}},
		{script: "install-publiccode-parser.sh", resolve: "resolve_publiccode_parser_dist", pin: "resolve_publiccode_parser_sha256", refusal: "ERROR: Unsupported publiccode-parser platform: ", assets: map[string][]platform{
			"publiccode-parser-go_Linux_x86_64.tar.gz": linuxAMD64, "publiccode-parser-go_Linux_arm64.tar.gz": linuxARM64,
			"publiccode-parser-go_Darwin_x86_64.tar.gz": darwinAMD64, "publiccode-parser-go_Darwin_arm64.tar.gz": darwinARM64,
		}},
		{script: "install-cosign.sh", resolve: "resolve_cosign_asset", pin: "resolve_cosign_sha256", refusal: "ERROR: unsupported cosign platform: ", assets: map[string][]platform{
			"cosign-linux-amd64": linuxAMD64, "cosign-linux-arm64": linuxARM64,
			"cosign-darwin-amd64": darwinAMD64, "cosign-darwin-arm64": darwinARM64,
		}},
		// A runner is lent to a Linux job; there is no macOS build to lend.
		{script: "install-forgejo-runner.sh", resolve: "resolve_forgejo_runner_asset", pin: "resolve_forgejo_runner_sha256", refusal: "ERROR: unsupported forgejo-runner platform: ", assets: map[string][]platform{
			"forgejo-runner-13.2.0-linux-amd64": linuxAMD64, "forgejo-runner-13.2.0-linux-arm64": linuxARM64,
		}, unsupported: append(darwinAMD64, darwinARM64...)},
	} {
		t.Run(tool.script, func(t *testing.T) {
			t.Parallel()

			for asset, platforms := range tool.assets {
				for _, target := range platforms {
					body := `dist="$(` + tool.resolve + `)" && printf '%s\n' "$dist" && ` + tool.pin + ` "$dist"`

					stdout, stderr, err := runSourcedScript(t, tool.script, body, map[string]string{"OS": target.os, "ARCH": target.arch})

					resolved, pin, _ := strings.Cut(stdout, "\n")
					if err != nil || resolved != asset || !container.ValidSHA256Hex(pin) {
						t.Errorf("%s/%s: resolved %q with pin %q (err %v, stderr %q), want %s with a pinned SHA-256",
							target.os, target.arch, resolved, pin, err, stderr, asset)
					}
				}
			}

			for _, target := range append(tool.unsupported, platform{"Linux", "riscv64"}, platform{"Darwin", "ppc64"}, platform{"FreeBSD", "x86_64"}) {
				stdout, stderr, err := runSourcedScript(t, tool.script, tool.resolve, map[string]string{"OS": target.os, "ARCH": target.arch})
				// cosign names the architecture after normalizing its aliases.
				if err == nil || stdout != "" || !strings.HasPrefix(stderr, tool.refusal+target.os+"/") || strings.Count(stderr, "\n") != 1 {
					t.Errorf("%s/%s: stdout %q, stderr %q, err %v, want an unsupported-platform refusal", target.os, target.arch, stdout, stderr, err)
				}
			}
		})
	}
}

// An installer runs in one step and its tool in a later one: publish-container
// installs trivy before the image scan. An exported PATH ends with the step, so
// the shared helper also records the directory in $GITHUB_PATH, which GitHub and
// Forgejo prepend to every later step's PATH.
func TestCIPrependPath_CarriesTheDirectoryIntoLaterSteps(t *testing.T) {
	t.Parallel()

	pathFile := filepath.Join(t.TempDir(), "github-path")

	stdout, stderr, err := runSourcedScript(t, "install-common.sh",
		`ci_prepend_path /opt/tool-bin && printf '%s\n' "${PATH%%:*}"`,
		map[string]string{"GITHUB_PATH": pathFile})
	if err != nil {
		t.Fatalf("ci_prepend_path: %v\n%s", err, stderr)
	}

	if strings.TrimSpace(stdout) != "/opt/tool-bin" {
		t.Errorf("this step's PATH starts with %q, want /opt/tool-bin", stdout)
	}

	body, err := os.ReadFile(pathFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "/opt/tool-bin\n" {
		t.Errorf("GITHUB_PATH = %q, want the directory on one line", body)
	}
}

// Forgejo names its file FORGEJO_PATH and also sets GITHUB_PATH; the helper
// writes Forgejo's own, as setup-forge-ci.sh does, and only that one.
func TestCIPrependPath_PrefersForgejoPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	forgejoPath, githubPath := filepath.Join(dir, "forgejo-path"), filepath.Join(dir, "github-path")

	_, stderr, err := runSourcedScript(t, "install-common.sh", `ci_prepend_path /opt/tool-bin`,
		map[string]string{"FORGEJO_PATH": forgejoPath, "GITHUB_PATH": githubPath})
	if err != nil {
		t.Fatalf("ci_prepend_path: %v\n%s", err, stderr)
	}

	if body, readErr := os.ReadFile(forgejoPath); readErr != nil || string(body) != "/opt/tool-bin\n" {
		t.Errorf("FORGEJO_PATH = %q (%v), want the directory", body, readErr)
	}

	if _, statErr := os.Stat(githubPath); !os.IsNotExist(statErr) {
		t.Errorf("GITHUB_PATH was written too: %v", statErr)
	}
}

// The cosign that verifies a forge-ci download lives in a temporary directory
// removed afterwards; carried into later steps, it would put a vanished
// directory on their PATH. The opt-out lasts only for the bootstrap call.
func TestBootstrapForgeCICosign_StaysOutOfLaterSteps(t *testing.T) {
	t.Parallel()

	pathFile := filepath.Join(t.TempDir(), "github-path")

	stdout, stderr, err := runSourcedScript(t, "install-forge-ci.sh",
		`install_cosign() { ci_prepend_path /tmp/bootstrap/cosign-bin; }; bootstrap_forge_ci_cosign && printf '%s' "$GITHUB_PATH"`,
		map[string]string{"GITHUB_PATH": pathFile, "FORGEJO_PATH": pathFile})
	if err != nil {
		t.Fatalf("bootstrap_forge_ci_cosign: %v\n%s", err, stderr)
	}

	if stdout != pathFile {
		t.Errorf("GITHUB_PATH after the bootstrap = %q, want it restored to %q", stdout, pathFile)
	}

	if body, readErr := os.ReadFile(pathFile); readErr == nil && len(body) > 0 {
		t.Errorf("the bootstrap cosign reached GITHUB_PATH: %q", body)
	}
}

// TestPinnedInstallers_ReuseOnlyThePinnedVersion puts the tool on PATH as a
// fixture reporting a version the way the real tool does. Only the pinned
// version is reused; any other, an unreadable one included, goes on to the
// pinned download, which the inert curl of runSourcedScript then refuses.
func TestPinnedInstallers_ReuseOnlyThePinnedVersion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ tool, pinned, other string }{
		{"trivy", "Version: 0.76.1", "Version: 0.76.0"},
		{"cyclonedx-gomod", "Version:          v1.12.0", "Version:          v1.12.0-rc1"},
		{"cargo-cyclonedx", "cargo-cyclonedx 0.5.9", "cargo-cyclonedx 0.5.10"},
		{"git-cliff", "git-cliff 2.14.2", "git-cliff 2.14.1"},
		{"gh", "gh version 2.103.2 (2026-10-01)", "gh version 2.103.20 (2026-10-01)"},
		{"glab", "glab 1.124.0 (abc1234)", "glab 1.123.0 (abc1234)"},
		{"yq", "yq (https://example.invalid/yq/) version v4.54.1", "yq version v4.54.11"},
		{"mise", "2026.10.19 linux-x64 (2026-10-19)", "2026.10.1 linux-x64 (2026-10-01)"},
		{"publiccode-parser", "publiccode-parser v5.4.3", ""},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			for _, reported := range []string{tc.pinned, tc.other} {
				bin := t.TempDir()
				fixture := "#!/bin/sh\nprintf '%s\\n' '" + reported + "'\n"

				if err := os.WriteFile(filepath.Join(bin, tc.tool), []byte(fixture), 0o700); err != nil {
					t.Fatal(err)
				}

				stdout, stderr, err := runSourcedScript(t, "install-"+tc.tool+".sh", "install_"+strings.ReplaceAll(tc.tool, "-", "_"),
					map[string]string{"PATH": bin})

				if reported == tc.pinned {
					if err != nil || !strings.Contains(stdout, "already installed") {
						t.Errorf("%q: err=%v stdout=%q stderr=%q, want the pinned tool reused", reported, err, stdout, stderr)
					}

					continue
				}

				if err == nil || !strings.Contains(stdout, "is not the pinned") || !strings.Contains(stderr, "unexpected download attempt") {
					t.Errorf("%q: err=%v stdout=%q stderr=%q, want the pinned download", reported, err, stdout, stderr)
				}
			}
		})
	}
}
