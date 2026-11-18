// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package archguard

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

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// processStateWriters are the os functions that change state every test in the
// process shares. testing restores what t.Setenv and t.Chdir change and
// refuses them in a parallel test; a raw call does neither, so its value
// reaches whichever test runs next.
var processStateWriters = map[string]bool{"Setenv": true, "Unsetenv": true, "Clearenv": true, "Chdir": true}

// isolatedEnvOwner is the one test helper that writes the environment itself:
// isolatedenv.Unsetenv unsets under a t.Setenv restore, and IsolateProcess
// scrubs a whole process from TestMain.
const isolatedEnvOwner = "internal/testutil/isolatedenv/"

// TestTestCodeChangesProcessStateOnlyThroughTesting fails when a test, or a
// helper under internal/testutil, writes the process environment or working
// directory without testing's restore. A helper is in scope because it holds
// the test's T where the linters cannot see it: testfs once changed directory
// with os.Chdir from a method, unguarded in a parallel test. TestMain may write
// both, since it runs before any test.
func TestTestCodeChangesProcessStateOnlyThroughTesting(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		rel = filepath.ToSlash(rel)

		if entry.IsDir() {
			if path == root {
				return nil
			}

			// The directories the go command ignores hold no test code of this
			// module, and neither does a nested module: its tests cannot import
			// isolatedenv.
			if name := entry.Name(); name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}

			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}

			return nil
		}

		testCode := strings.HasSuffix(rel, "_test.go") || (strings.HasPrefix(rel, "internal/testutil/") && strings.HasSuffix(rel, ".go"))
		if !testCode || (strings.HasPrefix(rel, isolatedEnvOwner) && !strings.HasSuffix(rel, "_test.go")) {
			return nil
		}

		fset := token.NewFileSet()

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		for _, position := range processStateWrites(file) {
			t.Errorf("%s: changes the process environment or working directory without restoring it.\n"+
				"    Fix: t.Setenv, isolatedenv.Unsetenv(t, key) or t.Chdir (testfs.Real.Chdir),\n"+
				"    which restore at cleanup and refuse a parallel test; or TestMain, for\n"+
				"    the whole process.", fset.Position(position))
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// processStateWrites returns each reference to a processStateWriters function
// of package os outside TestMain. A reference, not only a call: a function
// value writes just the same once it is called. Parser objects distinguish
// local shadowing, as in operatorCredentialReferences.
func processStateWrites(file *ast.File) []token.Pos {
	names := map[string]bool{}
	dot := false

	for _, spec := range file.Imports {
		if path, err := strconv.Unquote(spec.Path.Value); err != nil || path != "os" {
			continue
		}

		switch {
		case spec.Name == nil:
			names["os"] = true
		case spec.Name.Name == ".":
			dot = true
		default:
			names[spec.Name.Name] = true
		}
	}

	var (
		found []token.Pos
		visit func(ast.Node) bool
	)

	visit = func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := node.X.(*ast.Ident); ok && pkg.Obj == nil && names[pkg.Name] && processStateWriters[node.Sel.Name] {
				found = append(found, node.Pos())
			}

			ast.Inspect(node.X, visit)

			return false
		case *ast.Ident:
			if dot && node.Obj == nil && processStateWriters[node.Name] {
				found = append(found, node.Pos())
			}
		}

		return true
	}

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "TestMain" {
			continue
		}

		ast.Inspect(decl, visit)
	}

	return found
}
