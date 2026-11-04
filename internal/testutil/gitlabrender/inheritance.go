// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabrender

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
)

var (
	errUnknownParent = errors.New("extends unknown")
	errExtendsCycle  = errors.New("extends cycle through")
	errInvalidParent = errors.New("extends must name a job")
)

// ResolveExtends merges a job over its parents in order. Hashes
// merge recursively; arrays and scalar values replace the inherited value.
func ResolveExtends(t *testing.T, body, job map[string]any) map[string]any {
	t.Helper()

	resolved, err := resolveExtends(body, job, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}

	return resolved
}

func resolveExtends(body, job map[string]any, visiting map[string]bool) (map[string]any, error) {
	resolved := map[string]any{}

	var parents []any

	switch extends := job["extends"].(type) {
	case nil:
	case string:
		parents = []any{extends}
	case []any:
		parents = extends
	default:
		return nil, errInvalidParent
	}

	for _, parent := range parents {
		name, named := parent.(string)
		if !named || name == "" {
			return nil, errInvalidParent
		}

		if visiting[name] {
			return nil, fmt.Errorf("%w %q", errExtendsCycle, name)
		}

		inherited, known := body[name].(map[string]any)
		if !known {
			return nil, fmt.Errorf("%w %q", errUnknownParent, name)
		}

		visiting[name] = true
		fields, err := resolveExtends(body, inherited, visiting)
		delete(visiting, name)

		if err != nil {
			return nil, err
		}

		mergeGitLabHash(resolved, fields)
	}

	mergeGitLabHash(resolved, job)
	delete(resolved, "extends")

	return resolved, nil
}

func mergeGitLabHash(into, from map[string]any) {
	for key, value := range from {
		inherited, isHash := into[key].(map[string]any)
		if override, ok := value.(map[string]any); ok && isHash {
			merged := maps.Clone(inherited)
			mergeGitLabHash(merged, override)
			into[key] = merged

			continue
		}

		into[key] = value
	}
}

// RulesAdmit evaluates the literal comparisons used by the image-stage tests,
// after input interpolation. It is not an evaluator for arbitrary GitLab rules.
func RulesAdmit(rules any) bool {
	list, _ := rules.([]any)
	if len(list) == 0 {
		return true
	}

	for _, raw := range list {
		rule, _ := raw.(map[string]any)
		expression, conditional := rule["if"].(string)

		if !conditional || literalRuleMatches(expression) {
			return rule["when"] != "never"
		}
	}

	return false
}

func literalRuleMatches(expression string) bool {
	for clause := range strings.SplitSeq(expression, "&&") {
		operator := "=="
		if strings.Contains(clause, "!=") {
			operator = "!="
		}

		sides := strings.SplitN(clause, operator, 2)
		if len(sides) != 2 {
			return false
		}

		left, right := strings.Trim(strings.TrimSpace(sides[0]), `'"`), strings.Trim(strings.TrimSpace(sides[1]), `'"`)

		if (left == right) != (operator == "==") {
			return false
		}
	}

	return true
}
