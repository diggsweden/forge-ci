// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
)

// The isolation rule lets a release job that holds a secret run only the
// actions its list marks as running the engine alone, and refuses every one
// the list does not name. The list names every action of the directory, so
// a new action is classified before it ships. An action it allows calls no
// other action: a nested action would run code the list did not judge.
func TestIsolationClassifiesEveryLayerAction(t *testing.T) {
	t.Parallel()

	classified := validate.LayerActions()

	var actions []string

	for _, path := range layerActions(t) {
		name := strings.TrimSuffix(strings.TrimPrefix(path, ".forgejo/actions/"), "/action.yml")
		actions = append(actions, name)

		if !classified[name] {
			continue
		}

		for _, step := range actionSteps(t, path) {
			if uses := scalarString(mappingValue(step, "uses")); uses != "" {
				t.Errorf("%s may run in a job that holds a secret but calls %s", name, uses)
			}
		}
	}

	slices.Sort(actions)

	if listed := slices.Sorted(maps.Keys(classified)); !slices.Equal(listed, actions) {
		t.Errorf("the isolation rule classifies the actions %q; the directory holds %q: classify each new action in internal/domain/validate/layeractions.go", listed, actions)
	}
}
