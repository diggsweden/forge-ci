// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package enforcement

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestNanolinterSeparatesDependencyFindingsFromLint(t *testing.T) {
	body := []byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter","rules":[{"id":"format"},{"id":"osv"}]}},"results":[
{"ruleId":"format","message":{"text":"format finding"},"properties":{"source":"curated","check_id":"format","class":"formatter","status":"fail"}},
{"ruleId":"CVE-EXAMPLE","message":{"text":"dependency finding"},"properties":{"source":"curated","check_id":"osv","class":"security","status":"fail"}},
{"ruleId":"sast","message":{"text":"source finding"},"properties":{"source":"curated","check_id":"sast","class":"security","status":"fail"}}
]}]}`)

	report, err := ParseNanolinter(body)
	if err != nil || report.LintFindings != 2 || report.VulnerabilityFindings != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}

	var security struct {
		Runs []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(report.SecuritySARIF, &security); err != nil {
		t.Fatal(err)
	}

	if len(security.Runs) != 1 || len(security.Runs[0].Results) != 2 || security.Runs[0].Results[0].RuleID != "CVE-EXAMPLE" {
		t.Fatalf("security scope lost or included quality findings: %s", report.SecuritySARIF)
	}

	decision, err := (Policy{EnforceVulnerabilities: true}).Decide(Vulnerabilities, report.VulnerabilityFindings)
	if err != nil || !decision.Enforced {
		t.Fatal("disabling lint waived dependency findings")
	}
}

// TestNanolinterReportOnlyChecksAreNoLintFindings: a check the plan demotes to
// warn is reported and not counted by the lint gate, while a check the floor
// requires is reported as blocking and is counted. A demoted dependency check
// still counts as a vulnerability.
func TestNanolinterReportOnlyChecksAreNoLintFindings(t *testing.T) {
	t.Parallel()

	report, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[
{"ruleId":"sast","properties":{"source":"curated","check_id":"sast","class":"security","policy":"warn","status":"fail"}},
{"ruleId":"secret","properties":{"source":"curated","check_id":"secrets","class":"security","policy":"block","status":"fail"}},
{"ruleId":"CVE-EXAMPLE","properties":{"source":"curated","check_id":"osv","class":"security","policy":"warn","status":"fail"}},
{"ruleId":"future","properties":{"source":"curated","check_id":"future-scanner","class":"security","policy":"warn","status":"fail"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}

	if report.LintFindings != 1 || report.VulnerabilityFindings != 2 {
		t.Fatalf("lint = %d, vulnerabilities = %d, want 1 and 2", report.LintFindings, report.VulnerabilityFindings)
	}

	if !strings.Contains(string(report.SecuritySARIF), `"ruleId": "sast"`) {
		t.Fatal("a report-only SAST finding must stay in the security SARIF")
	}
}

// On a pull request nanolinter marks each dependency advisory with the SARIF
// baselineState. One the merge base already had is not a vulnerability
// finding of the request; it is still listed in the security SARIF.
func TestNanolinterUnchangedAdvisoriesAreNoVulnerabilityFindings(t *testing.T) {
	t.Parallel()

	report, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[
{"ruleId":"CVE-OLD","baselineState":"unchanged","properties":{"source":"curated","check_id":"osv","class":"security","status":"fail"}},
{"ruleId":"CVE-NEW","baselineState":"new","properties":{"source":"curated","check_id":"osv","class":"security","status":"fail"}},
{"ruleId":"CVE-UNMARKED","properties":{"source":"curated","check_id":"trivy-fs","class":"security","status":"fail"}},
{"ruleId":"secret","baselineState":"unchanged","properties":{"source":"curated","check_id":"secrets","class":"security","status":"fail"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}

	if report.LintFindings != 1 || report.VulnerabilityFindings != 2 {
		t.Fatalf("lint = %d, vulnerabilities = %d, want 1 and 2", report.LintFindings, report.VulnerabilityFindings)
	}

	if !strings.Contains(string(report.SecuritySARIF), `"ruleId": "CVE-OLD"`) {
		t.Fatal("an unchanged advisory must stay in the security SARIF")
	}
}

func TestNanolinterUnknownSecurityClassIsNeverLintOnly(t *testing.T) {
	report, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[{"ruleId":"future","properties":{"source":"curated","check_id":"future-scanner","class":"security","status":"fail"}}]}]}`))
	if err != nil || report.LintFindings != 1 || report.VulnerabilityFindings != 1 {
		t.Fatalf("unknown finding escaped an enforcing class: %+v %v", report, err)
	}
}

