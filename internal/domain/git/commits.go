// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package git

import (
	"regexp"
	"strings"
)

// Commit is what a reviewer asks of one commit of a change: who wrote it,
// whether it carries a signature and of which kind, and its trailers.
type Commit struct {
	SHA     string
	Author  string
	Subject string
	// Signature is one of SignatureKinds, or empty. It says a signature is
	// present, not that it verifies: only the forge knows the trusted keys.
	Signature string
	Trailers  []Trailer
}

// Trailer is one "Key: value" line of a commit message's last paragraph.
type Trailer struct{ Key, Value string }

var trailerLine = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*): +(\S.*)$`)

// ParseRawLog reads the output of `git log --format=raw -z`, newest first.
// A record it cannot read is left out; the others stand.
func ParseRawLog(raw string) []Commit {
	var commits []Commit

	for record := range strings.SplitSeq(raw, "\x00") {
		if commit, ok := parseRawCommit(record); ok {
			commits = append(commits, commit)
		}
	}

	return commits
}

func parseRawCommit(record string) (Commit, bool) {
	header, message, _ := strings.Cut(strings.TrimLeft(record, "\n"), "\n\n")

	var commit Commit

	for line := range strings.SplitSeq(header, "\n") {
		key, value, _ := strings.Cut(line, " ")

		switch {
		case key == "commit":
			commit.SHA = value
		case key == "author":
			// "Name <address> seconds zone": the name ends at the address.
			commit.Author, _, _ = strings.Cut(value, " <")
		case strings.HasPrefix(key, "gpgsig"):
			commit.Signature = signatureKind(value)
		}
	}

	if commit.SHA == "" {
		return Commit{}, false
	}

	// The raw format indents every message line by four spaces.
	lines := strings.Split(message, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "    ")
	}

	commit.Subject = strings.TrimSpace(lines[0])
	commit.Trailers = parseTrailers(lines[1:])

	return commit, true
}

// SignatureKinds are the kinds a commit signature is reported as, in the
// order a summary names them. A signature in a format this does not know is
// still a signature.
var SignatureKinds = []string{"SSH", "GPG", "X.509", "other"}

func signatureKind(armor string) string {
	switch {
	case strings.Contains(armor, "BEGIN SSH SIGNATURE"):
		return SignatureKinds[0]
	case strings.Contains(armor, "BEGIN PGP SIGNATURE"):
		return SignatureKinds[1]
	case strings.Contains(armor, "BEGIN SIGNED MESSAGE"):
		return SignatureKinds[2]
	default:
		return SignatureKinds[3]
	}
}

// parseTrailers reads the last paragraph of a message body as trailers, the
// way git does: every line is a trailer or continues the one before. A last
// paragraph of prose has none.
func parseTrailers(body []string) []Trailer {
	end := len(body)
	for end > 0 && strings.TrimSpace(body[end-1]) == "" {
		end--
	}

	start := end
	for start > 0 && strings.TrimSpace(body[start-1]) != "" {
		start--
	}

	var trailers []Trailer

	for _, line := range body[start:end] {
		if match := trailerLine.FindStringSubmatch(line); match != nil {
			trailers = append(trailers, Trailer{Key: match[1], Value: strings.TrimSpace(match[2])})

			continue
		}

		if len(trailers) == 0 || !strings.HasPrefix(line, " ") {
			return nil
		}

		trailers[len(trailers)-1].Value += " " + strings.TrimSpace(line)
	}

	return trailers
}
