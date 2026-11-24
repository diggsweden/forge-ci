// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ownPipelinePrefix names the hosting repository's own pipelines in its
// workflow directory: the ones it starts itself and no consumer calls.
const ownPipelinePrefix = "self-"

// OwnPipeline reports whether a workflow file name, or a path ending in one,
// is one of the hosting repository's own pipelines (self-*.yml) rather than a
// reusable workflow of the layer.
func OwnPipeline(name string) bool {
	return strings.HasPrefix(path.Base(name), ownPipelinePrefix)
}

// WorkflowTriggers returns the events of a workflow's on:, in any of its
// scalar, list and mapping forms. A workflow that names none returns none.
func WorkflowTriggers(body []byte) ([]string, error) {
	var doc struct {
		On yaml.Node `yaml:"on"`
	}

	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse workflow yaml: %w", err)
	}

	switch doc.On.Kind {
	case yaml.ScalarNode:
		return []string{doc.On.Value}, nil
	case yaml.SequenceNode:
		triggers := make([]string, 0, len(doc.On.Content))
		for _, item := range doc.On.Content {
			triggers = append(triggers, item.Value)
		}

		return triggers, nil
	case yaml.MappingNode:
		triggers := make([]string, 0, len(doc.On.Content)/2)
		for i := 0; i < len(doc.On.Content); i += 2 {
			triggers = append(triggers, doc.On.Content[i].Value)
		}

		return triggers, nil
	default:
		return nil, nil
	}
}

// OwnPipelineMismatch reports why a workflow's name and triggers disagree,
// or "" when they agree. The forge calls only a workflow_call workflow, so
// that trigger is what makes a file part of the layer, and the name must say
// the same: a self-*.yml file has no workflow_call, every other file has it.
// A layer workflow whose workflow_call is replaced by another trigger would
// start in the hosting repository with its secrets; its name then fails it.
func OwnPipelineMismatch(name string, body []byte) string {
	triggers, err := WorkflowTriggers(body)
	if err != nil {
		return err.Error()
	}

	callable := slices.Contains(triggers, "workflow_call")

	switch {
	case OwnPipeline(name) && callable:
		return "a self-*.yml workflow is the repository's own pipeline and must not take workflow_call; rename it if consumers call it"
	case !OwnPipeline(name) && !callable:
		return "a workflow consumers do not call must be named self-*.yml; a layer workflow must take workflow_call"
	default:
		return ""
	}
}
