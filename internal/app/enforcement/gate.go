// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Package enforcement applies the administrator policy to classified findings.
// It does not suppress execution, transport, or verification errors.
package enforcement

import (
	"context"
	"fmt"
	"io"

	"github.com/diggsweden/forge-ci/internal/domain/enforcement"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// Findings always reports the real result and policy identity before deciding
// whether findings block. Report-writing failures remain blocking.
func Findings(ctx context.Context, out io.Writer, check enforcement.Check, count int) error {
	policy, err := Resolve(ctx)
	if err != nil {
		return err
	}

	return FindingsFor(out, policy, check, count)
}

// FindingsFor is Findings with an explicit policy value; production callers
// pass Resolve's, tests pass a fixed policy.
func FindingsFor(out io.Writer, policy enforcement.Policy, check enforcement.Check, count int) error {
	decision, err := policy.Decide(check, count)
	if err != nil {
		return err
	}

	return report(out, decision)
}

func report(out io.Writer, decision enforcement.Decision) error {
	status := "clean"
	if decision.Findings > 0 {
		status = "findings"
	}

	if _, err := fmt.Fprintf(out, "Enforcement policy default-sha256:%s: %s result=%s count=%d enforcement=%t source=%s\n", decision.PolicySHA256, decision.Check, status, decision.Findings, decision.Enforced, decision.Source); err != nil {
		return fmt.Errorf("report admin policy decision: %w", err)
	}

	if decision.Enforced && decision.Findings > 0 {
		return fmt.Errorf("%s findings block progression: %w", decision.Check, errs.ErrValidation)
	}

	return nil
}
