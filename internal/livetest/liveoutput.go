// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	liveScenarioPrefix = "FORGE_CI_LIVE_SCENARIO "
	livePackageBegin   = "FORGE_CI_LIVE_PACKAGE_BEGIN "
	livePackageEnd     = "FORGE_CI_LIVE_PACKAGE_END "
	liveVerdictFailed  = "failed"
	testPass           = "PASS"
	testFail           = "FAIL"
)

// The verdicts of a live run. verdictSkipped is the one whose human line
// says more than its name.
const (
	verdictPassed         = "passed"
	verdictInterrupted    = "interrupted"
	verdictInfrastructure = "infrastructure"
	verdictSkipped        = "skipped"
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
	verdict, err := ReadLiveRun(log, exitCode)

	return verdict.Result(), err
}

// LiveRunVerdict is the machine-readable account of one live run that
// verdict.json holds beside the run's log. Its run header is what the run
// script hands over, limited to the inputs the log's header records; the rest
// is read from the retained log, which the run script has already redacted.
// So it can hold no more than the log does.
type LiveRunVerdict struct {
	// Verdict is passed, failed, interrupted, skipped or infrastructure: the
	// lab, not the product, failed every test that failed.
	Verdict  string        `json:"verdict"`
	ExitCode int           `json:"exit_code"`
	Run      LiveRunHeader `json:"run"`
	// Tests are the leaf tests that ran, in the order they started.
	Tests          []LeafTestResult            `json:"tests"`
	Infrastructure []LiveInfrastructureFailure `json:"infrastructure,omitempty"`
	AdminPolicy    []LiveAdminPolicyDecision   `json:"admin_policy,omitempty"`
}

// LiveRunHeader is what the run was asked to do, as the run script holds it
// and writes it into the log's header.
type LiveRunHeader struct {
	StartedAt    string `json:"started_at,omitempty"`
	SourceCommit string `json:"source_commit,omitempty"`
	Profile      string `json:"profile,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Scenario     string `json:"scenario,omitempty"`
	// Inputs are the consumer inputs of LiveRunInputs that were set, keyed
	// in lower case as the log's header has them.
	Inputs map[string]string `json:"inputs,omitempty"`
}

// LiveRunInputs are the consumer inputs a run records, the same names the run
// script's header loop logs: each names a setting, a file or a user and never
// carries a secret. An external registry's token stays in its file.
var LiveRunInputs = []string{
	"FORGE_CI_LIVE_DEMO", "FORGE_CI_LIVE_CONSUMER", "FORGE_CI_LIVE_CONSUMER_EVENT",
	"FORGE_CI_LIVE_CONSUMER_SETTINGS", "FORGE_CI_LIVE_CONSUMER_VISIBILITY", "FORGE_CI_LIVE_IMAGE_PLAN_JOB_IMAGE",
	"FORGE_CI_LIVE_EXTERNAL_REGISTRY_TOKEN_FILE", "FORGE_CI_LIVE_EXTERNAL_REGISTRY_USER", "FORGE_CI_LIVE_GITLAB_IMAGE_BUILDER", "FORGE_CI_LIVE_FORGEJO_IMAGE_BUILDER",
}

// LiveRunHeaderFrom reads the run's header from the environment the run
// script hands the classifier: the run fields it passes, and the inputs of
// LiveRunInputs, no others.
func LiveRunHeaderFrom(getenv func(string) string) LiveRunHeader {
	header := LiveRunHeader{
		StartedAt:    getenv("FORGE_CI_LIVE_RUN_STARTED_AT"),
		SourceCommit: getenv("FORGE_CI_LIVE_RUN_SOURCE_COMMIT"),
		Profile:      getenv("FORGE_CI_LIVE_PROFILE"),
		Provider:     getenv("FORGE_CI_LIVE_PROVIDER"),
		Scenario:     getenv("FORGE_CI_LIVE_RUN_SCENARIO"),
	}

	for _, name := range LiveRunInputs {
		if value := getenv(name); value != "" {
			if header.Inputs == nil {
				header.Inputs = map[string]string{}
			}

			header.Inputs[strings.ToLower(name)] = value
		}
	}

	return header
}

// LeafTestResult is one leaf test: pass, fail or skip, or unfinished when
// the log has its start and no result.
type LeafTestResult struct {
	Name           string  `json:"name"`
	Package        string  `json:"package"`
	Result         string  `json:"result"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
}

// LiveInfrastructureFailure is one FORGE_CI_LIVE_INFRASTRUCTURE line.
type LiveInfrastructureFailure struct {
	Test    string `json:"test"`
	Package string `json:"package"`
	Detail  string `json:"detail"`
}

// LiveAdminPolicyDecision is one admin-policy decision line a job log in the
// run's log reported.
type LiveAdminPolicyDecision struct {
	PolicySHA256 string `json:"policy_sha256"`
	Check        string `json:"check"`
	Result       string `json:"result"`
	Enforcement  bool   `json:"enforcement"`
}

