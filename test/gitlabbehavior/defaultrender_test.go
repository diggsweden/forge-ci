// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabbehavior

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// requiredInputFixtures gives each input a component requires, one without a
// default, the value a consumer could pass. Every other input renders with its
// default.
var requiredInputFixtures = map[string]any{
	"forge-ci-repository":              "team/engine",
	"consumer-project":                 "team/app",
	"server-url":                       "https://gitlab.invalid",
	"registry":                         "registry.gitlab.invalid",
	"job-image":                        "registry.invalid/team/job@sha256:" + strings.Repeat("1", 64),
	"build-image":                      "registry.invalid/team/build@sha256:" + strings.Repeat("2", 64),
	"signer-image":                     "registry.invalid/team/signer@sha256:" + strings.Repeat("3", 64),
	"publication-project":              "team/publication",
	"publication-image-repository":     "registry.invalid/team/publication/app",
	"cosign-public-key-sha256":         strings.Repeat("4", 64),
	"release-git-host-key-fingerprint": "SHA256:" + strings.Repeat("A", 43),
	"request-allowed-signers":          "release@team.invalid ssh-ed25519 AAAA",
	"bot-name":                         "Release Bot",
	"bot-email":                        "release@team.invalid",
	"checks":                           []any{"make check"},
	"build":                            []any{"make dist"},
	"commands":                         []any{"make check"},
	"engine-sha":                       strings.Repeat("5", 40),
	"engine-reference":                 "v1.2.3",
	"changelog-config":                 ".chglog/config.yml",
	"commit-body-config":               ".forge-ci/commit-body.yml",
	"sign-distro-packages":             false,
	"release-asset-retention":          "",
	"base-images":                      false,
	"transparency-log":                 "none",
	"release-branch":                   "",
	"version-bump":                     "",
	"changelog-creator":                "git-cliff",
	"megalinter-image":                 "registry.invalid/team/megalinter@sha256:" + strings.Repeat("8", 64),
	"nanolinter-image":                 "registry.invalid/team/nanolinter@sha256:" + strings.Repeat("7", 64),
	"runtime-image":                    "registry.invalid/team/runtime@sha256:" + strings.Repeat("9", 64),
	"branch":                           "main",
	"image-repository":                 "registry.invalid/team/app",
	// The administrator settings of a child component, forwarded by its
	// parent: not the defaults, so a literal shows which value it took.
	"enforce-lint":                false,
	"enforce-vulnerabilities":     false,
	"enforce-application-tests":   false,
	"enforce-reproducible-builds": false,
}

// Every component renders, with its defaults and the inputs it requires, a
// configuration GitLab accepts: GitLab validates every job, its image
// included, before any job's rules, so an input that defaults empty must
// not empty a job a policy leaves off. The child pipelines a component
// includes render with the inputs it passes them.
func TestComponentsRenderAValidConfigurationWithTheirDefaults(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(filepath.Join(reporoot.Path(t), "templates"))
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
			continue
		}

		path := "templates/" + entry.Name()
		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()

			for _, problem := range gitlabRenderProblems(t, path, map[string]any{}) {
				t.Error(problem)
			}
		})
	}
}

