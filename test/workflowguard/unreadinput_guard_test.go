// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"gopkg.in/yaml.v3"
)

// An input a workflow, action or component declares but never reads is a
// setting that is silently ignored: the caller sets it, sees no error, and
// believes it took effect. Each declared input therefore has to be read
// somewhere in the same file. This does not prove the read is meaningful; it
// catches the declaration that nothing reads at all.

func TestDeclaredInputsAreRead(t *testing.T) {
	t.Parallel()

	var findings []string

	checked := 0

	check := func(path string, names []string, body string, read func(string) *regexp.Regexp) {
		checked += len(names)
		findings = append(findings, unreadInputs(path, names, body, read)...)
	}
	for _, dir := range []string{".github/workflows", ".forgejo/workflows"} {
		for _, path := range yamlFiles(t, dir) {
			body := reporoot.ReadFile(t, path)
			check(path, workflowInputNames(t, path, body), string(body), actionsInputRead)
		}
	}

	for _, dir := range []string{".github/actions", ".forgejo/actions"} {
		for _, entry := range reporoot.ReadDir(t, dir) {
			if !entry.IsDir() {
				continue
			}

			path := dir + "/" + entry.Name() + "/action.yml"
			body := reporoot.ReadFile(t, path)
			check(path, actionInputNames(t, path, body), string(body), actionsInputRead)
		}
	}

	for _, path := range yamlFiles(t, "templates") {
		names, rest := componentInputNames(t, path, reporoot.ReadFile(t, path))
		check(path, names, rest, componentInputRead)
	}

	if len(findings) > 0 {
		t.Fatalf("declared inputs nothing reads:\n  %s", strings.Join(findings, "\n  "))
	}

	if checked == 0 {
		t.Fatal("no declared input was found; this guard measured nothing")
	}
}

func TestUnreadInputs_ReportsOnlyTheUnreadOne(t *testing.T) {
	t.Parallel()

	body := "run: echo ${{ inputs.used }} ${{ inputs['quoted'] }}\n"

	got := unreadInputs("w.yml", []string{"used", "quoted", "ignored"}, body, actionsInputRead)
	if want := []string{"w.yml: ignored"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}

	got = unreadInputs("c.yml", []string{"job-name", "stage"}, `"$[[ inputs.job-name ]]":`, componentInputRead)
	if want := []string{"c.yml: stage"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func actionsInputRead(name string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(name)

	return regexp.MustCompile(`inputs(\.` + quoted + `\b|\[\s*['"]` + quoted + `['"]\s*\])`)
}

func componentInputRead(name string) *regexp.Regexp {
	return regexp.MustCompile(`\$\[\[\s*inputs\.` + regexp.QuoteMeta(name) + `\b`)
}

func unreadInputs(file string, names []string, body string, read func(string) *regexp.Regexp) []string {
	var findings []string

	for _, name := range names {
		// \b would accept a read of a longer name such as `name-suffix`.
		if !inputReadExactly(read(name), name, body) {
			findings = append(findings, file+": "+name)
		}
	}

	return findings
}

func inputReadExactly(pattern *regexp.Regexp, name, body string) bool {
	for _, loc := range pattern.FindAllStringIndex(body, -1) {
		end := loc[1]
		if end < len(body) && body[end] == '-' && strings.HasSuffix(body[loc[0]:end], name) {
			continue
		}

		return true
	}

	return false
}

func workflowInputNames(t *testing.T, path string, body []byte) []string {
	t.Helper()

	var doc struct {
		On yaml.Node `yaml:"on"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	var names []string

	if doc.On.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(doc.On.Content); i += 2 {
		trigger := doc.On.Content[i].Value
		if trigger != "workflow_call" && trigger != "workflow_dispatch" {
			continue
		}

		var declared struct {
			Inputs map[string]any `yaml:"inputs"`
		}
		if err := doc.On.Content[i+1].Decode(&declared); err != nil {
			t.Fatalf("%s: %s: %v", path, trigger, err)
		}

		for name := range declared.Inputs {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}

	slices.Sort(names)

	return names
}

func actionInputNames(t *testing.T, path string, body []byte) []string {
	t.Helper()

	var doc struct {
		Inputs map[string]any `yaml:"inputs"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	return inputKeys(doc.Inputs)
}

// componentInputNames returns the spec's inputs and the job document after
// the `---` that ends the spec header, where reads have to be.
func componentInputNames(t *testing.T, path string, body []byte) ([]string, string) {
	t.Helper()

	header, rest, found := bytes.Cut(body, []byte("\n---\n"))
	if !found {
		return nil, ""
	}

	var doc struct {
		Spec struct {
			Inputs map[string]any `yaml:"inputs"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(header, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	return inputKeys(doc.Spec.Inputs), string(rest)
}

func inputKeys(m map[string]any) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

func yamlFiles(t *testing.T, dir string) []string {
	t.Helper()

	var paths []string

	for _, entry := range reporoot.ReadDir(t, dir) {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".yaml")) {
			paths = append(paths, dir+"/"+entry.Name())
		}
	}

	return paths
}
