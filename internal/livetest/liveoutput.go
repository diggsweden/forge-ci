// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	liveScenarioPrefix = "FORGE_CI_LIVE_SCENARIO "
	livePackageBegin   = "FORGE_CI_LIVE_PACKAGE_BEGIN "
	livePackageEnd     = "FORGE_CI_LIVE_PACKAGE_END "
	liveVerdictFailed  = "failed"
	testPass           = "PASS"
)

// RegisterLiveScenario identifies a real live-test entrypoint, including its
// subtests. Registration alone is not execution: a passing leaf is still required.
// Keep this explicit in scenarios; guard helpers are also used by offline tests.
func RegisterLiveScenario(t interface{ Name() string }) {
	fmt.Printf("%s%s\n", liveScenarioPrefix, t.Name())
}

// LiveRunResult distinguishes real scenario execution from Go's package-level
// PASS when the selected tests or all of their subtests skipped.
type LiveRunResult struct {
	ExitCode int
	Verdict  string
}

// ClassifyLiveRun preserves process failures and requires a successful leaf
// scenario before calling a clean process exit a passing live run.
func ClassifyLiveRun(log io.Reader, exitCode int) (LiveRunResult, error) {
	if exitCode == 130 {
		return LiveRunResult{130, "interrupted"}, nil
	}

	if exitCode != 0 {
		return LiveRunResult{exitCode, liveVerdictFailed}, nil
	}

	scan := liveRunScan{
		lineResult: regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): (\S+)`),
		// testing writes RUN records at column zero, even for nested subtests.
		// t.Log's quoted/multiline output is indented and cannot introduce a run.
		lineRun: regexp.MustCompile(`^=== RUN[ \t]+(\S+)[ \t]*$`),
	}
	scanner := bufio.NewScanner(log)
	scanner.Buffer(make([]byte, 4096), 16<<20)

	for scanner.Scan() {
		scan.consume(scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return LiveRunResult{1, liveVerdictFailed}, fmt.Errorf("read live scenario results: %w", err)
	}

	if scan.failed || scan.active != "" {
		return LiveRunResult{1, liveVerdictFailed}, nil
	}

	if scan.passed {
		return LiveRunResult{0, "passed"}, nil
	}

	return LiveRunResult{1, "skipped: no scenario ran; the log says why"}, nil
}

// liveRunScan accumulates the package, scenario and test records of one live
// run log, line by line.
type liveRunScan struct {
	lineResult, lineRun *regexp.Regexp

	active    string
	results   map[string]string
	scenarios map[string]bool
	runs      map[string]bool

	passed, failed bool
}

// consume records one log line.
func (scan *liveRunScan) consume(line string) {
	switch {
	case strings.HasPrefix(line, livePackageBegin):
		if scan.active != "" || strings.TrimPrefix(line, livePackageBegin) == "" {
			scan.failed = true
		}

		scan.active = strings.TrimPrefix(line, livePackageBegin)
		scan.results, scan.scenarios = map[string]string{}, map[string]bool{}
		scan.runs = map[string]bool{}
	case strings.HasPrefix(line, livePackageEnd):
		if scan.active == "" || strings.TrimPrefix(line, livePackageEnd) != scan.active {
			scan.failed = true
		} else if passingLiveLeaf(scan.results, scan.scenarios, scan.runs) {
			scan.passed = true
		}

		scan.active = ""
	case strings.HasPrefix(line, liveScenarioPrefix):
		name := strings.TrimPrefix(line, liveScenarioPrefix)
		if scan.active != "" && scan.runs[name] {
			scan.scenarios[name] = true
		}
	default:
		scan.consumeTestRecord(line)
	}
}

// consumeTestRecord records a testing RUN or result line.
func (scan *liveRunScan) consumeTestRecord(line string) {
	if match := scan.lineRun.FindStringSubmatch(line); match != nil && scan.active != "" {
		if scan.runs[match[1]] {
			scan.failed = true
		}

		scan.runs[match[1]] = true
	}

	match := scan.lineResult.FindStringSubmatch(line)
	if match == nil {
		return
	}

	if match[1] == "FAIL" {
		scan.failed = true
	}

	if scan.active == "" {
		return
	}

	if _, duplicate := scan.results[match[2]]; duplicate {
		scan.failed = true
	}

	scan.results[match[2]] = match[1]
}

func passingLiveLeaf(results map[string]string, scenarios, runs map[string]bool) bool {
	for name := range runs {
		if results[name] != testPass {
			continue
		}

		leaf, ancestorsPassed := true, true
		// A child result without its RUN record cannot qualify, but must
		// still stop a parent PASS from masquerading as a passing leaf.
		for other := range results {
			if strings.HasPrefix(other, name+"/") {
				leaf = false
			}
		}

		for other := range runs {
			if strings.HasPrefix(other, name+"/") {
				leaf = false
			}
			// Only actual RUN identities are ancestors. A slash in a t.Run
			// name does not imply an intermediate test was ever executed.
			if strings.HasPrefix(name, other+"/") && results[other] != testPass {
				ancestorsPassed = false
			}
		}

		if leaf && ancestorsPassed {
			for scenario := range scenarios {
				if results[scenario] == testPass && (name == scenario || strings.HasPrefix(name, scenario+"/")) {
					return true
				}
			}
		}
	}

	return false
}