// The validator itself refuses what GitLab refuses: an empty image name, an
// unknown extends or needs, a need on a job its rules drop, an undeclared
// stage and a job with nothing to run.
func TestGitLabRenderProblemsFindsWhatGitLabRefuses(t *testing.T) {
	t.Parallel()

	body := map[string]any{
		"stages":     []any{"build"},
		".hidden":    map[string]any{"image": map[string]any{"name": ""}},
		"empty name": map[string]any{"extends": ".hidden", "stage": "build", "script": []any{"true"}},
		"empty":      map[string]any{"image": "", "stage": "build", "script": []any{"true"}},
		"orphan":     map[string]any{"extends": ".missing", "stage": "build", "script": []any{"true"}},
		"needy":      map[string]any{"stage": "build", "needs": []any{"nobody", map[string]any{"job": "gone", "optional": true}}, "script": []any{"true"}},
		"dropped":    map[string]any{"stage": "build", "rules": []any{map[string]any{"if": `"true" == "false"`}}, "script": []any{"true"}},
		"stranded":   map[string]any{"stage": "build", "needs": []any{"dropped"}, "script": []any{"true"}},
		"also gone":  map[string]any{"stage": "build", "rules": []any{map[string]any{"if": `"true" == "false"`}}, "needs": []any{"dropped"}, "script": []any{"true"}},
		"by chance":  map[string]any{"stage": "build", "needs": []any{map[string]any{"job": "dropped", "optional": true}}, "script": []any{"true"}},
		"lost":       map[string]any{"stage": "deploy", "script": []any{"true"}},
		"idle":       map[string]any{"stage": "build"},
	}

	want := []string{
		`job "empty name": image name is empty`,
		`job "empty": image name is empty`,
		`job "idle": no script, run or trigger`,
		`job "lost": stage "deploy" is not in stages`,
		`job "needy": needs unknown job "nobody"`,
		`job "orphan": extends unknown ".missing"`,
		`job "stranded": needs job "dropped", which its rules drop`,
	}

	if got := gitlabConfigProblems(body); !slices.Equal(got, want) {
		t.Fatalf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// gitlabRenderProblems renders a component with the given inputs, the inputs
// it requires and its defaults, and returns what GitLab would refuse in it,
// with the repository's files it includes, and in every child pipeline of
// the repository's files it triggers.
func gitlabRenderProblems(t *testing.T, path string, given map[string]any) []string {
	t.Helper()

	body, children, problems := renderPipeline(t, path, given)

	for _, problem := range gitlabConfigProblems(body) {
		problems = append(problems, path+": "+problem)
	}

	for _, child := range children {
		inputs, _ := child["inputs"].(map[string]any)
		problems = append(problems, gitlabRenderProblems(t, repositoryTemplate(child), inputs)...)
	}

	return problems
}

// renderPipeline renders a component and merges into it the repository's
// files it includes, the including document over them, as GitLab does. The
// repository's files its trigger jobs include are its child pipelines.
func renderPipeline(t *testing.T, path string, given map[string]any) (map[string]any, []map[string]any, []string) {
	t.Helper()

	body, problems := renderComponent(t, path, given)
	merged := map[string]any{}

	var children []map[string]any

	includes, _ := body["include"].([]any)
	for _, item := range includes {
		include, _ := item.(map[string]any)
		if template := repositoryTemplate(include); template != "" {
			inputs, _ := include["inputs"].(map[string]any)
			included, grandchildren, includedProblems := renderPipeline(t, template, inputs)
			mergeGitLabHash(merged, included)

			children = append(children, grandchildren...)
			problems = append(problems, includedProblems...)
		}
	}

	mergeGitLabHash(merged, body)
	delete(merged, "include")

	for _, value := range body {
		job, _ := value.(map[string]any)
		trigger, _ := job["trigger"].(map[string]any)
		triggered, _ := trigger["include"].([]any)

		for _, item := range triggered {
			if include, _ := item.(map[string]any); repositoryTemplate(include) != "" {
				children = append(children, include)
			}
		}
	}

	return merged, children, problems
}

// repositoryTemplate is the path of this repository's template an include
// names by project, or empty.
func repositoryTemplate(include map[string]any) string {
	file, _ := include["file"].(string)
	if _, byProject := include["project"]; !byProject || !strings.HasPrefix(file, "/templates/") {
		return ""
	}

	return strings.TrimPrefix(file, "/")
}

// renderComponent interpolates a component's inputs as GitLab does: each
// takes the value given, else its default, else its fixture, and must then
// satisfy the input's type, options and regex.
func renderComponent(t *testing.T, path string, given map[string]any) (map[string]any, []string) {
	t.Helper()

	values, document, problems := componentInputValues(t, path, given)

	var parsed any
	if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
		t.Fatalf("%s is not YAML: %v", path, err)
	}

	rendered, err := interpolateGitLab(parsed, values)
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", path, err))
	}

	body, _ := rendered.(map[string]any)

	return body, problems
}

