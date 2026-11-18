// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package archguard

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProcessStateWrites_FindsEverySpellingAndNoOther poisons the detector
// once: each spelling that writes process state is reported, and the
// lookalikes that do not are left alone.
func TestProcessStateWrites_FindsEverySpellingAndNoOther(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		source string
		want   int
	}{
		{"a call", `import "os"; func f() { _ = os.Setenv("K", "v"); _ = os.Unsetenv("K"); os.Clearenv(); _ = os.Chdir("/") }`, 4},
		{"a renamed import", `import sys "os"; func f() { _ = sys.Setenv("K", "v") }`, 1},
		{"a dot import", `import . "os"; func f() { _ = Chdir("/") }; var unset = Unsetenv`, 2},
		{"a local shadowing the dot import", `import . "os"; var _ = Getenv; func f() { Chdir := func(string) error { return nil }; _ = Chdir("/") }`, 0},
		{"a function value", `import "os"; var set = os.Setenv`, 1},
		{"a method holding the test", `import ("os"; "testing"); type r struct{ t testing.TB }; func (r *r) chdir() { _ = os.Chdir("/") }`, 1},
		{"in TestMain", `import ("os"; "testing"); func TestMain(m *testing.M) { _ = os.Setenv("K", "v"); os.Exit(m.Run()) }`, 0},
		{"through testing", `import "testing"; func f(t *testing.T) { t.Setenv("K", "v"); t.Chdir("/") }`, 0},
		{"a read", `import "os"; var _ = os.Getenv("K")`, 0},
		{"a local shadowing the import", `import "os"; func f() { os := struct{ Setenv func(string, string) error }{}; _ = os.Setenv("K", "v") }`, 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			file, err := parser.ParseFile(token.NewFileSet(), "owned_test.go", "package owned; "+testCase.source, 0)
			require.NoError(t, err)
			require.Len(t, processStateWrites(file), testCase.want)
		})
	}
}