func TestNanolinterRefusesMissingAndMalformedReports(t *testing.T) {
	for _, body := range []string{"", `{}`, `null`, `{"version":"2.1.0","runs":[]}`, `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"other"}},"results":[]}]}`, `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}}}]}`, `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[null]}]}`} {
		if _, err := ParseNanolinter([]byte(body)); !errors.Is(err, errs.ErrMalformedInput) {
			t.Errorf("accepted %s: %v", body, err)
		}
	}
}

func TestNanolinterOperationalFailureCannotBecomeFindings(t *testing.T) {
	for _, properties := range []string{
		`"details":"tool failed (exit 2)","exit_code":2`,
		`"failure_reason_category":"network"`,
		`"failure_reason_category":"internal_failure"`,
		`"failure_reason_category":"tool_missing"`,
	} {
		body := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[
{"ruleId":"sast","properties":{"source":"curated","check_id":"sast","class":"security","status":"fail",` + properties + `}},
{"ruleId":"CVE-EXAMPLE","properties":{"source":"curated","check_id":"osv","class":"security","status":"fail"}}]}]}`

		report, err := ParseNanolinter([]byte(body))
		if err != nil || !errors.Is(report.Unavailable, errs.ErrPrerequisite) || report.LintFindings != 0 {
			t.Fatalf("operational failure was eligible for a policy waiver: %s: %+v %v", properties, report, err)
		}

		// The failed check must not hide what the other check found.
		if report.VulnerabilityFindings != 1 || !strings.Contains(string(report.SecuritySARIF), "CVE-EXAMPLE") {
			t.Fatalf("findings lost behind another check's failure: %s: %+v", properties, report)
		}

		if outcome, detail := report.Outcome(Lint); outcome != "incomplete" || detail != "could-not-complete=sast" {
			t.Fatalf("failed check reads as %q %q", outcome, detail)
		}

		if outcome, detail := report.Outcome(Vulnerabilities); outcome != "" || detail != "" {
			t.Fatalf("complete gate reads as %q %q", outcome, detail)
		}
	}
}

// A repository with only a Containerfile has nothing for the iac check to
// scan. That is not a check that could not complete: reported n/a it passes
// and is listed, and only a check reported skip fails the report. Nanolinter
// 0.8.6 writes no row for either, which reads as a report without them.
func TestNanolinterNothingToScanIsNotACheckThatCouldNotComplete(t *testing.T) {
	const iac = `{"ruleId":"iac","properties":{"source":"curated","check_id":"iac","class":"security","status":"%s"}}`

	for _, test := range []struct {
		name, rows      string
		unavailable     bool
		outcome, detail string
	}{
		{"no row, as Nanolinter 0.8.6 reports it", "", false, "", ""},
		{"nothing to scan", fmt.Sprintf(iac, "n/a"), false, "not-applicable", "not-applicable=iac"},
		{"the scanner could not run", fmt.Sprintf(iac, "skip"), true, "incomplete", "could-not-complete=iac"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[` + test.rows + `]}]}`))
			if err != nil || report.LintFindings+report.VulnerabilityFindings != 0 {
				t.Fatalf("report=%+v err=%v", report, err)
			}

			if errors.Is(report.Unavailable, errs.ErrPrerequisite) != test.unavailable {
				t.Fatalf("unavailable=%v", report.Unavailable)
			}

			// iac is a security check outside the two lists: both gates own it.
			for _, gate := range []Check{Lint, Vulnerabilities} {
				if outcome, detail := report.Outcome(gate); outcome != test.outcome || detail != test.detail {
					t.Errorf("%s reads as %q %q", gate, outcome, detail)
				}
			}
		})
	}
}

