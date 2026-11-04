// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// livetestTree holds the live scenarios and their kit.
const livetestTree = "internal/livetest"

// repositoryCreators are the only files that make a creating request for a
// repository: the visibility-aware scratch helpers, and the demos, which are
// public by design. A scenario creates through NewScratchRepo,
// NewBareScratchRepo or a NewPublic* helper, so the run's visibility holds.
var repositoryCreators = map[string]bool{
	livetestTree + "/scratch.go": true,
	livetestTree + "/demo.go":    true,
}

// subjectVisibility are the scenarios that may pass a literal visibility to
// NewBareScratchRepo, because their subject is that visibility; each admits
// it with RequireVisibility first, so the other visibility's run reports it
// not covered.
var subjectVisibility = map[string]string{
	livetestTree + "/conformance/privateaccess_test.go": "proves a private repository refuses anonymous reads and admits the job token",
	livetestTree + "/conformance/privateimage_test.go":  "pulls a private job image into a private repository's job",
	livetestTree + "/conformance/gitlabcheck_test.go":   "its private case checks out a private consumer with the job token",
}

// privilegedDirect are the live files that may ask for the privileged runner
// directly, and why. Every other scenario resolves the run's builder through
// ChooseImageBuilder, or buildahRunner, which refuses kaniko with a reason.
var privilegedDirect = map[string]string{
	livetestTree + "/imagebuilder.go":                               "ChooseImageBuilder, the shared builder selection",
	livetestTree + "/conformance/gitlabrelease_test.go":             "the Go CLI fixture's checks are Buildah's own integration tests, its subject",
	livetestTree + "/conformance/gitlabconsumerdevelopment_test.go": "the Buildah branch of its own GitLabKaniko selection",
	livetestTree + "/conformance/layerconsumertrial_test.go":        "Maven Testcontainers need user namespaces to run containers, and the Buildah branch of the trial's CI_IMAGE_BUILDER",
	livetestTree + "/conformance/mavenservicedevelopment_test.go":   "Podman service readiness needs user namespaces independently of the selected image builder",
}

// buildahNamed are the live files whose fixtures may name Buildah, and why.
var buildahNamed = map[string]string{
	livetestTree + "/imagebuilder.go":                   "Kaniko development admission rejects an explicit setup-buildah action",
	livetestTree + "/imagebuilder_internal_test.go":     "negative admission regression for an explicit setup-buildah action",
	livetestTree + "/nativesnapshotartifact_test.go":    "the Buildah capability probe, run only when the selection is Buildah",
	livetestTree + "/conformance/gitlabrelease_test.go": "the build-image probe checks the build image's own Buildah",
}

// buildahLiteral names Buildah as a build step in a fixture.
var buildahLiterals = []string{"setup-buildah", "buildah build", "manifest push --local-manifest"}

type liveFinding struct {
	path string
	line int
	text string
}

// Every repository a live scenario creates takes its visibility from the run
// (FORGE_CI_LIVE_CONSUMER_VISIBILITY), and every scenario that builds images
// resolves the run's builder selection. A hard-coded visibility, a direct
// creating request, or a direct Buildah requirement outside the reasoned
// allowlists fails.
func TestLiveScenariosHonourRunVisibilityAndBuilder(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	findings, seen := scanLiveScenarios(t, root)

	for _, finding := range findings {
		t.Errorf("%s:%d %s", finding.path, finding.line, finding.text)
	}

	for path, reason := range subjectVisibility {
		if !seen["visibility:"+path] {
			t.Errorf("%s may pass a literal visibility (%s) but no longer does; drop it from the allowlist", path, reason)
		}
	}

	for kind, allowed := range map[string]map[string]string{"privileged:": privilegedDirect, "buildah:": buildahNamed} {
		for path, reason := range allowed {
			if !seen[kind+path] {
				t.Errorf("%s may require Buildah directly (%s) but no longer does; drop it from the allowlist", path, reason)
			}
		}
	}
}

// The guard refuses what the snapshot scenarios and the base fixture did
// before they followed the run's settings.
func TestLiveScenarioGuardRefusesTheOldShapes(t *testing.T) {
	t.Parallel()

	for name, source := range map[string]string{
		"literal public engine": `package p
func f() { NewBareScratchRepo(t, target, "snapshot-engine", "sha1", false) }`,
		"privileged runner": `package p
func f() { _ = PrivilegedRunnerLabel(t, target) }`,
		"buildah fixture": "package p\nconst build = `forge-ci container setup-buildah --install-packages=false`",
		"direct creation": "package p\nfunc f() { _ = target.BaseURL() + \"/api/v1/user/repos\" }",
		"empty public reason": `package p
func f() { NewPublicScratchRepo(t, target, "weburl", "") }`,
	} {
		findings, _ := scanLiveFile(t, livetestTree+"/conformance/example_test.go", source)
		if len(findings) == 0 {
			t.Errorf("%s: the guard accepted %q", name, source)
		}
	}
}

