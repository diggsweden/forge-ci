// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package enforcement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// codeQualityIssue is one entry of a GitLab Code Quality report: the fields
// GitLab reads, and no other.
type codeQualityIssue struct {
	Description string              `json:"description"`
	CheckName   string              `json:"check_name"`
	Fingerprint string              `json:"fingerprint"`
	Severity    string              `json:"severity"`
	Location    codeQualityLocation `json:"location"`
}

type codeQualityLocation struct {
	Path  string           `json:"path"`
	Lines codeQualityLines `json:"lines"`
}

type codeQualityLines struct {
	Begin int `json:"begin"`
}

type qualityResult struct {
	RuleID  string `json:"ruleId"`
	Level   string `json:"level"`
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
			Region struct {
				StartLine int `json:"startLine"`
			} `json:"region"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

// codeQuality renders the results the security SARIF leaves out as a GitLab
// Code Quality report: the findings of the failed quality checks, without the
// suppressed ones and those the merge base already had. A passed check adds
// nothing, so an empty report is no verdict: the job's result is.
//
// GitLab matches a finding of the merge request with one of the target branch
// by fingerprint. It is the check, rule, path and message without the line,
// so code moving above a finding does not make it new; a repeat of the same
// tuple is numbered, because GitLab keeps one issue per fingerprint.
func codeQuality(raws []json.RawMessage) ([]byte, error) {
	issues := []codeQualityIssue{}
	seen := map[string]int{}

	for _, raw := range raws {
		result, err := parseNanoResult(raw)
		if err != nil {
			return nil, err
		}

		if result.Properties.Status != failedStatus || result.BaselineState == "unchanged" || result.suppressed() {
			continue
		}

		var finding qualityResult
		if err := json.Unmarshal(raw, &finding); err != nil {
			return nil, fmt.Errorf("invalid nanolinter finding: %w", errs.ErrMalformedInput)
		}

		issue := codeQualityIssue{
			Description: finding.Message.Text,
			CheckName:   "nanolinter/" + result.Properties.CheckID,
			Severity:    codeQualitySeverity(finding.Level, result.Properties.Policy),
			Location:    codeQualityLocation{Path: ".", Lines: codeQualityLines{Begin: 1}},
		}
		if finding.RuleID != "" && finding.RuleID != result.Properties.CheckID {
			issue.CheckName += "/" + finding.RuleID
		}

		if len(finding.Locations) > 0 {
			location := finding.Locations[0].PhysicalLocation
			if location.ArtifactLocation.URI != "" {
				issue.Location.Path = location.ArtifactLocation.URI
			}

			if location.Region.StartLine > 0 {
				issue.Location.Lines.Begin = location.Region.StartLine
			}
		}

		identity, _ := json.Marshal([]string{issue.CheckName, issue.Location.Path, issue.Description}) //nolint:errchkjson // a string slice always encodes.
		repeat := seen[string(identity)]
		seen[string(identity)]++

		sum := sha256.Sum256(append(identity, strconv.Itoa(repeat)...))
		issue.Fingerprint = hex.EncodeToString(sum[:])

		issues = append(issues, issue)
	}

	return json.MarshalIndent(issues, "", "  ")
}

// codeQualitySeverity maps the SARIF level. A check the plan only reports is
// never more than minor: it does not fail the job.
func codeQualitySeverity(level, policy string) string {
	switch {
	case level == "note" || level == "none":
		return "info"
	case level == "warning" || policy == "warn":
		return "minor"
	default:
		return "major"
	}
}
