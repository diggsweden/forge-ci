// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package lexiconguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"github.com/stretchr/testify/require"
)

// singleSource defaults to a literal text policy, including comments. Digest
// rules instead inspect direct regexp calls; Platform and retry have separate
// AST rules. None of these guards proves all runtime validation uses canonical
// APIs, or detects every equivalent regex. They share source discovery here.
// Text mode stays the default where no inert copies are expected, because the
// regexp-call mode cannot follow a constant declared in another file: a comment
// restating a canonical pattern is cheap to reword, a validator built from a
// cross-file constant is not cheap to miss.

// singleSource is one single-sourcing rule: the spellings that must appear in
// no Go file other than the ones that own them.
type singleSource struct {
	root string // explicit owned root for scanner self-tests; empty selects the repository.
	// patterns are the literal spellings to hunt. A file matching any one of
	// them is an offender; a guard with several patterns is guarding several
	// notations of the same fact.
	patterns []string
	// owners are the repo-relative files entitled to hold the literal --
	// normally the guard itself plus the package that defines the canonical
	// value.
	owners []string
	// fold compares case-insensitively, for rules about a spelling rather
	// than about an exact pattern literal.
	fold bool
	// regexpCalls checks only direct stdlib regexp calls with literal or same-file
	// constant string arguments. Comments and inert examples are not validators.
	// Function-value aliases, dynamic strings and cross-file constants are not resolved.
	regexpCalls bool
}

// offenders returns every Go file outside the owners that restates one of the
// patterns, in walk order.
func (s singleSource) offenders(t *testing.T) []string {
	t.Helper()

	owned := make(map[string]bool, len(s.owners))
	for _, owner := range s.owners {
		owned[owner] = true
	}

	wanted := s.patterns
	if s.fold {
		wanted = make([]string, len(s.patterns))
		for i, pattern := range s.patterns {
			wanted[i] = strings.ToLower(pattern)
		}
	}

	var offenders []string

	root := s.root
	if root == "" {
		root = reporoot.Path(t)
	}

	visited, err := reporoot.WalkGoSources(root, func(path, rel string) error {
		if owned[rel] {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		bodies := []string{string(content)}
		if s.regexpCalls {
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, content, 0)
			if parseErr != nil {
				return parseErr
			}

			bodies = regexpArguments(file)
		}

		for _, body := range bodies {
			if s.fold {
				body = strings.ToLower(body)
			}

			for _, pattern := range wanted {
				if strings.Contains(body, pattern) {
					offenders = append(offenders, rel)

					return nil
				}
			}
		}

		return nil
	})
	require.NoError(t, err)
	require.NotZero(t, visited, "lexicon guard inspected no Go files")

	return offenders
}

// requireSingleSourced fails the test naming every file that restated the
// literal, with remedy explaining what to call instead.
func (s singleSource) requireSingleSourced(t *testing.T, remedy string) {
	t.Helper()

	if offenders := s.offenders(t); len(offenders) > 0 {
		t.Errorf("%s: %v", remedy, offenders)
	}

	if stale := s.staleOwners(reporoot.Path(t)); len(stale) > 0 {
		t.Errorf("owners exempted from the rule but missing or no longer holding any of %q: %v", s.patterns, stale)
	}
	// Every rule exercises the real walker with a clean and a poisoned file.
	s.root = t.TempDir()
	dir := filepath.Join(s.root, "internal", "app")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "poison.go")
	require.NoError(t, os.WriteFile(path, []byte("package clean\n"), 0o600))
	require.Empty(t, s.offenders(t))

	for _, pattern := range s.patterns {
		poison := "package poison\n/*" + pattern + "*/\n"
		if s.regexpCalls {
			poison = "package poison\nimport \"regexp\"\nvar _ = regexp.MustCompile(" + strconv.Quote(pattern) + ")\n"
		}

		require.NoError(t, os.WriteFile(path, []byte(poison), 0o600))
		require.Equal(t, []string{"internal/app/poison.go"}, s.offenders(t))
	}
}

