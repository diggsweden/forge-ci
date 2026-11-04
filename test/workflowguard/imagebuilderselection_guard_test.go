// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard_test

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// The trees that ship workflows, actions, templates, release scripts, demos
// and consumer fixtures.
var shippedCITrees = []string{".forgejo", ".github", ".gitlab", "templates", "examples", "test/live/demos", "test/live/consumer-fixtures"}

// The demos and examples, which a consumer copies: what they hard-wire, the
// consumer does too.
var demoTrees = []string{"examples", "test/live/demos"}

// buildahHardWired names Buildah directly: the Forgejo setup-buildah action,
// the engine's setup-buildah verb, or the bootstrap installer.
var buildahHardWired = regexp.MustCompile(`/setup-buildah@|/setup-buildah\b\s*$|container setup-buildah\b|install-buildah\.sh`)

// buildahAllowed is every place that may name Buildah directly, and why: the
// builder selection itself, which prepares Buildah when CI_IMAGE_BUILDER
// selects it, and the paths that are Buildah's by design.
var buildahAllowed = map[string]string{
	".forgejo/actions/setup-buildah/action.yml":                   "the Buildah preparation itself",
	".forgejo/actions/setup-image-builder/action.yml":             "the Forgejo builder selection: prepares Buildah when CI_IMAGE_BUILDER selects it",
	".forgejo/bin/snapshot-images.sh":                             "snapshot_build, the Forgejo snapshot builder selection",
	".gitlab/bin/release.sh":                                      "rc_image_builder, the GitLab release's builder selection",
	".github/workflows/publish-container.yml":                     "GitHub, Buildah only: its prep job refuses another CI_IMAGE_BUILDER",
	".github/workflows/snapshot-images.yml":                       "GitHub, Buildah only: its plan job refuses another CI_IMAGE_BUILDER",
	".github/workflows/self-release.yml":                          "forge-ci's own release on GitHub's runners, no consumer workflow",
	".github/workflows/self-runtime-container.yml":                "forge-ci's own runtime images on GitHub's runners, no consumer workflow",
	"test/live/consumer-fixtures/generic-cli/forgejo-release.yml": "the Go lane's checks are Buildah's own integration tests",
}

// githubBuildahOnly are the allowed GitHub workflows that consumers call: each
// refuses another selected builder early rather than ignore it.
var githubBuildahOnly = []string{".github/workflows/publish-container.yml", ".github/workflows/snapshot-images.yml"}

// hardCodedArch is a container step's literal platform or architecture: a
// demo follows the release's selected platforms instead.
var hardCodedArch = regexp.MustCompile(`--platform[= ]+["']?linux/|--arch[= ]+["']?(amd64|arm64)\b|release-platforms:\s*["']?linux/`)

// A container build must go through the builder the organization selects.
// A shipped step that names Buildah directly ignores CI_IMAGE_BUILDER (the
// cargo-container demo's export did, and kaniko was then refused there), so
// only the builder selection and the documented Buildah-only paths may.
func TestContainerBuildsUseTheSelectedImageBuilder(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	seen := map[string]bool{}

	for _, line := range shippedCILines(t, root, shippedCITrees) {
		if !buildahHardWired.MatchString(line.text) {
			continue
		}

		seen[line.path] = true

		if _, allowed := buildahAllowed[line.path]; !allowed {
			t.Errorf("%s:%d names Buildah directly (%s); prepare the builder with setup-image-builder, in the job image CI_IMAGE_BUILDER_JOB_IMAGE names, so CI_IMAGE_BUILDER has its effect", line.path, line.number, strings.TrimSpace(line.text))
		}
	}

	for path, reason := range buildahAllowed {
		if !seen[path] {
			t.Errorf("%s is allowed to name Buildah (%s) but no longer does; drop it from the allowlist", path, reason)
		}
	}

	for _, path := range githubBuildahOnly {
		body, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)

		if !strings.Contains(string(body), "name: Require the Buildah image builder") || !strings.Contains(string(body), "CI_IMAGE_BUILDER: ${{ vars.CI_IMAGE_BUILDER }}") {
			t.Errorf("%s builds with Buildah only and must refuse another CI_IMAGE_BUILDER before it builds", path)
		}
	}
}

