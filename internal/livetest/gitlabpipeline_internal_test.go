// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gitlabapi "gitlab.com/gitlab-org/api/client-go"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// The account of a failed release says what GitLab said at every level: the
// parent's failed job and its reason, the trigger job and the child it made,
// the child's refused configuration, and a parent refused before any job,
// whose configuration errors are all there is.
func TestGitLabPipelineAccountKeepsWhatGitLabSaid(t *testing.T) {
	t.Parallel()

	routes := map[string]string{
		"/api/v4/projects/group%2Fapp/pipelines/10":         `{"id":10,"status":"failed","source":"push"}`,
		"/api/v4/projects/group%2Fapp/pipelines/10/jobs":    `[{"name":"Check: release request","stage":"check","status":"failed","failure_reason":"script_failure"},{"name":"fine","status":"success"}]`,
		"/api/v4/projects/group%2Fapp/pipelines/10/bridges": `[{"name":"Release","stage":"release","status":"failed","failure_reason":"downstream_pipeline_creation_failed","downstream_pipeline":{"id":11,"status":"failed"}}]`,
		"/api/v4/projects/group%2Fapp/pipelines/11":         `{"id":11,"status":"failed","source":"parent_pipeline","yaml_errors":"jobs:images config contains unknown keys: nope"}`,
		"/api/v4/projects/group%2Fapp/pipelines/11/jobs":    `[]`,
		"/api/v4/projects/group%2Fapp/pipelines/11/bridges": `[]`,
		"/api/v4/projects/group%2Fapp/pipelines/12":         `{"id":12,"status":"failed","source":"push","yaml_errors":"input artifacts-config does not match the regex"}`,
		"/api/v4/projects/group%2Fapp/pipelines/12/jobs":    `[]`,
		"/api/v4/projects/group%2Fapp/pipelines/12/bridges": `[{"name":"Release","stage":"release","status":"failed","failure_reason":"","downstream_pipeline":null}]`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, found := routes[request.URL.EscapedPath()]
		if !found {
			response.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(response, `{"message":"404 Not found"}`)

			return
		}

		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(server.Close)

	client, err := gitlabapi.NewClient("token", gitlabapi.WithBaseURL(server.URL+"/api/v4"), gitlabapi.WithoutRetries())
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		pipeline int64
		want     []string
	}{
		{10, []string{
			"pipeline 10 (push): failed",
			`  job "Check: release request" (check): failed (script_failure)`,
			`  trigger "Release" (release): failed (downstream_pipeline_creation_failed), downstream pipeline 11`,
			"    pipeline 11 (parent_pipeline): failed",
			"      configuration refused: jobs:images config contains unknown keys: nope",
		}},
		{12, []string{
			"pipeline 12 (push): failed",
			"  configuration refused: input artifacts-config does not match the regex",
			`  trigger "Release" (release): failed, no downstream pipeline`,
		}},
	} {
		if got := GitLabPipelineAccount(t.Context(), client, "group/app", test.pipeline); !slices.Equal(got, test.want) {
			t.Errorf("account of %d:\n%s\nwant:\n%s", test.pipeline, strings.Join(got, "\n"), strings.Join(test.want, "\n"))
		}
	}

	// An unreadable pipeline is a line, never the end of the test.
	if got := GitLabPipelineAccount(t.Context(), client, "group/app", 13); len(got) != 1 || !strings.HasPrefix(got[0], "pipeline 13: unreadable") {
		t.Errorf("unreadable pipeline: %v", got)
	}
}

func TestForgejoRunAccountNamesJobsNoRunnerTook(t *testing.T) {
	t.Parallel()

	never, ran := int64(0), int64(7)
	got := forgejoRunAccount(WorkflowRun{ID: 4, WorkflowID: "release.yml", Status: runFailure}, []RunJob{
		{Name: "prepare", Status: runSuccess, TaskID: &ran},
		{Name: "build", Status: runFailure, TaskID: &ran},
		{Name: "sign", Status: runFailure, TaskID: &never, RunsOn: []string{"signer"}},
	})

	want := []string{
		"run 4 of release.yml: failure, 3 jobs",
		`  job "build": failure`,
		`  job "sign": failure; no runner ever took it (no task; runs-on [signer], needs []): the forge decided it before dispatch`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("account:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Every GitLab pipeline wait of the conformance scenarios logs GitLab's
// account of a pipeline that did not succeed before it returns it, so no
// scenario can judge, fail and clean up without it: a release once failed
// with "child pipeline was not created: <nil>" and its project, holding the
// reason, was deleted.
func TestGitLabPipelineWaitsLogTheForgesAccount(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(reporoot.Path(t), "internal", "livetest", "conformance")

	files, err := filepath.Glob(filepath.Join(dir, "gitlab*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no GitLab conformance scenarios found: %v", err)
	}

	waits := 0
	fset := token.NewFileSet()

	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		for _, decl := range parsed.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(function.Name.Name, "waitGitLab") || !returnsGitLabPipeline(function) {
				continue
			}

			waits++

			if !callsAny(function, "LogGitLabPipelineFailure", "waitGitLabPushPipeline") {
				t.Errorf("%s: %s returns a pipeline without logging GitLab's account of its failure; call livetest.LogGitLabPipelineFailure",
					filepath.Base(file), function.Name.Name)
			}
		}
	}

	if waits == 0 {
		t.Fatal("no GitLab pipeline wait found; the guard has drifted")
	}
}

func returnsGitLabPipeline(function *ast.FuncDecl) bool {
	if function.Type.Results == nil {
		return false
	}

	for _, result := range function.Type.Results.List {
		if star, ok := result.Type.(*ast.StarExpr); ok {
			if selector, ok := star.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "Pipeline" {
				return true
			}
		}
	}

	return false
}

func callsAny(function *ast.FuncDecl, names ...string) bool {
	found := false

	ast.Inspect(function, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return !found
		}

		switch callee := call.Fun.(type) {
		case *ast.Ident:
			found = found || slices.Contains(names, callee.Name)
		case *ast.SelectorExpr:
			found = found || slices.Contains(names, callee.Sel.Name)
		}

		return !found
	})

	return found
}