func TestTestArgumentOptOutSpellingsAreRefused(t *testing.T) {
	for _, args := range [][]string{{"-DskipTests"}, {"-Dmaven.test.skip=true"}, {"--define", "maven.test.failure.ignore=true"}, {"--define=skipTests=true"}, {"-D", "skipTests=true"}} {
		if err := MavenTestArguments(args); !errors.Is(err, errs.ErrUsage) {
			t.Errorf("accepted %v: %v", args, err)
		}
	}

	// Fail-never exits 0 whatever the tests did; skip.exec skips them; the
	// selection properties narrow them to nothing; reruns pass a failed test.
	// Maven takes an option name after one dash or two and a -D value after
	// "="; abbreviations are refused in case a later parser accepts them.
	for _, args := range [][]string{
		{"-fn"}, {"--fn"}, {"-fail-never"}, {"--fail-never"}, {"--fail-nev"}, {"-B", "--fail-n"},
		{"-define", "skipTests"}, {"-define=skipTests=yes"}, {"--D=skipTests=yes"}, {"-D=skipTests"}, {"--DskipTests"},
		{"-Dmaven.test.skip.exec=true"}, {"--def", "skipTests"}, {"--defi=maven.test.skip=true"},
		{"-Dsurefire.rerunFailingTestsCount=2"}, {"-Dfailsafe.rerunFailingTestsCount=1"},
		{"-Dtest=None", "-Dsurefire.failIfNoSpecifiedTests=false"}, {"-Dtest=OneTest"}, {"-Dit.test=None"},
		{"-Dgroups=fast"}, {"-DexcludedGroups=slow"}, {"-DfailIfNoTests=false"}, {"-Dsurefire.excludesFile=skip.txt"},
	} {
		if err := MavenTestArguments(args); !errors.Is(err, errs.ErrUsage) {
			t.Errorf("accepted %v: %v", args, err)
		}
	}

	for _, args := range [][]string{
		{"--batch-mode", "-DskipTests=false", "-Drevision=1.2.3"},
		{"-fae", "--fail-at-end", "--fail-fast", "--debug", "-Dtestcontainers.reuse=true", "-Dtest.timeout=60"},
		{"-Dsurefire.failIfNoSpecifiedTests=true", "-DfailIfNoTests=true", "-Dsurefire.rerunFailingTestsCount=0"},
	} {
		if err := MavenTestArguments(args); err != nil {
			t.Errorf("refused %v: %v", args, err)
		}
	}

	for _, args := range [][]string{{"build", "-x", "test"}, {"build", "--exclude-task=test"}, {"build", "-xtest"}} {
		if err := GradleTestArguments(args); !errors.Is(err, errs.ErrUsage) {
			t.Errorf("accepted %v: %v", args, err)
		}
	}
}