// A demo or example builds for the platforms its release selects, one native
// job per architecture, never for one it names itself.
func TestDemoContainerStepsFollowTheSelectedPlatforms(t *testing.T) {
	t.Parallel()

	for _, line := range shippedCILines(t, reporoot.Path(t), demoTrees) {
		if hardCodedArch.MatchString(line.text) {
			t.Errorf("%s:%d hard-codes a platform (%s); take it from RELEASE_PLATFORMS (release-platforms), a native job per architecture", line.path, line.number, strings.TrimSpace(line.text))
		}
	}
}

// A Forgejo demo or example job that builds a container prepares the
// selected builder, and a GitLab demo's build job builds none: its export
// jobs do, with the policy's image builder.
func TestDemoContainerBuildJobsPrepareTheSelectedBuilder(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	builds := 0

	for _, base := range demoTrees {
		require.NoError(t, filepath.WalkDir(filepath.Join(root, base), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}

			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)

			if strings.HasSuffix(rel, "/.gitlab/build-release.sh") {
				body, readErr := os.ReadFile(path)
				require.NoError(t, readErr)

				if strings.Contains(string(body), "forge-ci container build ") {
					t.Errorf("%s runs a container build in the build job, which has no image builder to select; move it to .gitlab/export-release.sh, which the policy's export jobs run", rel)
				}

				return nil
			}

			if filepath.Ext(path) != ".yml" || !strings.Contains(rel, "forgejo") {
				return nil
			}

			body, readErr := os.ReadFile(path)
			require.NoError(t, readErr)

			var workflow struct {
				Jobs map[string]struct {
					Container any `yaml:"container"`
					Steps     []struct {
						Uses string `yaml:"uses"`
						Run  string `yaml:"run"`
					} `yaml:"steps"`
				} `yaml:"jobs"`
			}
			require.NoError(t, yaml.Unmarshal(body, &workflow), rel)

			for name, job := range workflow.Jobs {
				builds += checkDemoBuildJob(t, rel+" job "+name, job.Container, job.Steps)
			}

			return nil
		}))
	}

	require.Positive(t, builds, "no demo job builds a container; the guard checks nothing")
}

func checkDemoBuildJob(t *testing.T, where string, container any, steps []struct {
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
},
) int {
	t.Helper()

	builds := slices.ContainsFunc(steps, func(step struct {
		Uses string `yaml:"uses"`
		Run  string `yaml:"run"`
	},
	) bool {
		return strings.Contains(step.Run, "forge-ci container build ") || strings.Contains(step.Run, "multiarch-image build-arch")
	})
	if !builds {
		return 0
	}

	prepared := slices.ContainsFunc(steps, func(step struct {
		Uses string `yaml:"uses"`
		Run  string `yaml:"run"`
	},
	) bool {
		return strings.Contains(step.Uses, "/setup-image-builder")
	})

	image, _ := container.(string)
	if !prepared || !strings.Contains(image, "builder_job_image") && !strings.Contains(image, "CI_IMAGE_BUILDER_JOB_IMAGE") {
		t.Errorf("%s builds a container without the selected builder: it needs a setup-image-builder step and, as its container, the builder's job image (empty for Buildah)", where)
	}

	return 1
}

type ciLine struct {
	path   string
	number int
	text   string
}

// shippedCILines returns every non-comment line of the YAML and shell files
// under the trees.
func shippedCILines(t *testing.T, root string, trees []string) []ciLine {
	t.Helper()

	var lines []ciLine

	for _, base := range trees {
		require.NoError(t, filepath.WalkDir(filepath.Join(root, base), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}

			switch filepath.Ext(path) {
			case ".yml", ".yaml", ".sh":
			default:
				return nil
			}

			rel, _ := filepath.Rel(root, path)
			body, readErr := os.ReadFile(path)
			require.NoError(t, readErr)

			scanner := bufio.NewScanner(bytes.NewReader(body))
			for number := 1; scanner.Scan(); number++ {
				if text := scanner.Text(); !strings.HasPrefix(strings.TrimSpace(text), "#") {
					lines = append(lines, ciLine{path: filepath.ToSlash(rel), number: number, text: text})
				}
			}

			return scanner.Err()
		}))
	}

	return lines
}
