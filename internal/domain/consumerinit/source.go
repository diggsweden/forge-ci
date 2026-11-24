// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package consumerinit

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// ParseForgeURL reads the web address of a Forge CI fork, which the
// consumer's workflows name, from https://host/owner/repo or from the SSH
// form a checkout's origin has, git@host:owner/repo or
// ssh://git@host/owner/repo. An HTTPS address keeps its port; an SSH port,
// which is no web port, a path or any other scheme is refused: the address
// is written into workflows and must not be a guess.
func ParseForgeURL(raw string) (Source, error) {
	host, repository, ok := splitForgeURL(strings.TrimSpace(raw))
	repository = strings.TrimSuffix(strings.Trim(repository, "/"), ".git")

	if !ok || host == "" || strings.ContainsAny(host, "@/") || !strings.Contains(repository, "/") {
		return Source{}, fmt.Errorf("the Forge CI fork's address must be https://<host>/<owner>/<repository>, got %q; pass --url: %w", redacted(raw), errs.ErrUsage)
	}

	return Source{Host: host, Repository: repository}, nil
}

// redacted drops the user part of an address, where an origin may carry
// a token.
func redacted(raw string) string {
	if parsed, err := url.Parse(raw); err == nil && parsed.User != nil {
		parsed.User = nil

		return parsed.String()
	}

	return raw
}

func splitForgeURL(raw string) (string, string, bool) {
	if user, rest, scp := strings.Cut(raw, "@"); scp && !strings.Contains(user, "/") && !strings.Contains(raw, "://") {
		host, repository, ok := strings.Cut(rest, ":")

		return host, repository, ok && !strings.Contains(host, ":")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}

	switch parsed.Scheme {
	case "https":
		return parsed.Host, parsed.Path, true
	case "ssh":
		return parsed.Hostname(), parsed.Path, parsed.Port() == ""
	default:
		return "", "", false
	}
}
