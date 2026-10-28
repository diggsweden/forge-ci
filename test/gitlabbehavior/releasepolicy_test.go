// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabbehavior

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"gopkg.in/yaml.v3"
)

func TestReleasePolicyComesFromTrustedInputNotAmbientVariables(t *testing.T) {
	decoder := yaml.NewDecoder(bytes.NewReader(reporoot.ReadFile(t, "templates/release-jobs.yml")))

	var spec any
	if err := decoder.Decode(&spec); err != nil {
		t.Fatal(err)
	}

	var jobs struct {
		Release struct {
			Before []string `yaml:"before_script"`
		} `yaml:".forge-ci-release"`
	}
	if err := decoder.Decode(&jobs); err != nil {
		t.Fatal(err)
	}

	if len(jobs.Release.Before) == 0 {
		t.Fatal("release bootstrap missing")
	}
	// Run its trusted-input binding before the network/bootstrap portion.
	prefix, _, found := strings.Cut(jobs.Release.Before[0], "expected=")
	if !found {
		t.Fatal("cannot locate engine bootstrap boundary")
	}

	for _, policy := range []string{"stable", "stable-prerelease"} {
		t.Run(policy, func(t *testing.T) {
			binding := strings.ReplaceAll(prefix, "$[[ inputs.release-policy ]]", policy)
			program := binding + `source "$1"
rc_engine() { printf 'selected=%s\n' "$RELEASE_POLICY"; }
rc_release_context
`
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "--noprofile", "--norc", "-c", program, "release-policy", filepath.Join(reporoot.Path(t), ".gitlab/bin/release.sh"))
			cmd.Dir = t.TempDir()
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + cmd.Dir, "CI_JOB_NAME=Check: release request", "CI_DEFAULT_BRANCH=main", "CI_COMMIT_TAG=release-request/v1.0.0", "CI_COMMIT_SHA=" + strings.Repeat("a", 40), "RC_RELEASE_POLICY=stable-prerelease", "RELEASE_POLICY=stable-prerelease"}

			output, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(output)) != "selected="+policy {
				t.Fatalf("trusted policy was shadowed: %v\n%s", err, output)
			}
		})
	}
}

func TestCentralPublisherBindsItsOwnReleasePolicy(t *testing.T) {
	scripts := publisherScripts(t, "publish-maven-central.yml")

	prefix, _, found := strings.Cut(scripts[0], "CI_OUTPUT=")
	if !found || !strings.Contains(prefix, "$[[ inputs.release-policy ]]") {
		t.Fatal("Central component has no trusted release-policy input binding")
	}

	for _, policy := range []string{"stable", "stable-prerelease"} {
		t.Run(policy, func(t *testing.T) {
			program := strings.ReplaceAll(prefix, "$[[ inputs.release-policy ]]", policy) + `printf 'selected=%s\n' "$RELEASE_POLICY"`
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "--noprofile", "--norc", "-c", program)
			cmd.Dir = t.TempDir()
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + cmd.Dir, "RELEASE_POLICY=stable-prerelease"}

			output, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(output)) != "selected="+policy {
				t.Fatalf("Central policy not honored: %v\n%s", err, output)
			}
		})
	}
}
