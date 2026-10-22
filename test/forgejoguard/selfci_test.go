// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// Forks register their own self-CI here, alongside adding the workflow files.
// Upstream serves only reusable Forgejo workflows, so its registry is empty.
// Example entry: ".forgejo/workflows/pullrequest.yml": {"pull_request"}.
func selfCIEvents() map[string][]string {
	return map[string][]string{}
}

func isSelfCIWorkflow(relative string) bool {
	_, registered := selfCIEvents()[relative]

	return registered
}

func selfCIRegistrationViolation(relative string, events []string) string {
	if path.Dir(relative) != ".forgejo/workflows" || path.Clean(relative) != relative ||
		strings.ContainsAny(relative, "*?[]\\") || !slices.Contains([]string{".yml", ".yaml"}, path.Ext(relative)) {
		return "self-CI must name one workflow file, not a pattern or another directory"
	}

	if _, reusable := coordinateWorkflows()[relative]; reusable {
		return "a reusable layer workflow cannot be registered as self-CI"
	}

	if len(events) == 0 || slices.Contains(events, "workflow_call") {
		return "self-CI must declare its own events, without workflow_call"
	}

	return ""
}

func selfCIEventViolation(data []byte, want []string) string {
	root, err := parseDocument(data)
	if err != nil {
		return err.Error()
	}

	entries := mappingEntries(mappingValue(root, "on"))
	events := make([]string, 0, len(entries))

	for _, entry := range entries {
		events = append(events, scalarString(entry.Key))
	}

	slices.Sort(events)

	expected := slices.Clone(want)
	slices.Sort(expected)

	if len(expected) == 0 || slices.Contains(events, "workflow_call") || !slices.Equal(events, expected) {
		return "self-CI events must match the registered events exactly"
	}

	return ""
}

func TestSelfCIWorkflowsAreExplicitAndRunOnTheRepositoryRunner(t *testing.T) {
	t.Parallel()

	discovered := allWorkflows(t)

	for relative, events := range selfCIEvents() {
		if violation := selfCIRegistrationViolation(relative, events); violation != "" {
			t.Errorf("%s: %s", relative, violation)

			continue
		}

		if !slices.Contains(discovered, relative) {
			t.Errorf("registered self-CI workflow %s was not discovered", relative)

			continue
		}

		if violation := selfCIEventViolation(reporoot.ReadFile(t, relative), events); violation != "" {
			t.Errorf("%s: %s", relative, violation)
		}

		for name, target := range loadWorkflow(t, relative).jobs {
			if violation := routingViolation(target); violation != "" {
				t.Errorf("%s job %s: %s", relative, name, violation)
			}
		}
	}
}

func TestSelfCIRegistrationRejectsBroadOrLayerExemptions(t *testing.T) {
	t.Parallel()

	for _, relative := range []string{
		".forgejo/workflows/*.yml", ".forgejo/workflows/../other.yml",
		".forgejo/actions/action.yml", ".forgejo/workflows/prepare-release.yml",
		".forgejo/workflows/release.txt",
	} {
		if selfCIRegistrationViolation(relative, []string{"push"}) == "" {
			t.Errorf("accepted invalid self-CI registration %q", relative)
		}
	}

	for _, events := range [][]string{nil, {"workflow_call"}, {"push", "workflow_call"}} {
		if selfCIRegistrationViolation(".forgejo/workflows/self.yml", events) == "" {
			t.Errorf("accepted invalid events %v", events)
		}
	}

	if violation := selfCIRegistrationViolation(".forgejo/workflows/self.yml", []string{"push"}); violation != "" {
		t.Fatal(violation)
	}
}

func TestSelfCIEventsRejectUnregisteredTriggers(t *testing.T) {
	t.Parallel()

	for _, document := range []string{
		"on: push\n", "on:\n  push:\n  pull_request:\n",
		"on:\n  workflow_call:\n", "jobs: {}\n",
	} {
		if selfCIEventViolation([]byte(document), []string{"push"}) == "" {
			t.Errorf("accepted unregistered events in %q", document)
		}
	}

	if violation := selfCIEventViolation([]byte("on:\n  push:\n"), []string{"push"}); violation != "" {
		t.Fatal(violation)
	}
}
