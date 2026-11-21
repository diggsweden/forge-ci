// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A job that calls a reusable workflow caps every job inside it: GitHub
// refuses the whole run at startup (startup_failure, no job log) when a
// called job asks for more than the caller granted. So each consumer caller of
// a forge-ci workflow, in the examples and in the demos, must grant at least
// what that workflow's job tree asks for. The hosted demo of 2026-10-16 found
// every pull-request example missing actions: read this way. Nor may it grant
// more: a scope the called jobs never ask for is a token right handed to code
// that does not need it, and a copied example spreads it. attestations: write
// is never asked for, since forge-ci signs with cosign.

// permissionCaller is the part of a workflow this guard reads.
type permissionCaller struct {
	Permissions any                          `yaml:"permissions"`
	Jobs        map[string]permissionCallJob `yaml:"jobs"`
}

type permissionCallJob struct {
	Permissions any    `yaml:"permissions"`
	Uses        string `yaml:"uses"`
}

const forgeCIWorkflowPrefix = "diggsweden/forge-ci/.github/workflows/"

func TestConsumerCallersGrantExactlyWhatTheCalledWorkflowsAsk(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	workflows := loadPermissionWorkflows(t, filepath.Join(root, ".github", "workflows"))

	var violations []string

	count := 0

	check := func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !isWorkflowFile(entry.Name()) || !isConsumerGitHubWorkflow(root, path) {
			return walkErr
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var caller permissionCaller
		if err := yaml.Unmarshal(body, &caller); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		relative, _ := filepath.Rel(root, path)

		// forge-ci signs with cosign, not the GitHub attestation API.
		if writesAttestations(body) {
			violations = append(violations, relative+": grants attestations: write, which no forge-ci workflow uses")
		}

		for jobName, job := range caller.Jobs {
			target, found := strings.CutPrefix(job.Uses, forgeCIWorkflowPrefix)
			if !found {
				continue
			}

			target, _, _ = strings.Cut(target, "@")
			count++

			required := requiredPermissions(target, workflows, nil)
			granted := permissionScopes(job.Permissions, caller.Permissions)

			for _, missing := range missingPermissions(required, granted) {
				violations = append(violations, fmt.Sprintf("%s: job %s calls %s without %s", relative, jobName, target, missing))
			}

			for _, excess := range missingPermissions(granted, required) {
				violations = append(violations, fmt.Sprintf("%s: job %s grants %s, which no job of %s asks for", relative, jobName, excess, target))
			}
		}

		return nil
	}

	for _, dir := range []string{"examples", filepath.Join("test", "live", "fixtures", "consumers")} {
		require.NoError(t, filepath.WalkDir(filepath.Join(root, dir), check))
	}

	require.NotZero(t, count, "no consumer calls a forge-ci workflow")

	slices.Sort(violations)
	require.Emptyf(t, violations, "consumer callers must grant what the called workflows ask, and no more; GitHub refuses a run that grants less at startup:\n  %s",
		strings.Join(violations, "\n  "))
}

