// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// Read the live case's actual repository argument: testing the shared selector
// alone would not catch this case ignoring it and always publishing the source.
func TestMavenLibraryStableUsesRequestedConsumerVisibility(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("conformance", "mavenlibraryconsumer_test.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var argument ast.Expr
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if ok && name.Name == "newConsumerRepos" && len(call.Args) == 5 {
			if argument != nil {
				t.Fatal("expected one library stable repository creation")
			}
			argument = call.Args[4]
		}
		return true
	})
	if argument == nil {
		t.Fatal("library stable repository creation not found")
	}

	for _, test := range []struct {
		selector string
		private  bool
	}{{"", false}, {"both", false}, {"public", false}, {"private", true}} {
		t.Run("selector="+test.selector, func(t *testing.T) {
			t.Setenv(consumerVisibilityEnv, test.selector)
			var got bool
			switch expr := argument.(type) {
			case *ast.Ident:
				if expr.Name != "false" && expr.Name != "true" {
					t.Fatalf("unsupported repository visibility argument %s", expr.Name)
				}
				got = expr.Name == "true"
			case *ast.CallExpr:
				selector, ok := expr.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "ScenarioPrivate" || len(expr.Args) != 1 {
					t.Fatal("library stable must use the existing ScenarioPrivate selector")
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || pkg.Name != "livetest" {
					t.Fatal("unexpected visibility selector package")
				}
				got = ScenarioPrivate(t)
			default:
				t.Fatal("unexpected repository visibility argument")
			}
			if got != test.private {
				t.Errorf("library stable selector %q creates private=%t, want %t", test.selector, got, test.private)
			}
		})
	}
}
