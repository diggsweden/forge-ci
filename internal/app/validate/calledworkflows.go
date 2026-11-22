// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"gopkg.in/yaml.v3"
)

// fileViolation is a violation in another file than the checked workflow,
// annotated at that file.
type fileViolation struct {
	File string
	Line int
	Msg  string
}

// releaseFile is a workflow or action file of the repository, by its path.
type releaseFile struct {
	Path string
	Body []byte
}

// calledLocalWorkflows returns the local reusable workflows the release
// workflows call, directly or through another local workflow, each once and
// none of the release workflows themselves, and the violations of reading
// them. A local workflow runs in the release with what its caller hands it,
// so the release-isolation rules hold for its jobs as for the caller's.
func calledLocalWorkflows(in IsolationInput, releases []releaseFile) ([]releaseFile, []fileViolation) {
	root := repositoryRoot(in)
	seen := map[string]bool{}

	for _, release := range releases {
		seen[path.Clean(release.Path)] = true
	}

	queue := slices.Clone(releases)

	var (
		called     []releaseFile
		violations []fileViolation
	)

	for len(queue) > 0 {
		caller := queue[0]
		queue = queue[1:]

		calls, err := localWorkflowCalls(caller.Body)
		if err != nil {
			violations = append(violations, fileViolation{File: caller.Path, Line: 1, Msg: err.Error()})

			continue
		}

		for _, call := range calls {
			rel := path.Clean(strings.TrimPrefix(call.uses, "./"))
			if rel == ".." || strings.HasPrefix(rel, "../") {
				violations = append(violations, fileViolation{File: caller.Path, Line: call.line, Msg: fmt.Sprintf("job %q calls %s, which is outside the repository", call.job, call.uses)})

				continue
			}

			name := path.Join(root, rel)
			if seen[name] {
				continue
			}

			seen[name] = true

			body, found, err := readRepositoryFile(in, name)

			switch {
			case err != nil:
				violations = append(violations, fileViolation{File: caller.Path, Line: call.line, Msg: fmt.Sprintf("job %q calls %s: %v", call.job, call.uses, err)})
			case !found:
				violations = append(violations, fileViolation{File: caller.Path, Line: call.line, Msg: fmt.Sprintf("job %q calls %s, which the repository does not have: a local workflow the release runs must be checked with it", call.job, call.uses)})
			default:
				file := releaseFile{Path: name, Body: body}
				called = append(called, file)
				queue = append(queue, file)
			}
		}
	}

	return called, violations
}

// calledWorkflowViolations applies the per-job release-isolation rules to
// each called local workflow: no signing secret, no secrets: inherit, no
// persisted checkout credentials and no toolchain cache.
func calledWorkflowViolations(in IsolationInput, called []releaseFile) []fileViolation {
	var violations []fileViolation

	for _, file := range called {
		found, err := validate.CheckIsolation(file.Body, validate.IsolationConfig{SigningSecrets: in.SigningSecrets})
		if err != nil {
			violations = append(violations, fileViolation{File: file.Path, Line: 1, Msg: fmt.Sprintf("check isolation %s: %v", file.Path, err)})

			continue
		}

		for _, v := range found {
			violations = append(violations, fileViolation{File: file.Path, Line: v.Line, Msg: v.Msg})
		}
	}

	return violations
}

// localWorkflowCall is a job that calls a reusable workflow by a path in
// the repository.
type localWorkflowCall struct {
	job  string
	uses string
	line int
}

// localWorkflowCalls returns the jobs of a workflow that call a reusable
// workflow of the same repository, sorted by job name.
func localWorkflowCalls(body []byte) ([]localWorkflowCall, error) {
	var doc struct {
		Jobs map[string]struct {
			Uses yaml.Node `yaml:"uses"`
		} `yaml:"jobs"`
	}

	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse workflow YAML: %w", err)
	}

	var calls []localWorkflowCall

	for _, name := range slices.Sorted(maps.Keys(doc.Jobs)) {
		job := doc.Jobs[name]

		uses, ok := resolvedScalar(&job.Uses)
		if !ok || !strings.HasPrefix(uses, "./") || !isYAMLFile(uses) {
			continue
		}

		calls = append(calls, localWorkflowCall{job: name, uses: uses, line: job.Uses.Line})
	}

	return calls, nil
}
