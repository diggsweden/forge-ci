// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"gopkg.in/yaml.v3"
)

// TestDeclaredInputsAreRead counts an input read once anything reads it, and
// passing it into an environment variable is a read. Such an input is still
// ignored when nothing reads the variable. The variable counts as read when
// another value of the same file names it, as a run script or an expression
// does, or when the engine's Go or a shell script does, which is where a
// step's command picks it up. A name read only by a third-party tool would
// be a false finding; none is today.

var inputExpression = regexp.MustCompile(`\binputs(\.|\[)`)

func TestInputEnvironmentVariablesAreRead(t *testing.T) {
	t.Parallel()

	readers := identifiers(engineReaderText(t))

	var findings []string

	checked := 0

	check := func(path string) {
		body := reporoot.ReadFile(t, path)
		names, values := inputEnvironmentNames(t, path, body)
		checked += len(names)
		named := identifiers(values)

		for _, name := range names {
			if !named[name] && !readers[name] {
				findings = append(findings, path+": "+name)
			}
		}
	}

	for _, dir := range []string{".github/workflows", ".forgejo/workflows"} {
		for _, path := range yamlFiles(t, dir) {
			check(path)
		}
	}

	for _, dir := range []string{".github/actions", ".forgejo/actions"} {
		for _, entry := range reporoot.ReadDir(t, dir) {
			if entry.IsDir() {
				check(dir + "/" + entry.Name() + "/action.yml")
			}
		}
	}

	if len(findings) > 0 {
		t.Fatalf("inputs passed to an environment variable nothing reads:\n  %s", strings.Join(findings, "\n  "))
	}

	if checked == 0 {
		t.Fatal("no input reached an environment variable; this guard measured nothing")
	}
}

func TestInputEnvironmentNames_SeesOnlyTheUnreadVariable(t *testing.T) {
	t.Parallel()

	body := []byte(`jobs:
  build:
    env:
      JOB_LEVEL: ${{ inputs.level }}
    steps:
      - env:
          USED: ${{ inputs.used }}
          IGNORED: ${{ inputs['ignored'] }}
          CONSTANT: fixed
        # $IGNORED
        run: |
          # echo "$IGNORED"
          echo "$USED ${JOB_LEVEL}"
`)

	names, values := inputEnvironmentNames(t, "w.yml", body)
	if want := []string{"IGNORED", "JOB_LEVEL", "USED"}; !slices.Equal(names, want) {
		t.Fatalf("names %q, want %q", names, want)
	}

	var unread []string

	named := identifiers(values)
	for _, name := range names {
		if !named[name] {
			unread = append(unread, name)
		}
	}

	if want := []string{"IGNORED"}; !slices.Equal(unread, want) {
		t.Fatalf("unread %q, want %q", unread, want)
	}
}

// inputEnvironmentNames returns the sorted names of every env: entry whose
// value reads an input, and the file's values without comment lines and
// without those entries, where a read of a name would be.
func inputEnvironmentNames(t *testing.T, path string, body []byte) ([]string, string) {
	t.Helper()

	var (
		names []string
		text  strings.Builder
	)

	var walk func(node *yaml.Node, key string)

	walk = func(node *yaml.Node, key string) {
		switch node.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, child := range node.Content {
				walk(child, key)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(node.Content); i += 2 {
				name, value := node.Content[i].Value, node.Content[i+1]
				if key == "env" && value.Kind == yaml.ScalarNode && inputExpression.MatchString(value.Value) {
					if !slices.Contains(names, name) {
						names = append(names, name)
					}

					continue
				}

				walk(value, name)
			}
		case yaml.ScalarNode:
			for line := range strings.SplitSeq(node.Value, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "#") {
					text.WriteString(line)
					text.WriteByte('\n')
				}
			}
		case yaml.AliasNode:
			// The anchored value is walked where it is defined.
		}
	}

	decoder := yaml.NewDecoder(bytes.NewReader(body))

	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatalf("%s: %v", path, err)
		}

		walk(&doc, "")
	}

	slices.Sort(names)

	return names, text.String()
}

// readerDotDirs are the hidden directories that hold the engine's scripts.
var readerDotDirs = []string{".engine", ".forgejo", ".github", ".gitlab"}

// engineReaderText is every non-test Go file and shell script of the
// repository, where a step's command reads its environment.
func engineReaderText(t *testing.T) string {
	t.Helper()

	root := reporoot.Path(t)

	var text strings.Builder

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") && !slices.Contains(readerDotDirs, entry.Name()) || entry.Name() == "testdata") {
				return filepath.SkipDir
			}

			return nil
		}

		name := entry.Name()
		if !entry.Type().IsRegular() || strings.HasSuffix(name, "_test.go") ||
			!strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".sh") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		text.Write(body)
		text.WriteByte('\n')

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return text.String()
}

var identifier = regexp.MustCompile(`[A-Za-z0-9_]+`)

// identifiers is the set of words of text that could name a variable.
func identifiers(text string) map[string]bool {
	set := map[string]bool{}
	for _, word := range identifier.FindAllString(text, -1) {
		set[word] = true
	}

	return set
}