// staleOwners returns each owner under root that is missing or holds none of
// the patterns. An owner is exempt only because it declares the canonical
// value; once that declaration moves or is deleted, the exemption hides a file
// the rule no longer has a reason to skip, and the rule guards a single source
// that does not exist.
func (s singleSource) staleOwners(root string) []string {
	var stale []string

	for _, owner := range s.owners {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(owner)))
		if err != nil || !s.holdsPattern(string(content)) {
			stale = append(stale, owner)
		}
	}

	return stale
}

func (s singleSource) holdsPattern(content string) bool {
	for _, pattern := range s.patterns {
		if strings.Contains(content, pattern) || (s.fold && strings.Contains(strings.ToLower(content), strings.ToLower(pattern))) {
			return true
		}
	}

	return false
}

func TestStaleOwners_ReportsMissingAndEmptiedOwners(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for rel, body := range map[string]string{
		"holds.go":   "package p\nconst P = `^[0-9a-f]{64}$`\n",
		"folded.go":  "package p\n// " + strings.ToUpper(britishArtifact) + "\n",
		"emptied.go": "package p\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600))
	}

	rule := singleSource{patterns: []string{"^[0-9a-f]{64}$"}, owners: []string{"holds.go", "emptied.go", "missing.go"}}
	require.Equal(t, []string{"emptied.go", "missing.go"}, rule.staleOwners(root))

	spelling := singleSource{patterns: []string{britishArtifact}, fold: true, owners: []string{"folded.go", "holds.go"}}
	require.Equal(t, []string{"holds.go"}, spelling.staleOwners(root))
}

// importedFunction recognizes direct calls, including renamed/dot imports, but
// not methods on local values that shadow an import. Parse with object resolution.
func importedFunction(file *ast.File, expr ast.Expr, pkg string, names ...string) bool {
	var qualifier, name string

	switch fun := ast.Unparen(expr).(type) {
	case *ast.SelectorExpr:
		id, ok := fun.X.(*ast.Ident)
		if !ok || id.Obj != nil {
			return false
		}

		qualifier, name = id.Name, fun.Sel.Name
	case *ast.Ident:
		if fun.Obj != nil {
			return false
		}

		qualifier, name = ".", fun.Name
	default:
		return false
	}

	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath != pkg {
			continue
		}

		alias := filepath.Base(pkg)
		if spec.Name != nil {
			alias = spec.Name.Name
		}

		if alias != qualifier {
			continue
		}

		if slices.Contains(names, name) {
			return true
		}
	}

	return false
}

func regexpArguments(file *ast.File) []string {
	// Omitted const initializers inherit the preceding specification in the
	// same declaration group. Index by declaration, not spelling, to retain scope.
	initializers := make(map[*ast.ValueSpec][]ast.Expr)

	ast.Inspect(file, func(node ast.Node) bool {
		decl, ok := node.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}

		var values []ast.Expr

		for _, item := range decl.Specs {
			spec, ok := item.(*ast.ValueSpec)
			if !ok {
				continue
			}

			if len(spec.Values) != 0 {
				values = spec.Values
			}

			initializers[spec] = values
		}

		return false
	})

	var found []string

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 || !importedFunction(file, call.Fun, "regexp",
			"Compile", "MustCompile", "CompilePOSIX", "MustCompilePOSIX", "Match", "MatchString", "MatchReader") {
			return true
		}

		if value, ok := constantString(call.Args[0], make(map[ast.Expr]bool), initializers); ok {
			found = append(found, value)
		}

		return true
	})

	return found
}

func constantString(expr ast.Expr, seen map[ast.Expr]bool, initializers map[*ast.ValueSpec][]ast.Expr) (string, bool) {
	expr = ast.Unparen(expr)
	if seen[expr] {
		return "", false
	}

	seen[expr] = true
	defer delete(seen, expr)

	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)

			return text, err == nil
		}
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			left, leftOK := constantString(value.X, seen, initializers)
			right, rightOK := constantString(value.Y, seen, initializers)

			return left + right, leftOK && rightOK
		}
	case *ast.Ident:
		if value.Obj != nil && value.Obj.Kind == ast.Con {
			if spec, ok := value.Obj.Decl.(*ast.ValueSpec); ok {
				values := initializers[spec]
				for i, name := range spec.Names {
					if name.Obj == value.Obj && i < len(values) {
						return constantString(values[i], seen, initializers)
					}
				}
			}
		}
	default:
	}

	return "", false
}