// ReadLiveRun classifies a run from its exit status and its log, and records
// what the log says of it. A nil log, as for a run whose log cannot be
// opened, is classified from the status alone.
func ReadLiveRun(log io.Reader, exitCode int) (LiveRunVerdict, error) {
	scan := newLiveRunScan()

	if log != nil {
		scanner := bufio.NewScanner(log)
		scanner.Buffer(make([]byte, 4096), 16<<20)

		for scanner.Scan() {
			scan.consume(scanner.Text())
		}

		if err := scanner.Err(); err != nil {
			// Unread evidence cannot pass, nor tell the lab from the product.
			verdict := scan.verdict
			verdict.ExitCode, verdict.Verdict = max(exitCode, 1), liveVerdictFailed

			if exitCode == 130 {
				verdict.Verdict = verdictInterrupted
			}

			return verdict, fmt.Errorf("read live scenario results: %w", err)
		}
	}

	scan.flush()

	verdict := scan.verdict
	verdict.ExitCode, verdict.Verdict = scan.classify(exitCode)

	return verdict, nil
}

// WriteLiveRunVerdict writes verdict as verdict.json in dir, readable by its
// owner alone, as the log beside it is.
func WriteLiveRunVerdict(dir string, verdict LiveRunVerdict) error {
	if verdict.Tests == nil {
		verdict.Tests = []LeafTestResult{}
	}

	body, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the run verdict: %w", err)
	}

	path := filepath.Join(dir, "verdict.json")
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write the run verdict: %w", err)
	}

	return nil
}

// Result is the verdict as the run script prints it: its exit status, and
// the Live result line's wording.
func (verdict LiveRunVerdict) Result() LiveRunResult {
	if verdict.Verdict == verdictSkipped {
		return LiveRunResult{verdict.ExitCode, "skipped: no scenario ran; the log says why"}
	}

	return LiveRunResult{verdict.ExitCode, verdict.Verdict}
}

// classify decides the verdict and its exit status. A failed process stays
// failed, except that a run whose every failure the lab caused is called
// infrastructure, still with its non-zero status.
func (scan *liveRunScan) classify(exitCode int) (int, string) {
	switch {
	case exitCode == 130:
		return 130, verdictInterrupted
	case exitCode != 0 && scan.onlyInfrastructure():
		return exitCode, verdictInfrastructure
	case exitCode != 0:
		return exitCode, liveVerdictFailed
	case scan.failed || scan.active != "":
		return 1, liveVerdictFailed
	case scan.passed:
		return 0, verdictPassed
	default:
		return 1, verdictSkipped
	}
}

// onlyInfrastructure reports whether the lab caused every failure in the
// log: some test failed, every failed test is marked or the ancestor of a
// marked test, and nothing else went wrong -- no test left unfinished, no
// broken package boundary.
func (scan *liveRunScan) onlyInfrastructure() bool {
	if scan.broken || scan.active != "" || len(scan.failures) == 0 || len(scan.verdict.Infrastructure) == 0 {
		return false
	}

	for _, test := range scan.verdict.Tests {
		if test.Result == "unfinished" {
			return false
		}
	}

	for _, failed := range scan.failures {
		covered := false

		for _, marked := range scan.verdict.Infrastructure {
			if marked.Package == failed.Package && (marked.Test == failed.Name || strings.HasPrefix(marked.Test, failed.Name+"/")) {
				covered = true
			}
		}

		if !covered {
			return false
		}
	}

	return true
}

// liveRunScan accumulates the package, scenario and test records of one live
// run log, line by line.
type liveRunScan struct {
	lineResult, lineRun, lineAdminPolicy *regexp.Regexp

	active    string
	results   map[string]string
	elapsed   map[string]float64
	scenarios map[string]bool
	runs      map[string]bool
	order     []string

	verdict  LiveRunVerdict
	failures []LeafTestResult
	policies map[LiveAdminPolicyDecision]bool

	passed, failed, broken bool
}

