// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package enforcement_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

const domainEnforcement = "github.com/diggsweden/forge-ci/internal/domain/enforcement"

// The embedded default is not the effective policy: the effective policy is
// the default with this invocation's --enforce-* flags applied, after the
// environment has been refused. Resolve is the one place that does both, so a
// gate that read enforcement.Current itself would ignore the flags and the
// refusal, silently. Only Resolve's file may call it.
func TestOnlyResolveReadsTheEmbeddedDefault(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)
	allowed := filepath.Join("internal", "app", "enforcement", "admin.go")

	var offenders []string

	checked := 0

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".claude", "node_modules", "vendor":
				return filepath.SkipDir
			}

			return nil
		}

		rel, _ := filepath.Rel(root, path)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || rel == allowed ||
			strings.HasPrefix(rel, filepath.Join("internal", "domain", "enforcement")+string(filepath.Separator)) {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}

		alias := ""

		for _, spec := range file.Imports {
			if importPath, _ := strconv.Unquote(spec.Path.Value); importPath == domainEnforcement {
				alias = "enforcement"
				if spec.Name != nil {
					alias = spec.Name.Name
				}
			}
		}

		if alias == "" {
			return nil
		}

		checked++

		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Current" {
				return true
			}

			if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == alias {
				offenders = append(offenders, rel)
			}

			return true
		})

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if checked < 5 {
		t.Fatalf("only %d files import the enforcement domain; this guard measured nothing", checked)
	}

	if len(offenders) > 0 {
		t.Fatalf("these files read the embedded default instead of appenforcement.Resolve(ctx), so they ignore the --enforce-* flags and the environment refusal: %v", offenders)
	}
}
