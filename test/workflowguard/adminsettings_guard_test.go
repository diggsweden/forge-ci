// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"gopkg.in/yaml.v3"
)

// The administrator's four enforcement settings reach the engine only as its
// command-line flags, from the inputs of the workflow, action or component
// that runs the gate. These guards hold every forge to that:
//
//   - every engine call that applies a setting passes it, so an accepted input
//     always reaches its gate;
//   - every caller of a reusable workflow or component that takes the
//     settings forwards them, so the administrator's top-level input reaches
//     the leaf;
//   - no value comes from a repository, project or organization variable, or
//     from an environment variable the engine would refuse.
//
// This repository's own pipelines (self-*.yml) call the reusable workflows
// directly and keep the enforcing defaults; examples and demos are consumer
// configuration.

var adminSettings = []string{"lint", "vulnerabilities", "application-tests", "reproducible-builds"}

// adminGates are the engine commands that apply each setting.
var adminGates = []struct {
	command  *regexp.Regexp
	settings []string
}{
	{regexp.MustCompile(`\bpolicy nanolinter\b`), []string{"lint", "vulnerabilities"}},
	{regexp.MustCompile(`\bpolicy tests\b`), []string{"application-tests"}},
	{regexp.MustCompile(`\bbuild (go|maven|npm|cargo|gradle|gradle-android) run\b`), []string{"application-tests"}},
	{regexp.MustCompile(`\blint swift (format-lint|swiftlint)\b`), []string{"lint"}},
	{regexp.MustCompile(`\bvalidate (prerequisites|jvm-reproducibility)\b`), []string{"reproducible-builds"}},
	{regexp.MustCompile(`\bsecurity scan container\b`), []string{"vulnerabilities"}},
	{regexp.MustCompile(`\bcontainer build(\s|$)`), []string{"vulnerabilities"}},
	{regexp.MustCompile(`\bcontainer snapshot-images build-artifact\b`), []string{"application-tests"}},
	{regexp.MustCompile(`\bcontainer snapshot-images publish\b`), []string{"vulnerabilities"}},
	{regexp.MustCompile(`\bcontainer release-images publish\b`), []string{"vulnerabilities"}},
	{regexp.MustCompile(`\bcontainer multiarch-image assemble\b.*--from-layouts\b`), []string{"vulnerabilities"}},
	{regexp.MustCompile(`\bcontainer build-push-oci-image\b`), []string{"vulnerabilities"}},
}

// engineCall is a command line that runs the engine, the way the layers do.
var engineCall = regexp.MustCompile(`(^|[\s|;&(])(forge-ci|run_forge_ci|rc_engine|"\$\{?FORGE_CI_BIN\}?")\s`)

type adminSource struct {
	path string
	// text is what runs: YAML values without input declarations and comment
	// lines, or a shell file without comments; continuation lines joined.
	text string
}