func newLiveRunScan() *liveRunScan {
	return &liveRunScan{
		lineResult: regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): (\S+)(?: \(([0-9.]+)s\))?`),
		// testing writes RUN records at column zero, even for nested subtests.
		// t.Log's quoted/multiline output is indented and cannot introduce a run.
		lineRun: regexp.MustCompile(`^=== RUN[ \t]+(\S+)[ \t]*$`),
		// As internal/app/enforcement prints it, from a job's log the
		// scenario printed or retained; a not-waivable line has no count.
		lineAdminPolicy: regexp.MustCompile(`(?:Admin policy sha256|Enforcement policy default-sha256):([0-9a-f]{64}): (\S+) result=(\S+)(?: count=\d+)? enforcement=(true|false)`),
		policies:        map[LiveAdminPolicyDecision]bool{},
	}
}

// consume records one log line.
func (scan *liveRunScan) consume(line string) {
	scan.consumeAdminPolicy(line)

	switch {
	case strings.HasPrefix(line, livePackageBegin):
		scan.beginPackage(strings.TrimPrefix(line, livePackageBegin))
	case strings.HasPrefix(line, livePackageEnd):
		scan.endPackage(strings.TrimPrefix(line, livePackageEnd))
	case strings.HasPrefix(line, liveScenarioPrefix):
		name := strings.TrimPrefix(line, liveScenarioPrefix)
		if scan.active != "" && scan.runs[name] {
			scan.scenarios[name] = true
		}
	case strings.HasPrefix(line, liveInfrastructurePrefix):
		if test, detail, ok := strings.Cut(strings.TrimPrefix(line, liveInfrastructurePrefix), " "); ok && scan.active != "" {
			scan.verdict.Infrastructure = append(scan.verdict.Infrastructure,
				LiveInfrastructureFailure{Test: test, Package: scan.active, Detail: detail})
		}
	default:
		scan.consumeTestRecord(line)
	}
}

// beginPackage opens a package's records; one already open, or a nameless
// one, breaks the log's structure.
func (scan *liveRunScan) beginPackage(name string) {
	if scan.active != "" || name == "" {
		scan.failed, scan.broken = true, true
	}

	scan.flush()
	scan.active = name
	scan.results, scan.scenarios = map[string]string{}, map[string]bool{}
	scan.runs, scan.elapsed, scan.order = map[string]bool{}, map[string]float64{}, nil
}

// endPackage closes the open package, which must be the one named.
func (scan *liveRunScan) endPackage(name string) {
	if scan.active == "" || name != scan.active {
		scan.failed, scan.broken = true, true
	} else if passingLiveLeaf(scan.results, scan.scenarios, scan.runs) {
		scan.passed = true
	}

	scan.flush()
	scan.active = ""
}

// consumeAdminPolicy records an admin-policy decision once, wherever in the
// log its line appears.
func (scan *liveRunScan) consumeAdminPolicy(line string) {
	match := scan.lineAdminPolicy.FindStringSubmatch(line)
	if match == nil {
		return
	}

	decision := LiveAdminPolicyDecision{PolicySHA256: match[1], Check: match[2], Result: match[3], Enforcement: match[4] == "true"}
	if !scan.policies[decision] {
		scan.policies[decision] = true
		scan.verdict.AdminPolicy = append(scan.verdict.AdminPolicy, decision)
	}
}

// consumeTestRecord records a testing RUN or result line.
func (scan *liveRunScan) consumeTestRecord(line string) {
	if match := scan.lineRun.FindStringSubmatch(line); match != nil && scan.active != "" {
		if scan.runs[match[1]] {
			scan.failed, scan.broken = true, true
		}

		scan.runs[match[1]] = true
		scan.order = append(scan.order, match[1])
	}

	match := scan.lineResult.FindStringSubmatch(line)
	if match == nil {
		return
	}

	if match[1] == testFail {
		scan.failed = true
	}

	if scan.active == "" {
		if match[1] == testFail {
			scan.broken = true
		}

		return
	}

	if _, duplicate := scan.results[match[2]]; duplicate {
		scan.failed, scan.broken = true, true
	}

	scan.results[match[2]] = match[1]

	if seconds, err := strconv.ParseFloat(match[3], 64); err == nil {
		scan.elapsed[match[2]] = seconds
	}
}

// flush records the tests of the package that is ending: every leaf that
// ran, and every test that failed.
func (scan *liveRunScan) flush() {
	if scan.active == "" {
		return
	}

	for _, name := range scan.order {
		test := LeafTestResult{Name: name, Package: scan.active, Result: liveTestResult(scan.results[name]), ElapsedSeconds: scan.elapsed[name]}
		if test.Result == "fail" {
			scan.failures = append(scan.failures, test)
		}

		if liveLeaf(name, scan.runs, scan.results) {
			scan.verdict.Tests = append(scan.verdict.Tests, test)
		}
	}

	// A FAIL with no RUN still fails the run, and must be accounted for.
	for name, result := range scan.results {
		if result == testFail && !scan.runs[name] {
			scan.failures = append(scan.failures, LeafTestResult{Name: name, Package: scan.active, Result: "fail"})
		}
	}

	scan.order = nil
}

// liveTestResult spells a testing result for verdict.json.
func liveTestResult(result string) string {
	if result == "" {
		return "unfinished"
	}

	return strings.ToLower(result)
}

// liveLeaf reports whether no other test of the package ran, or reported,
// under name.
func liveLeaf(name string, runs map[string]bool, results map[string]string) bool {
	for other := range runs {
		if strings.HasPrefix(other, name+"/") {
			return false
		}
	}

	for other := range results {
		if strings.HasPrefix(other, name+"/") {
			return false
		}
	}

	return true
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
