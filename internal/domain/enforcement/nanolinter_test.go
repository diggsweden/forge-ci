// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package enforcement

import (
	"encoding/json"
	"errors"
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
		body := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"nanolinter"}},"results":[{"ruleId":"sast","properties":{"source":"curated","check_id":"sast","class":"security","status":"fail",` + properties + `}}]}]}`
		if _, err := ParseNanolinter([]byte(body)); !errors.Is(err, errs.ErrDependencyUnavailable) {
			t.Fatalf("operational failure was eligible for a policy waiver: %s: %v", properties, err)
		}
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
