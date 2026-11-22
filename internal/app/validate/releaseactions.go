// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// foreignActionViolations holds what the release runs to the repository and
// the subject: every uses: in the release workflows, in each local workflow
// they call and in each local action they use must be a path in the
// repository or a reference to the subject, and a local action must be a
// composite action. A third-party action, a docker:// image or a Node action
// in a release job is code that no reviewed commit of the repository or the
// engine pins. The local actions are followed in turn, and returned: their
// references to the subject are held to the pin and address checks too.
func foreignActionViolations(in IsolationInput, files []releaseFile) ([]releaseFile, []fileViolation) {
	root := repositoryRoot(in)
	seen := map[string]bool{}

	queue := append([]releaseFile(nil), files...)

	var (
		actions    []releaseFile
		violations []fileViolation
	)

	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]

		refs, err := yamlUsesRefs(file.Body)
		if err != nil {
			violations = append(violations, fileViolation{File: file.Path, Line: 1, Msg: err.Error()})

			continue
		}

		for _, ref := range refs {
			switch {
			case referencesSubject(ref.value, in.SinglePinSubject):
			case !strings.HasPrefix(ref.value, "./"):
				violations = append(violations, fileViolation{File: file.Path, Line: ref.line, Msg: fmt.Sprintf(
					"%s is neither a path in this repository nor %s: a release runs only the repository's own actions and the subject's", ref.value, in.SinglePinSubject)})
			case isYAMLFile(ref.value):
				// A local workflow: calledLocalWorkflows reads it.
			default:
				action, violation := localAction(in, root, ref)
				if violation != "" {
					violations = append(violations, fileViolation{File: file.Path, Line: ref.line, Msg: violation})
				} else if !seen[action.Path] {
					seen[action.Path] = true
					actions = append(actions, action)
					queue = append(queue, action)
				}
			}
		}
	}

	return actions, violations
}

// localAction reads the composite action a local uses: names, or says why it
// may not run in the release.
func localAction(in IsolationInput, root string, ref usesRef) (releaseFile, string) {
	rel := path.Clean(strings.TrimPrefix(ref.value, "./"))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return releaseFile{}, ref.value + " is outside the repository"
	}

	for _, name := range []string{"action.yml", "action.yaml"} {
		file := path.Join(root, rel, name)

		body, found, err := readRepositoryFile(in, file)
		if err != nil {
			return releaseFile{}, fmt.Sprintf("local action %s: %v", ref.value, err)
		}

		if !found {
			continue
		}

		var action struct {
			Runs struct {
				Using string `yaml:"using"`
			} `yaml:"runs"`
		}

		if err := yaml.Unmarshal(body, &action); err != nil {
			return releaseFile{}, fmt.Sprintf("local action %s: parse %s: %v", ref.value, file, err)
		}

		if action.Runs.Using != "composite" {
			return releaseFile{}, fmt.Sprintf("local action %s runs %q: a release runs composite actions only, never a Node or container action", ref.value, action.Runs.Using)
		}

		return releaseFile{Path: file, Body: body}, ""
	}

	return releaseFile{}, fmt.Sprintf("local action %s has no action.yml in the repository", ref.value)
}
