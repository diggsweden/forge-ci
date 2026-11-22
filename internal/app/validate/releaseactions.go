// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	domaincontainer "github.com/diggsweden/forge-ci/internal/domain/container"
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

// variableImage is an image a job takes from one repository variable, as the
// layer's CI_JOB_IMAGE: a repository admin sets it, as they set the pin.
var variableImage = regexp.MustCompile(`^\$\{\{\s*vars\.[A-Za-z_][A-Za-z0-9_]*\s*\}\}$`)

// releaseImageViolations holds the images a release job runs in to what the
// actions are held to: a job's container image and each service image is a
// reference pinned by digest, written out, or the one repository variable
// that names it. A tag such as :latest, or an expression from an input or
// the event, runs an image no reviewed commit or repository admin chose, and
// the job's steps run inside it with what the job holds.
func releaseImageViolations(files []releaseFile) []fileViolation {
	var violations []fileViolation

	for _, file := range files {
		var doc struct {
			Jobs map[string]struct {
				Container yaml.Node            `yaml:"container"`
				Services  map[string]yaml.Node `yaml:"services"`
			} `yaml:"jobs"`
		}

		if err := yaml.Unmarshal(file.Body, &doc); err != nil {
			violations = append(violations, fileViolation{File: file.Path, Line: 1, Msg: fmt.Sprintf("parse workflow YAML: %v", err)})

			continue
		}

		for _, name := range slices.Sorted(maps.Keys(doc.Jobs)) {
			job := doc.Jobs[name]
			violations = append(violations, imageViolation(file.Path, fmt.Sprintf("job %q runs in", name), &job.Container)...)

			for _, service := range slices.Sorted(maps.Keys(job.Services)) {
				node := job.Services[service]
				violations = append(violations, imageViolation(file.Path, fmt.Sprintf("job %q starts service %q from", name, service), &node)...)
			}
		}
	}

	return violations
}

// imageViolation refuses the image of a container: or service entry, given
// as the image itself or as a mapping with an image: key, unless it is
// pinned by digest or taken from one repository variable.
func imageViolation(file, what string, node *yaml.Node) []fileViolation {
	image := node
	if resolved := resolveNode(node); resolved != nil && resolved.Kind == yaml.MappingNode {
		// Decoding applies YAML merge keys, which a walk of the raw pairs misses.
		var fields map[string]yaml.Node
		if err := resolved.Decode(&fields); err != nil {
			return []fileViolation{{File: file, Line: resolved.Line, Msg: fmt.Sprintf("%s an unreadable container or service entry: %v", what, err)}}
		}

		image = nil
		if field, ok := fields["image"]; ok {
			image = &field
		}
	}

	if image == nil || image.Kind == 0 {
		return nil
	}

	value, ok := resolvedScalar(image)
	if ok && (domaincontainer.ValidDigestReference(value) || variableImage.MatchString(value)) {
		return nil
	}

	return []fileViolation{{File: file, Line: image.Line, Msg: fmt.Sprintf(
		"%s image %s: a release job's container and service images are pinned by digest (@sha256:...) or taken from one repository variable (${{ vars.NAME }})", what, value)}}
}

// resolveNode follows an alias to the node it names.
func resolveNode(node *yaml.Node) *yaml.Node {
	seen := map[*yaml.Node]bool{}
	for node != nil && node.Kind == yaml.AliasNode && !seen[node] {
		seen[node] = true
		node = node.Alias
	}

	return node
}