// Two conditions the rule above turns on. A skipped check that is not a
// security check fails nothing, and a gate where one scan ran is not
// "not-applicable" for another that does not apply.
func TestNanolinterSkipAndNotApplicableAreScopedToTheirGate(t *testing.T) {
	const row = `{"ruleId":"%[1]s","properties":{"source":"curated","check_id":"%[1]s","class":"%[2]s","status":"%[3]s"}}`

	parse := func(rows string) NanolinterReport {
		t.Helper()

		report, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[` + rows + `]}]}`))
		if err != nil {
			t.Fatal(err)
		}

		return report
	}

	formatter := parse(fmt.Sprintf(row, "format", "formatter", "skip"))
	if outcome, detail := formatter.Outcome(Lint); formatter.Unavailable != nil || outcome != "" || detail != "" {
		t.Errorf("a skipped formatter reads as %q %q, unavailable=%v", outcome, detail, formatter.Unavailable)
	}

	scanned := parse(fmt.Sprintf(row, "osv", "security", "n/a") + "," + fmt.Sprintf(row, "trivy-fs", "security", "pass"))
	if outcome, detail := scanned.Outcome(Vulnerabilities); outcome != "" || detail != "not-applicable=osv" {
		t.Errorf("a gate whose other scan ran reads as %q %q", outcome, detail)
	}
}

// A check without a verdict is recorded on the gate it belongs to and on no
// other, so each gate's enforcement decides for its own checks.
func TestIncompleteIsRecordedPerGate(t *testing.T) {
	t.Parallel()

	report, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[
{"properties":{"source":"curated","check_id":"osv","class":"security","status":"fail","failure_reason_category":"tool_failure"}},
{"properties":{"source":"curated","check_id":"sast","class":"security","status":"pass"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}

	if err := report.Incomplete(Vulnerabilities); !errors.Is(err, errs.ErrPrerequisite) || !strings.Contains(err.Error(), "osv") {
		t.Fatalf("vulnerabilities gate: %v", err)
	}

	if err := report.Incomplete(Lint); err != nil {
		t.Fatalf("lint gate took the dependency check's failure: %v", err)
	}
}

// TestNanolinterCodeQualityListsTheFailedQualityFindings: the report holds
// what the security SARIF leaves out, at each finding's file and line. A
// passed check, a suppressed finding and one the merge base had add nothing,
// and a repeated finding keeps its own fingerprint.
func TestNanolinterCodeQualityListsTheFailedQualityFindings(t *testing.T) {
	t.Parallel()

	const located = `"locations":[{"physicalLocation":{"artifactLocation":{"uri":"docs/a.md"},"region":{"startLine":%d}}}]`

	finding := func(line int, extra string) string {
		return `{"ruleId":"MD013","level":"error","message":{"text":"line too long"},` + fmt.Sprintf(located, line) + extra +
			`,"properties":{"source":"curated","check_id":"markdown","class":"formatter","policy":"block","status":"fail"}}`
	}

	report, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[` +
		finding(3, "") + "," + finding(9, "") + "," +
		finding(12, `,"suppressions":[{"status":"accepted"}]`) + "," + finding(15, `,"baselineState":"unchanged"`) + `,
{"ruleId":"yaml","level":"warning","message":{"text":"yaml failed"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"."}}}],"properties":{"source":"curated","check_id":"yaml","class":"formatter","policy":"warn","status":"fail"}},
{"ruleId":"SC2086","level":"note","message":{"text":"quote this"},"properties":{"source":"curated","check_id":"shell","class":"diagnostic","status":"pass"}},
{"ruleId":"secret","level":"error","message":{"text":"a secret"},"properties":{"source":"curated","check_id":"secrets","class":"security","status":"fail"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}

	var issues []codeQualityIssue
	if err := json.Unmarshal(report.CodeQuality, &issues); err != nil {
		t.Fatal(err)
	}

	if len(issues) != 3 {
		t.Fatalf("want the two markdown findings and the yaml failure, got %s", report.CodeQuality)
	}

	first, second, whole := issues[0], issues[1], issues[2]
	if first.CheckName != "nanolinter/markdown/MD013" || first.Severity != "major" || first.Location.Path != "docs/a.md" || first.Location.Lines.Begin != 3 || second.Location.Lines.Begin != 9 {
		t.Fatalf("finding lost its rule, severity or place: %+v %+v", first, second)
	}

	if len(first.Fingerprint) != 64 || first.Fingerprint == second.Fingerprint {
		t.Fatalf("a repeated finding must keep its own fingerprint: %q %q", first.Fingerprint, second.Fingerprint)
	}

	if whole.CheckName != "nanolinter/yaml" || whole.Severity != "minor" || whole.Location.Path != "." || whole.Location.Lines.Begin != 1 {
		t.Fatalf("a failure without a finding is one repository entry: %+v", whole)
	}

	// The line is no part of the identity: a finding that moved is the same one.
	moved, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[` + finding(40, "") + `]}]}`))
	if err != nil {
		t.Fatal(err)
	}

	var movedIssues []codeQualityIssue
	if err := json.Unmarshal(moved.CodeQuality, &movedIssues); err != nil || len(movedIssues) != 1 || movedIssues[0].Fingerprint != first.Fingerprint {
		t.Fatalf("moved finding changed identity: %s err=%v", moved.CodeQuality, err)
	}

	empty, err := ParseNanolinter([]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[]}]}`))
	if err != nil || strings.TrimSpace(string(empty.CodeQuality)) != "[]" {
		t.Fatalf("no finding is an empty list, not null: %q err=%v", empty.CodeQuality, err)
	}
}
