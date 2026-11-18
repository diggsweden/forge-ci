// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"gopkg.in/yaml.v3"
)

// A caller forwards its changelog configuration input whether it is set or
// not, so a callee's default never applies once a caller is in between: a
// non-empty default on the way was dead, and an empty one reached git-cliff
// as --config "". The bundled default is therefore stated once, where
// git-cliff runs, and names a template the runtime image copies to /opt.
func TestChangelogConfigDefaultsOnlyWhereGitCliffRuns(t *testing.T) {
	t.Parallel()

	const bundled = "/opt/forge-ci/templates/gitcliff/default.toml"

	root := reporoot.Path(t)

	entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		var workflow struct {
			On struct {
				WorkflowCall struct {
					Inputs map[string]struct {
						Default string `yaml:"default"`
					} `yaml:"inputs"`
				} `yaml:"workflow_call"`
			} `yaml:"on"`
		}
		if err := yaml.Unmarshal(reporoot.ReadFile(t, ".github/workflows/"+entry.Name()), &workflow); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}

		for _, name := range []string{"changelog-config", "changelog.config"} {
			if input, ok := workflow.On.WorkflowCall.Inputs[name]; ok && input.Default != "" {
				t.Errorf("%s input %s defaults to %q; leave it empty so generate-changelog's default applies", entry.Name(), name, input.Default)
			}
		}
	}

	env := workflowStepEnv(t, "generate-changelog.yml", "generate-changelog", "Generate Changelog with git-cliff")
	if want := "${{ inputs['changelog-config'] || '" + bundled + "' }}"; env["CONFIG"] != want {
		t.Errorf("git-cliff CONFIG = %q, want %q", env["CONFIG"], want)
	}

	if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(bundled, "/opt/forge-ci/"))); err != nil {
		t.Errorf("bundled changelog default is not in templates/: %v", err)
	}
}