func adminSources(t *testing.T) []adminSource {
	t.Helper()

	var sources []adminSource

	add := func(path, text string) {
		sources = append(sources, adminSource{path: path, text: strings.ReplaceAll(text, "\\\n", " ")})
	}

	for _, dir := range []string{".github/workflows", ".forgejo/workflows"} {
		for _, path := range yamlFiles(t, dir) {
			if strings.HasPrefix(path[strings.LastIndex(path, "/")+1:], "self-") {
				continue
			}

			add(path, readableText(t, path, reporoot.ReadFile(t, path), isWorkflowInputDeclaration))
		}
	}

	for _, dir := range []string{".github/actions", ".forgejo/actions"} {
		for _, entry := range reporoot.ReadDir(t, dir) {
			if entry.IsDir() {
				path := dir + "/" + entry.Name() + "/action.yml"
				add(path, readableText(t, path, reporoot.ReadFile(t, path), isActionInputDeclaration))
			}
		}
	}

	for _, path := range yamlFiles(t, "templates") {
		_, rest, err := componentInputNames(reporoot.ReadFile(t, path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		add(path, readableText(t, path, rest, nil))
	}

	for _, path := range []string{".gitlab/bin/release.sh", ".gitlab/bin/snapshot-images.sh", ".forgejo/bin/snapshot-images.sh"} {
		var kept []string

		for line := range strings.SplitSeq(string(reporoot.ReadFile(t, path)), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				kept = append(kept, line)
			}
		}

		add(path, strings.Join(kept, "\n"))
	}

	return sources
}

// adminFlagsMissing returns the settings a gate call does not pass.
func adminFlagsMissing(line string, settings []string) []string {
	var missing []string

	for _, setting := range settings {
		if !strings.Contains(line, "--enforce-"+setting+"=") {
			missing = append(missing, setting)
		}
	}

	return missing
}

func TestEveryGateCallPassesTheAdministratorSettings(t *testing.T) {
	t.Parallel()

	var findings []string

	checked := 0

	for _, source := range adminSources(t) {
		for line := range strings.SplitSeq(source.text, "\n") {
			for _, gate := range adminGates {
				if !gate.command.MatchString(line) || !engineCall.MatchString(line) {
					continue
				}

				checked++

				if missing := adminFlagsMissing(line, gate.settings); len(missing) > 0 {
					findings = append(findings, fmt.Sprintf("%s: %q does not pass --enforce-%s", source.path, strings.TrimSpace(line), strings.Join(missing, ", --enforce-")))
				}
			}
		}
	}

	// The snapshot steps call the engine through snapshot_engine, which
	// passes the settings the caller's start assigned.
	snapshot := string(reporoot.ReadFile(t, ".forgejo/bin/snapshot-images.sh"))
	_, body, _ := strings.Cut(snapshot, "snapshot_engine() {")
	body, _, _ = strings.Cut(body, "\n}\n")

	if missing := adminFlagsMissing(strings.ReplaceAll(body, "\\\n", " "), []string{"application-tests", "vulnerabilities"}); len(missing) > 0 {
		findings = append(findings, ".forgejo/bin/snapshot-images.sh: snapshot_engine does not pass --enforce-"+strings.Join(missing, ", --enforce-"))
	}

	// The GitLab jobs that run caller-listed commands put a forge-ci first
	// on PATH that carries all four settings, assigned from the inputs.
	for _, path := range []string{"templates/check.yml", "templates/release-jobs.yml"} {
		want := `forge_ci_admin_path "${RC_WORK}/admin-bin" "${RC_ADMIN_LINT}" "${RC_ADMIN_VULNERABILITIES}"  "${RC_ADMIN_APPLICATION_TESTS}" "${RC_ADMIN_REPRODUCIBLE_BUILDS}"`
		if path == "templates/check.yml" {
			want = strings.Replace(want, "RC_WORK", "RC_HELPERS_DIR", 1)
		}

		joined := strings.Join(strings.Fields(strings.ReplaceAll(string(reporoot.ReadFile(t, path)), "\\\n", " ")), " ")
		if !strings.Contains(joined, strings.Join(strings.Fields(want), " ")) {
			findings = append(findings, path+": the commands' forge-ci does not carry the four administrator settings")
		}
	}

	if len(findings) > 0 {
		t.Fatalf("administrator settings that do not reach their gate:\n  %s", strings.Join(findings, "\n  "))
	}

	if checked < 20 {
		t.Fatalf("only %d gate calls were found; this guard measured nothing", checked)
	}
}

// A carrier of a setting, such as a step's ADMIN_LINT, is bound to the input
// of the same setting, and every --enforce-* value names its own setting's
// input or carrier: no variable, no other setting, no literal.
func TestAdministratorSettingsComeOnlyFromTheirInputs(t *testing.T) {
	t.Parallel()

	envName := map[string]string{}
	for _, setting := range adminSettings {
		envName[setting] = "ADMIN_" + strings.ToUpper(strings.ReplaceAll(setting, "-", "_"))
	}

	binding := regexp.MustCompile(`^\s*(?:readonly\s+)?([A-Z0-9_]*ADMIN_(LINT|VULNERABILITIES|APPLICATION_TESTS|REPRODUCIBLE_BUILDS))[:=]\s*(.*)$`)
	flag := regexp.MustCompile(`--enforce-([a-z-]+)=`)
	refused := regexp.MustCompile(`(?i)\b([a-z0-9_]*_)?enforce_(lint|vulnerabilities|application_tests|reproducible_builds)\b`)
	variable := regexp.MustCompile(`(?i)vars(\.|\[)[^}\]]*enforce`)

	var findings []string

	files := slices.Concat(yamlFiles(t, ".github/workflows"), yamlFiles(t, ".forgejo/workflows"), yamlFiles(t, "templates"),
		[]string{".gitlab/bin/release.sh", ".gitlab/bin/snapshot-images.sh", ".forgejo/bin/snapshot-images.sh", ".engine/bin/lib-forge-ci.sh"})

	for _, dir := range []string{".github/actions", ".forgejo/actions"} {
		for _, entry := range reporoot.ReadDir(t, dir) {
			if entry.IsDir() {
				files = append(files, dir+"/"+entry.Name()+"/action.yml")
			}
		}
	}

	for _, path := range files {
		for number, line := range strings.Split(string(reporoot.ReadFile(t, path)), "\n") {
			at := fmt.Sprintf("%s:%d", path, number+1)

			if match := refused.FindString(line); match != "" {
				findings = append(findings, at+": "+match+" is a name the engine refuses")
			}

			if variable.MatchString(line) {
				findings = append(findings, at+": a setting read from a variable")
			}

			if match := binding.FindStringSubmatch(line); match != nil {
				setting := strings.ToLower(strings.ReplaceAll(match[2], "_", "-"))
				value := strings.TrimSpace(match[3])
				inputs := []string{
					"${{ inputs['enforce-" + setting + "'] }}", "${{ inputs.enforce-" + setting + " }}",
					"'$[[ inputs.enforce-" + setting + " ]]'",
				}

				if !slices.ContainsFunc(inputs, func(input string) bool { return value == input || strings.HasPrefix(value, input+" ") }) {
					findings = append(findings, fmt.Sprintf("%s: %s is bound to %q, not the enforce-%s input", at, match[1], value, setting))
				}
			}

			for _, match := range flag.FindAllStringSubmatchIndex(line, -1) {
				setting, rest := line[match[2]:match[3]], line[match[1]:]
				value, _, _ := strings.Cut(rest, " ")

				if path == ".engine/bin/lib-forge-ci.sh" && value == "%s" {
					continue
				}

				if !strings.Contains(value, envName[setting]) && !strings.HasPrefix(rest, "'$[[ inputs.enforce-"+setting+" ]]'") {
					findings = append(findings, fmt.Sprintf("%s: --enforce-%s takes %s, not its own input", at, setting, value))
				}
			}
		}
	}

	if len(findings) > 0 {
		t.Fatalf("administrator settings from another source:\n  %s", strings.Join(findings, "\n  "))
	}
}

// On GitLab every project, group and instance variable is in the job's
// environment, so a shell value a component reads must be assigned from its
// input in the job's own script, or a variable of that name would supply it.
// Each carrier a template, or a library its jobs source, reads is assigned
// read-only from the input of its setting in the job start.
func TestGitLabCarriersAreAssignedFromTheirInputs(t *testing.T) {
	t.Parallel()

	read := regexp.MustCompile(`\$\{?((RC|SNAPSHOT)_ADMIN_[A-Z_]+)`)
	assigned := regexp.MustCompile(`\b((RC|SNAPSHOT)_ADMIN_([A-Z_]+))='\$\[\[ inputs\.enforce-([a-z-]+) \]\]'`)

	assignments := func(path string) map[string]bool {
		names := map[string]bool{}

		for _, match := range assigned.FindAllStringSubmatch(string(reporoot.ReadFile(t, path)), -1) {
			if strings.ToLower(strings.ReplaceAll(match[3], "_", "-")) == match[4] {
				names[match[1]] = true
			}
		}

		return names
	}

	var findings []string

	checked := 0

	require := func(reader, start string) {
		names := assignments(start)

		for _, match := range read.FindAllStringSubmatch(string(reporoot.ReadFile(t, reader)), -1) {
			checked++

			if !names[match[1]] {
				findings = append(findings, fmt.Sprintf("%s reads %s, which %s does not assign from its input", reader, match[1], start))
			}
		}
	}

	for _, path := range yamlFiles(t, "templates") {
		require(path, path)
	}

	require(".forgejo/bin/snapshot-images.sh", "templates/snapshot-image-jobs.yml")
	require(".gitlab/bin/snapshot-images.sh", "templates/snapshot-image-jobs.yml")

	if len(findings) > 0 {
		t.Fatalf("GitLab carriers a variable could supply:\n  %s", strings.Join(findings, "\n  "))
	}

	if checked < 10 {
		t.Fatalf("only %d carrier reads were found; this guard measured nothing", checked)
	}
}

// A reusable workflow or component that takes a setting is called with it:
// each caller declares the setting and forwards its own input, so the value
// the administrator gives the top-level workflow reaches the leaf.
func TestAdministratorSettingsAreForwardedToEveryCallee(t *testing.T) {
	t.Parallel()

	var findings []string

	declared := map[string][]string{}

	for _, path := range yamlFiles(t, ".github/workflows") {
		declared[path] = adminInputsOf(workflowInputNames(t, path, reporoot.ReadFile(t, path)))
	}

	forwarded := 0

	for _, path := range yamlFiles(t, ".github/workflows") {
		if strings.HasPrefix(path[strings.LastIndex(path, "/")+1:], "self-") {
			continue
		}

		var workflow struct {
			Jobs map[string]struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(reporoot.ReadFile(t, path), &workflow); err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		for name, job := range workflow.Jobs {
			callee := strings.TrimPrefix(job.Uses, "./")
			for _, setting := range declared[callee] {
				forwarded++

				want := []string{"${{ inputs['enforce-" + setting + "'] }}", "${{ inputs.enforce-" + setting + " }}"}
				if !slices.Contains(want, job.With["enforce-"+setting]) {
					findings = append(findings, fmt.Sprintf("%s job %s: calls %s without forwarding enforce-%s", path, name, callee, setting))
				}

				if !slices.Contains(declared[path], setting) {
					findings = append(findings, fmt.Sprintf("%s: calls %s but does not take enforce-%s", path, callee, setting))
				}
			}
		}
	}

	// GitLab: a component that includes another one forwards its own input.
	include := regexp.MustCompile(`(?m)^\s+file: /templates/([a-z0-9-]+\.yml)$`)

	for _, path := range yamlFiles(t, "templates") {
		body := string(reporoot.ReadFile(t, path))

		own, _, err := componentInputNames([]byte(body))
		if err != nil {
			t.Fatal(err)
		}

		for _, match := range include.FindAllStringSubmatch(body, -1) {
			callee, _, err := componentInputNames(reporoot.ReadFile(t, "templates/"+match[1]))
			if err != nil {
				t.Fatal(err)
			}

			for _, setting := range adminInputsOf(callee) {
				forwarded++

				if !strings.Contains(body, "enforce-"+setting+": $[[ inputs.enforce-"+setting+" ]]") || !slices.Contains(own, "enforce-"+setting) {
					findings = append(findings, fmt.Sprintf("%s: includes %s without forwarding enforce-%s", path, match[1], setting))
				}
			}
		}
	}

	if len(findings) > 0 {
		t.Fatalf("administrator settings a caller drops:\n  %s", strings.Join(findings, "\n  "))
	}

	if forwarded < 20 {
		t.Fatalf("only %d forwarded settings were checked; this guard measured nothing", forwarded)
	}
}

func adminInputsOf(names []string) []string {
	var settings []string

	for _, setting := range adminSettings {
		if slices.Contains(names, "enforce-"+setting) {
			settings = append(settings, setting)
		}
	}

	return settings
}
