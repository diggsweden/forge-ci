// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"encoding/json"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// rowValueDeref matches a matrix row value read as an object or a list:
// matrix.build.name, matrix.build['cache-tag'] or toJSON(matrix.archs).
var rowValueDeref = regexp.MustCompile(`matrix\.[A-Za-z0-9_-]+[.[]|matrix\['[^']*'\][.[]|toJSON\(matrix\.`)

// rowDimension matches a row value a job reads by name, matrix.arch.
var rowDimension = regexp.MustCompile(`matrix\.([A-Za-z0-9_-]+)`)

// fallbackRows matches the literal rows a planned matrix falls back to.
var fallbackRows = regexp.MustCompile(`'(\{"include":.*\})'`)

// Forgejo appends a row's values to the name of a job whose matrix comes
// from another job's output, whatever name the job sets, and shows an object
// or a list there as a Go map or slice: "Build: x (map[arch:amd64 ...])". So
// the rows of such a matrix hold short scalar values only, and the job reads
// anything else by them from its planner's outputs. The job's own name holds
// no row value, which Forgejo would show twice. The rule holds for the layer's
// workflows and every Forgejo workflow of the examples and the live fixtures.
func TestPlannedMatrixRowsHoldScalarValues(t *testing.T) {
	t.Parallel()

	planned := 0

	workflows := allWorkflows(t)
	root := reporoot.Path(t)

	for _, tree := range []string{"examples", "test/live/fixtures"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || filepath.Ext(path) != ".yml" {
				return err
			}

			relative, err := filepath.Rel(root, path)
			relative = filepath.ToSlash(relative)

			if strings.Contains(relative, "/.forgejo/workflows/") || strings.HasPrefix(relative, "examples/") && strings.Contains(relative, "forgejo/") {
				workflows = append(workflows, relative)
			}

			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, relative := range workflows {
		for name, target := range loadWorkflow(t, relative).jobs {
			matrix := mappingValue(mappingValue(target.node, "strategy"), "matrix")
			if !anyString(matrix, func(value string) bool { return strings.Contains(value, "needs.") }) {
				continue
			}

			planned++

			if anyString(target.node, rowValueDeref.MatchString) {
				t.Errorf("%s: job %s reads a value of its planned matrix row as an object or a list, which Forgejo shows in the job's name", relative, name)
			}

			if jobName := scalarString(mappingValue(target.node, "name")); strings.Contains(jobName, "matrix") {
				t.Errorf("%s: job %s is named %q, and Forgejo appends its row's values to the name already", relative, name, jobName)
			}

			checkFallbackRowsHoldTheirDimensions(t, relative, name, target)
		}
	}

	if planned == 0 {
		t.Fatal("found no matrix planned by another job to inspect")
	}
}

// Forgejo resolves runs-on and with for every row, the no-op row a planned
// matrix falls back to without a plan's rows included, and refuses one that
// reads a dimension the row lacks: the run fails before any job starts, as
// snapshot-images.yml's build did for a plan of files alone ("Build: image
// (incomplete matrix)"). So each fallback row holds every dimension they read.
func checkFallbackRowsHoldTheirDimensions(t *testing.T, relative, name string, target job) {
	t.Helper()

	literal := fallbackRows.FindStringSubmatch(scalarString(mappingValue(mappingValue(target.node, "strategy"), "matrix")))
	if literal == nil {
		return
	}

	var fallback struct {
		Include []map[string]any `json:"include"`
	}

	if err := json.Unmarshal([]byte(literal[1]), &fallback); err != nil {
		t.Errorf("%s: job %s falls back to %s, which is not a matrix: %v", relative, name, literal[1], err)

		return
	}

	read := map[string]bool{}

	for _, field := range []string{"runs-on", "with"} {
		anyString(mappingValue(target.node, field), func(value string) bool {
			for _, dimension := range rowDimension.FindAllStringSubmatch(value, -1) {
				read[dimension[1]] = true
			}

			return false
		})
	}

	for _, dimension := range slices.Sorted(maps.Keys(read)) {
		for _, row := range fallback.Include {
			if _, ok := row[dimension]; !ok {
				t.Errorf("%s: job %s reads matrix.%s in runs-on or with, which its fallback row %v lacks; Forgejo fails the run before it starts", relative, name, dimension, row)
			}
		}
	}
}