// componentInputValues is the interpolation values of a component, each
// input the value given, else its default, else its fixture, the document
// after its spec, and what GitLab refuses in the values.
func componentInputValues(t *testing.T, path string, given map[string]any) (map[string]any, string, []string) {
	t.Helper()

	text := string(reporoot.ReadFile(t, path))

	var spec struct {
		Spec struct {
			Inputs map[string]map[string]any `yaml:"inputs"`
		} `yaml:"spec"`
	}

	header, document, found := strings.Cut(text, "\n---\n")
	if !found {
		document = text
	} else if err := yaml.Unmarshal([]byte(header), &spec); err != nil {
		t.Fatal(err)
	}

	var problems []string

	values := map[string]any{"component.sha": strings.Repeat("6", 40), "component.reference": "v1.2.3"}

	for name, input := range spec.Spec.Inputs {
		value, ok := given[name]
		if !ok {
			value, ok = input["default"]
		}

		if !ok {
			value, ok = requiredInputFixtures[name]
		}

		if !ok {
			t.Fatalf("%s requires input %s: give it a fixture", path, name)
		}

		if problem := gitlabInputProblem(input, value); problem != "" {
			problems = append(problems, fmt.Sprintf("%s: input %s: %s", path, name, problem))
		}

		values["inputs."+name] = value
	}

	return values, document, problems
}

// renderScript interpolates a component's inputs in a script, as GitLab
// does, each the value given, else its default, else its fixture.
func renderScript(t *testing.T, path, script string, given map[string]any) string {
	t.Helper()

	values, _, problems := componentInputValues(t, path, given)
	if len(problems) != 0 {
		t.Fatal(strings.Join(problems, "\n"))
	}

	rendered, err := interpolateGitLab(script, values)
	if err != nil {
		t.Fatal(err)
	}

	text, ok := rendered.(string)
	if !ok {
		t.Fatalf("%s renders a script that is not text", path)
	}

	return text
}

// gitlabInputProblem is GitLab's refusal of an input value, if any.
func gitlabInputProblem(input map[string]any, value any) string {
	kind, _ := input["type"].(string)

	switch kind {
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Sprintf("%v is not an array", value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Sprintf("%v is not a boolean", value)
		}
	default:
		text, ok := value.(string)
		if !ok {
			return fmt.Sprintf("%v is not a string", value)
		}

		if options, ok := input["options"].([]any); ok && !slices.Contains(options, any(text)) {
			return fmt.Sprintf("%q is not one of %v", text, options)
		}

		if pattern, ok := input["regex"].(string); ok && !regexp.MustCompile(pattern).MatchString(text) {
			return fmt.Sprintf("%q does not match %s", text, pattern)
		}
	}

	return ""
}

var gitlabInterpolation = regexp.MustCompile(`\$\[\[\s*([a-z0-9._-]+)\s*\]\]`)

// interpolateGitLab interpolates the parsed document as GitLab does: in
// keys and string values, after YAML parsing. A string that is one
// interpolation alone takes the value itself, an array or boolean included;
// within other text a value takes its text, JSON for an array.
func interpolateGitLab(node any, values map[string]any) (any, error) {
	switch value := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))

		for key, child := range value {
			name, err := interpolateGitLab(key, values)
			if err != nil {
				return nil, err
			}

			rendered, err := interpolateGitLab(child, values)
			if err != nil {
				return nil, err
			}

			out[fmt.Sprint(name)] = rendered
		}

		return out, nil
	case []any:
		out := make([]any, len(value))

		for i, child := range value {
			rendered, err := interpolateGitLab(child, values)
			if err != nil {
				return nil, err
			}

			out[i] = rendered
		}

		return out, nil
	case string:
		if whole := gitlabInterpolation.FindStringSubmatchIndex(value); whole != nil && whole[0] == 0 && whole[1] == len(value) {
			input, ok := values[value[whole[2]:whole[3]]]
			if !ok {
				return nil, fmt.Errorf("%s names an undeclared input", value)
			}

			return input, nil
		}

		var err error

		text := gitlabInterpolation.ReplaceAllStringFunc(value, func(block string) string {
			input, ok := values[gitlabInterpolation.FindStringSubmatch(block)[1]]
			if !ok {
				err = fmt.Errorf("%s names an undeclared input", block)

				return block
			}

			if text, isText := input.(string); isText {
				return text
			}

			encoded, _ := json.Marshal(input)

			return string(encoded)
		})

		return text, err
	default:
		return node, nil
	}
}