func scanLiveScenarios(t *testing.T, root string) ([]liveFinding, map[string]bool) {
	t.Helper()

	var findings []liveFinding

	seen := map[string]bool{}

	require.NoError(t, filepath.WalkDir(filepath.Join(root, livetestTree), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}

		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)

		found, marks := scanLiveFile(t, rel, string(body))
		findings = append(findings, found...)

		for mark := range marks {
			seen[mark] = true
		}

		return nil
	}))

	return findings, seen
}

func scanLiveFile(t *testing.T, rel, source string) ([]liveFinding, map[string]bool) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, source, parser.SkipObjectResolution)
	require.NoError(t, err, rel)

	var findings []liveFinding

	seen := map[string]bool{}
	report := func(node ast.Node, text string) {
		findings = append(findings, liveFinding{path: rel, line: fset.Position(node.Pos()).Line, text: text})
	}

	_, privilegedAllowed := privilegedDirect[rel]
	_, buildahAllowed := buildahNamed[rel]
	_, visibilityAllowed := subjectVisibility[rel]
	requiresVisibility := strings.Contains(source, "RequireVisibility(")

	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			checkLiveCall(n, rel, privilegedAllowed, visibilityAllowed && requiresVisibility, seen, report)
		case *ast.BasicLit:
			checkLiveLiteral(n, rel, buildahAllowed, seen, report)
		}

		return true
	})

	return findings, seen
}

func checkLiveCall(call *ast.CallExpr, rel string, privilegedAllowed, visibilityAllowed bool, seen map[string]bool, report func(ast.Node, string)) {
	switch name := calleeName(call); name {
	case "PrivilegedRunnerLabel":
		seen["privileged:"+rel] = true

		if !privilegedAllowed {
			report(call, "asks for the privileged runner directly; resolve the run's builder with ChooseImageBuilder (or buildahRunner with a reason), so a kaniko run builds with kaniko")
		}
	case "Recreate", "CreateRepo", "CreateProject", "createBareRepo", "createInitialisedRepo", "createPrivateScratchRepo":
		if !repositoryCreators[rel] {
			report(call, name+" creates a repository outside the scratch helpers, past the run's visibility; use NewScratchRepo, NewBareScratchRepo or a NewPublic* helper")
		}
	case "NewBareScratchRepo":
		if len(call.Args) == 5 && isBoolLiteral(call.Args[4]) {
			seen["visibility:"+rel] = true

			if !visibilityAllowed {
				report(call, "passes a literal visibility; pass a ConsumerVisibility's or ScenarioPrivate(t), NewPublicBareScratchRepo with a reason, or admit a subject's visibility with RequireVisibility")
			}
		}
	case "NewPublicScratchRepo", "NewPublicBareScratchRepo", "NewPublicScratchRepoUnique":
		if len(call.Args) == 0 || !namesReason(call.Args[len(call.Args)-1]) {
			report(call, name+" needs a reason: a Read*Anonymously constant or a nonempty string saying what reads it without credentials")
		}
	}
}

func checkLiveLiteral(lit *ast.BasicLit, rel string, buildahAllowed bool, seen map[string]bool, report func(ast.Node, string)) {
	if lit.Kind != token.STRING {
		return
	}

	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}

	for _, literal := range buildahLiterals {
		if strings.Contains(value, literal) {
			seen["buildah:"+rel] = true

			if !buildahAllowed {
				report(lit, "a fixture names Buildah ("+literal+"); build with the selected builder (container build, multiarch-image build-arch) in the builder's job, as the export and image jobs do")
			}
		}
	}

	if !repositoryCreators[rel] && (strings.HasSuffix(value, "/user/repos") || strings.HasSuffix(value, "/api/v4/projects")) {
		report(lit, "a repository-creating endpoint outside the scratch helpers, past the run's visibility")
	}
}

func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}

	return ""
}

func isBoolLiteral(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)

	return ok && (ident.Name == "true" || ident.Name == "false")
}

func namesReason(expr ast.Expr) bool {
	switch reason := expr.(type) {
	case *ast.BasicLit:
		value, err := strconv.Unquote(reason.Value)

		return err == nil && strings.TrimSpace(value) != ""
	case *ast.SelectorExpr:
		return strings.HasPrefix(reason.Sel.Name, "Read") && strings.HasSuffix(reason.Sel.Name, "Anonymously")
	case *ast.Ident:
		return strings.HasPrefix(reason.Name, "Read") && strings.HasSuffix(reason.Name, "Anonymously") || reason.Name == "public" || reason.Name == "reason"
	}

	return false
}
