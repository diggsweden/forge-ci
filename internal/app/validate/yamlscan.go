// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/git"

	"gopkg.in/yaml.v3"
)

// yamlUsesValues returns the scalar value of every uses: key in the document,
// at any depth.
func yamlUsesValues(data []byte) ([]string, error) {
	refs, err := yamlUsesRefs(data)
	if err != nil {
		return nil, err
	}

	values := make([]string, 0, len(refs))
	for _, ref := range refs {
		values = append(values, ref.value)
	}

	return values, nil
}

// usesRef is the value of one uses: key and its line.
type usesRef struct {
	value string
	line  int
}

// yamlUsesRefs returns every uses: key in the document, at any depth, with
// the line of its value.
func yamlUsesRefs(data []byte) ([]usesRef, error) {
	doc, err := parseYAMLNode(data)
	if err != nil {
		return nil, err
	}

	var values []usesRef

	walkYAML(doc, map[*yaml.Node]bool{}, func(node *yaml.Node) {
		if node.Kind != yaml.MappingNode {
			return
		}

		// A YAML mapping node stores its children flat as key, value, key,
		// value, so the cursor advances two at a time.
		for pair := 0; pair+1 < len(node.Content); pair += 2 {
			mappingKey, ok := resolvedScalar(node.Content[pair])
			if !ok || mappingKey != "uses" {
				continue
			}

			if value, ok := resolvedScalar(node.Content[pair+1]); ok {
				values = append(values, usesRef{value: value, line: node.Content[pair+1].Line})
			}
		}
	})

	return values, nil
}

func parseYAMLNode(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse workflow YAML: %w", err)
	}

	return &doc, nil
}

func walkYAML(node *yaml.Node, seen map[*yaml.Node]bool, visit func(*yaml.Node)) {
	if node == nil || seen[node] {
		return
	}

	seen[node] = true

	visit(node)

	if node.Kind == yaml.AliasNode {
		walkYAML(node.Alias, seen, visit)

		return
	}

	for _, child := range node.Content {
		walkYAML(child, seen, visit)
	}
}

func resolvedScalar(node *yaml.Node) (string, bool) {
	seen := map[*yaml.Node]bool{}
	for node != nil && node.Kind == yaml.AliasNode && !seen[node] {
		seen[node] = true
		node = node.Alias
	}

	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}

	return node.Value, true
}

// pinnedSubjectSHA returns the full commit a uses: value pins subject to,
// or "" when the value does not name subject as its repository.
func pinnedSubjectSHA(uses, subject string) string {
	_, end, ok := subjectReference(uses, subject)
	if !ok {
		return ""
	}

	at := strings.LastIndexByte(uses[end:], '@')
	if at < 0 {
		return ""
	}

	if ref := uses[end+at+1:]; git.ValidCommitSHA(ref) {
		return ref
	}

	return ""
}

// referencesSubject reports whether a uses: value names subject as its
// repository, whatever it is pinned to.
func referencesSubject(uses, subject string) bool {
	_, _, ok := subjectReference(uses, subject)

	return ok
}

// subjectReference reports whether a uses: value names subject as its
// repository. It returns the forge address the value writes before the
// repository ("https://forge.example"), empty when the value leaves the
// address to the forge, and the index just past the subject.
//
// Forges name an owner and a repository without regard to case, so the
// subject matches in any case: ExampleScope/CI-Layer is the same engine.
//
// A value without an address starts with its repository, so the subject must
// start it. Finding the subject anywhere between two separators also took
// "prefix/org/ci/x@sha", a path in the repository prefix/org, for org/ci. A
// value with an address may carry a path before the repository, as a forge
// served below one does, so there the subject is looked for after the host
// and what precedes it is the address the caller compares.
func subjectReference(uses, subject string) (string, int, bool) {
	if subject == "" {
		return "", 0, false
	}

	scheme := strings.Index(uses, "://")
	if scheme < 0 {
		if hasPrefixFold(uses, subject) && endsComponent(uses, len(subject)) {
			return "", len(subject), true
		}

		return "", 0, false
	}

	for start := scheme + len("://"); ; {
		idx := indexFold(uses[start:], "/"+subject)
		if idx < 0 {
			return "", 0, false
		}

		idx += start
		if end := idx + 1 + len(subject); endsComponent(uses, end) {
			return uses[:idx], end, true
		}

		start = idx + 1
	}
}

// hasPrefixFold reports whether s starts with prefix, in any case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// indexFold is the index of the first occurrence of sub in s, in any case,
// or -1.
func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}

	return -1
}

// endsComponent reports whether a path component of uses ends at end: a "/"
// or the "@ref" separator follows.
func endsComponent(uses string, end int) bool {
	return end < len(uses) && (uses[end] == '/' || uses[end] == '@')
}