// gitlabGlobalKeywords are the top-level keys that are not jobs.
var gitlabGlobalKeywords = []string{
	"after_script", "before_script", "cache", "default", "image", "include",
	"services", "spec", "stages", "variables", "workflow",
}

// gitlabConfigProblems is what GitLab's configuration validation, which runs
// before any rule, refuses in a rendered document, sorted.
func gitlabConfigProblems(body map[string]any) []string {
	// A component's jobs run in the stages of the pipeline including it; a
	// child pipeline's declares its own.
	stages, declared := body["stages"].([]any)
	if declared {
		stages = append(stages, ".pre", ".post")
	}

	var problems []string

	for name, value := range body {
		job, isJob := value.(map[string]any)
		if !isJob || slices.Contains(gitlabGlobalKeywords, name) {
			continue
		}

		if missing := missingExtends(body, job); missing != "" {
			problems = append(problems, fmt.Sprintf("job %q: extends unknown %q", name, missing))

			continue
		}

		if strings.HasPrefix(name, ".") {
			continue
		}

		fields := resolveGitLabExtends(body, job)
		problems = append(problems, gitlabJobProblems(body, name, fields, stages)...)
	}

	sort.Strings(problems)

	return problems
}

func gitlabJobProblems(body map[string]any, name string, fields map[string]any, stages []any) []string {
	var problems []string

	if image, ok := fields["image"]; ok {
		imageName := image
		if options, isMap := image.(map[string]any); isMap {
			imageName = options["name"]
		}

		if text, _ := imageName.(string); strings.TrimSpace(text) == "" {
			problems = append(problems, fmt.Sprintf("job %q: image name is empty", name))
		}
	}

	stage, _ := fields["stage"].(string)
	if stage == "" {
		stage = "test"
	}

	if stages != nil && !slices.Contains(stages, any(stage)) {
		problems = append(problems, fmt.Sprintf("job %q: stage %q is not in stages", name, stage))
	}

	_, script := fields["script"]
	_, run := fields["run"]
	_, trigger := fields["trigger"]

	if !script && !run && !trigger {
		problems = append(problems, fmt.Sprintf("job %q: no script, run or trigger", name))
	}

	// A job its rules drop needs nothing.
	needs, _ := fields["needs"].([]any)
	if gitlabRulesDrop(fields["rules"]) {
		needs = nil
	}

	for _, need := range needs {
		needed, optional := need, false

		if options, isMap := need.(map[string]any); isMap {
			if _, crossPipeline := options["pipeline"]; crossPipeline {
				continue
			}

			if _, crossProject := options["project"]; crossProject {
				continue
			}

			needed, optional = options["job"], options["optional"] == true
		}

		if optional {
			continue
		}

		neededName, _ := needed.(string)

		neededJob, defined := body[neededName].(map[string]any)

		if !defined {
			problems = append(problems, fmt.Sprintf("job %q: needs unknown job %q", name, neededName))
		} else if gitlabRulesDrop(resolveGitLabExtends(body, neededJob)["rules"]) {
			problems = append(problems, fmt.Sprintf("job %q: needs job %q, which its rules drop", name, neededName))
		}
	}

	return problems
}

// gitlabRulesDrop says whether rules, rendered, drop their job as GitLab
// creates the pipeline. Rules that read a variable are decided only then, so
// they drop nothing here, nor do rules other than an if alone.
func gitlabRulesDrop(rules any) bool {
	list, _ := rules.([]any)
	for _, rule := range list {
		options, _ := rule.(map[string]any)
		if condition, literal := options["if"].(string); !literal || len(options) != 1 || strings.Contains(condition, "$") {
			return false
		}
	}

	return !gitlabRulesAdmit(rules)
}