// A consumer job that runs its own steps is named for its stage, as
// docs/workflows.md names job display names: the name is the status check an
// adopter requires, and an unnamed job shows only its id. A call is left
// unnamed: GitHub shows it as "<id> / <called job>", the called job's name.
func TestConsumerGitHubJobsAreNamedForTheirStage(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	stages := []string{"Check", "Prepare", "Build", "Sign", "Publish", "Summary"}

	var violations []string

	count := 0

	check := func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !isWorkflowFile(entry.Name()) || !isConsumerGitHubWorkflow(root, path) {
			return walkErr
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var workflow struct {
			Jobs map[string]struct {
				Name string `yaml:"name"`
				Uses string `yaml:"uses"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(body, &workflow); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		relative, _ := filepath.Rel(root, path)

		for id, job := range workflow.Jobs {
			if job.Uses != "" {
				continue
			}

			count++

			// The pull request's project job has the name it has on every forge.
			if job.Name == "Build and test" {
				continue
			}

			if stage, _, _ := strings.Cut(job.Name, ":"); !slices.Contains(stages, stage) {
				violations = append(violations, fmt.Sprintf("%s: job %s is named %q", relative, id, job.Name))
			}
		}

		return nil
	}

	for _, dir := range []string{"examples", filepath.Join("test", "live", "fixtures", "consumers")} {
		require.NoError(t, filepath.WalkDir(filepath.Join(root, dir), check))
	}

	require.NotZero(t, count, "no consumer job runs its own steps")

	slices.Sort(violations)
	require.Emptyf(t, violations, "a consumer job's name starts with its stage, one of %v, as in \"Check: scripts\", or is the pull request's \"Build and test\":\n  %s",
		stages, strings.Join(violations, "\n  "))
}

// isConsumerGitHubWorkflow reports whether path is a consumer's GitHub
// workflow: an example's examples/<name>/github/ file, or a demo's
// consumer/test/live/github*/workflows/ file.
func isConsumerGitHubWorkflow(root, path string) bool {
	dir := filepath.Dir(path)
	if relative, _ := filepath.Rel(root, dir); strings.HasPrefix(relative, "examples"+string(filepath.Separator)) {
		return filepath.Base(dir) == "github"
	}

	return filepath.Base(dir) == "workflows" && strings.HasPrefix(filepath.Base(filepath.Dir(dir)), "github")
}

// The same rule holds inside the engine: a job that calls another workflow of
// this repository caps what that workflow's jobs may ask, whatever the
// consumer granted above it. GitHub checks every such call before any job
// starts, a skipped one too.
func TestInternalCallersGrantWhatTheCalledWorkflowsAsk(t *testing.T) {
	t.Parallel()

	workflows := loadPermissionWorkflows(t, filepath.Join(reporoot.Path(t), ".github", "workflows"))

	var violations []string

	count := 0

	for name, workflow := range workflows {
		for jobName, job := range workflow.Jobs {
			target := internalWorkflowTarget(job.Uses)
			if target == "" {
				continue
			}

			count++

			granted := permissionScopes(job.Permissions, workflow.Permissions)
			for _, missing := range missingPermissions(requiredPermissions(target, workflows, nil), granted) {
				violations = append(violations, fmt.Sprintf("%s: job %s calls %s without %s", name, jobName, target, missing))
			}
		}
	}

	require.NotZero(t, count, "no workflow calls another")

	slices.Sort(violations)
	require.Emptyf(t, violations, "a calling job grants less than the called workflow asks; GitHub refuses such a run at startup:\n  %s",
		strings.Join(violations, "\n  "))
}

func TestRequiredPermissionsFollowsInternalCallsAndKeepsTheHigherLevel(t *testing.T) {
	t.Parallel()

	workflows := map[string]permissionCaller{
		"outer.yml": {Jobs: map[string]permissionCallJob{
			"plan":  {Permissions: map[string]any{"contents": "read"}},
			"inner": {Permissions: map[string]any{"contents": "read", "actions": "read"}, Uses: "./.github/workflows/inner.yml"},
		}},
		"inner.yml": {
			Permissions: map[string]any{"contents": "read"},
			Jobs: map[string]permissionCallJob{
				"lint":     {},
				"publish":  {Permissions: map[string]any{"contents": "write", "actions": "read"}},
				"external": {Uses: "other/repo/.github/workflows/x.yml@v1"},
			},
		},
	}

	required := requiredPermissions("outer.yml", workflows, nil)
	require.Equal(t, map[string]string{"contents": "write", "actions": "read"}, required)

	require.Equal(t, []string{"actions: read", "contents: write"},
		missingPermissions(required, map[string]string{"contents": "read"}))
	require.Empty(t, missingPermissions(required, map[string]string{"contents": "write", "actions": "write"}))
}

func loadPermissionWorkflows(t *testing.T, dir string) map[string]permissionCaller {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	workflows := map[string]permissionCaller{}

	for _, entry := range entries {
		if entry.IsDir() || !isWorkflowFile(entry.Name()) {
			continue
		}

		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)

		var workflow permissionCaller
		require.NoErrorf(t, yaml.Unmarshal(body, &workflow), "parse %s", entry.Name())

		workflows[entry.Name()] = workflow
	}

	return workflows
}

// requiredPermissions is the union, at the higher level, of what every job of
// a workflow asks for, through the workflows it calls in this repository. A
// job without permissions of its own asks for the workflow's.
func requiredPermissions(name string, workflows map[string]permissionCaller, seen map[string]bool) map[string]string {
	if seen == nil {
		seen = map[string]bool{}
	}

	required := map[string]string{}
	if seen[name] {
		return required
	}

	seen[name] = true
	workflow := workflows[name]

	for _, job := range workflow.Jobs {
		merge(required, permissionScopes(job.Permissions, workflow.Permissions))

		if target := internalWorkflowTarget(job.Uses); target != "" {
			merge(required, requiredPermissions(target, workflows, seen))
		}
	}

	return required
}

// permissionScopes reads a job's permissions map, or the workflow's when the
// job has none. Only the map form occurs in this repository.
func permissionScopes(job, workflow any) map[string]string {
	declared := job
	if declared == nil {
		declared = workflow
	}

	scopes := map[string]string{}

	if mapping, ok := declared.(map[string]any); ok {
		for scope, level := range mapping {
			scopes[scope], _ = level.(string)
		}
	}

	return scopes
}

func merge(into, from map[string]string) {
	for scope, level := range from {
		if permissionRank(level) > permissionRank(into[scope]) {
			into[scope] = level
		}
	}
}

func missingPermissions(required, granted map[string]string) []string {
	var missing []string

	for scope, level := range required {
		if permissionRank(granted[scope]) < permissionRank(level) {
			missing = append(missing, scope+": "+level)
		}
	}

	slices.Sort(missing)

	return missing
}

func permissionRank(level string) int {
	switch level {
	case "write":
		return 2
	case "read":
		return 1
	}

	return 0
}
