// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabbehavior

import (
	"reflect"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/gitlabrender"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// The comment job holds a token that may write to the merge request. It is
// safe because it handles nothing of the change: no checkout, the results
// file of the lint job only, and the token through an environment scope no
// other job of the component names. The lint job reads the change and
// refuses the token outright.
func TestPRCommentJobHoldsItsTokenAwayFromTheChange(t *testing.T) {
	t.Parallel()

	jobs := gitlabrender.Jobs(t, "templates/nanolinter.yml", map[string]any{"pr-comment": true})

	comment, lint := jobs["PR check results"], jobs["nanolinter"]
	if comment == nil || lint == nil {
		t.Fatalf("the component renders %d jobs, want nanolinter and PR check results", len(jobs))
	}

	variables, _ := comment["variables"].(map[string]any)
	if variables["GIT_STRATEGY"] != "empty" || variables["GIT_CHECKOUT"] != "false" {
		t.Errorf("the comment job checks the change out: %v", variables)
	}

	if want := map[string]any{"name": "pr/comment", "action": "prepare"}; !reflect.DeepEqual(comment["environment"], want) {
		t.Errorf("the comment job's environment is %v, want %v", comment["environment"], want)
	}

	if lint["environment"] != nil {
		t.Errorf("the lint job names an environment, %v, and would receive a scoped token", lint["environment"])
	}

	if want := []any{map[string]any{"job": "nanolinter", "artifacts": true}}; !reflect.DeepEqual(comment["needs"], want) {
		t.Errorf("the comment job needs %v, want the lint job's artifacts and nothing else", comment["needs"])
	}

	if comment["allow_failure"] != true {
		t.Error("a comment that cannot be posted must be a warning, not a failed pipeline")
	}

	rules, _ := comment["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("the comment job has %d rules, want one", len(rules))
	}

	rule, _ := rules[0].(map[string]any)
	if rule["if"] != `"true" == "true" && $CI_PIPELINE_SOURCE == "merge_request_event"` || rule["when"] != "always" {
		t.Errorf("the comment job runs on %v, want merge requests only, after a failed lint too", rule)
	}

	off := gitlabrender.Jobs(t, "templates/nanolinter.yml", map[string]any{})["PR check results"]
	if offRules, _ := off["rules"].([]any); len(offRules) != 1 ||
		!strings.HasPrefix(offRules[0].(map[string]any)["if"].(string), `"false" == "true"`) {
		t.Errorf("without pr-comment the job could still run: %v", off["rules"])
	}

	body := string(reporoot.ReadFile(t, "templates/nanolinter.yml"))
	for _, want := range []string{
		"PR_COMMENT_TOKEN must not be available to the lint job",
		"unset PR_COMMENT_TOKEN",
		`run_forge_ci report pr-comment --token-file -`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("templates/nanolinter.yml no longer has %q", want)
		}
	}

	if !strings.Contains(string(reporoot.ReadFile(t, "templates/check.yml")), "PR_COMMENT_TOKEN; do") {
		t.Error("the check job no longer refuses PR_COMMENT_TOKEN")
	}

	if !strings.Contains(string(reporoot.ReadFile(t, "templates/megalinter.yml")), "PR_COMMENT_TOKEN must not be available to the lint job") {
		t.Error("the MegaLinter job no longer refuses PR_COMMENT_TOKEN")
	}
}