// missingExtends is the first extends of the job, or of what it extends, that
// the document does not define.
func missingExtends(body, job map[string]any) string {
	var parents []any

	switch extends := job["extends"].(type) {
	case string:
		parents = []any{extends}
	case []any:
		parents = extends
	}

	for _, parent := range parents {
		name, _ := parent.(string)

		inherited, ok := body[name].(map[string]any)
		if !ok {
			return name
		}

		if missing := missingExtends(body, inherited); missing != "" {
			return missing
		}
	}

	return ""
}

// The release component's default render: its image jobs exist, never run,
// and still name a job image GitLab accepts.
func TestReleaseWithoutReleaseImagesNeedsNoBuilderJobImage(t *testing.T) {
	t.Parallel()

	for _, builder := range []string{"buildah", "kaniko"} {
		if problems := gitlabRenderProblems(t, "templates/release.yml", map[string]any{"image-builder": builder}); len(problems) != 0 {
			t.Errorf("%s:\n%s", builder, strings.Join(problems, "\n"))
		}
	}

	// With release images, the image jobs run in builder-job-image, and
	// without one GitLab refuses the child pipeline as it creates it, before
	// any job runs.
	problems := gitlabRenderProblems(t, "templates/release.yml", map[string]any{"release-images": true})
	if !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, `job "Images: build amd64": image name is empty`)
	}) {
		t.Fatalf("an empty builder-job-image with release images was accepted:\n%s", strings.Join(problems, "\n"))
	}

	builderImage := "registry.invalid/team/buildah-job@sha256:" + strings.Repeat("7", 64)
	if problems := gitlabRenderProblems(t, "templates/release.yml", map[string]any{"release-images": true, "builder-job-image": builderImage}); len(problems) != 0 {
		t.Fatalf("with its builder job image:\n%s", strings.Join(problems, "\n"))
	}
}

// A release with images renders a configuration GitLab accepts for each
// image builder, with and without export jobs, and with artifacts-config
// naming a file under .forge-ci/, the documented default's directory: an
// input GitLab refuses fails the policy pipeline as GitLab creates it, so
// the release child never starts.
func TestReleaseWithImagesRendersAValidConfiguration(t *testing.T) {
	t.Parallel()

	builderImage := "registry.invalid/team/builder-job@sha256:" + strings.Repeat("7", 64)
	export := []any{`forge-ci container build --mode local --platform "$EXPORT_PLATFORM" --output-dir "dist/linux-$EXPORT_ARCH"`}

	for _, builder := range []string{"buildah", "kaniko"} {
		for _, exports := range []bool{false, true} {
			for _, config := range []string{"", ".forge-ci/artifacts.yml", ".forge-ci/release-images.yml", "deploy/artifacts.yml"} {
				inputs := map[string]any{
					"release-images": true, "image-builder": builder, "builder-job-image": builderImage,
					"artifacts-config": config, "release-platforms": "linux/amd64",
					"job-user": "65532:65532", "version-bump": "npm",
					"export-files": exports, "export": []any{},
				}
				if exports {
					inputs["export"] = export
				}

				if builder == "buildah" {
					inputs["build-runner-tags"] = []any{"privileged"}
				}

				if problems := gitlabRenderProblems(t, "templates/release.yml", inputs); len(problems) != 0 {
					t.Errorf("%s, export-files %t, artifacts-config %q:\n%s", builder, exports, config, strings.Join(problems, "\n"))
				}
			}
		}
	}

	// A path that leaves the repository or quotes the script stays refused.
	for _, config := range []string{"/etc/artifacts.yml", "it's.yml", "-artifacts.yml"} {
		if problems := gitlabRenderProblems(t, "templates/release.yml", map[string]any{
			"release-images": true, "builder-job-image": builderImage, "artifacts-config": config,
		}); len(problems) == 0 {
			t.Errorf("artifacts-config %q was accepted", config)
		}
	}
}
