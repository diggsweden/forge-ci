// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPullRequestExamplesUseConsistentLabels(t *testing.T) {
	t.Parallel()

	for forge, pattern := range map[string]string{
		"github":  "github/pullrequest-workflow.yml",
		"forgejo": "forgejo/pullrequest-workflow.yml",
		"gitlab":  "gitlab/.gitlab-ci.yml",
	} {
		paths, err := filepath.Glob(filepath.Join(reporoot.Path(t), "examples", "*", pattern))
		require.NoError(t, err)
		require.NotEmpty(t, paths, forge)

		for _, path := range paths {
			var example struct {
				Jobs map[string]struct {
					Name string `yaml:"name"`
				} `yaml:"jobs"`
				Include []struct {
					Component string         `yaml:"component"`
					Inputs    map[string]any `yaml:"inputs"`
				} `yaml:"include"`
			}

			body, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, yaml.Unmarshal(body, &example), path)

			switch forge {
			case "github":
				require.Equal(t, "Quality checks", example.Jobs["pr-checks"].Name, path)
			case "forgejo":
				require.Equal(t, "Quality checks", example.Jobs["lint"].Name, path)
				require.Equal(t, "Verify release isolation", example.Jobs["release-gate"].Name, path)
				require.Equal(t, "PR check results", example.Jobs["summary"].Name, path)
			case "gitlab":
				names := make(map[string]bool)

				for _, include := range example.Include {
					name, ok := include.Inputs["job-name"].(string)
					if strings.Contains(include.Component, "/nanolinter@") {
						require.Equal(t, "Quality checks", name, path)
					}

					if ok {
						require.False(t, names[name], "%s: duplicate job name %q", path, name)
						names[name] = true
					}
				}

				require.True(t, names["Quality checks"], path)
			}
		}
	}
}
