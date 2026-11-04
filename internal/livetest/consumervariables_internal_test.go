// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"errors"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestMergeConsumerVariablesPreservesAuthority(t *testing.T) {
	plan := map[string]string{"FORGE_CI_REPOSITORY": "fixtures/engine", "CI_RUNNER_LABEL": "runner"}

	got, err := MergeConsumerVariables(plan, map[string]string{"FORGE_CI_REPOSITORY": "fixtures/engine", "SERVICE_IMAGE_REPOSITORY": "registry.invalid/fixtures/service"})
	if err != nil || len(got) != 3 || got["FORGE_CI_REPOSITORY"] != "fixtures/engine" {
		t.Fatal("same binding was not deduplicated")
	}

	got["CI_RUNNER_LABEL"] = "changed"

	if plan["CI_RUNNER_LABEL"] != "runner" {
		t.Fatal("mutated admitted plan")
	}

	if _, err := MergeConsumerVariables(plan, map[string]string{"FORGE_CI_REPOSITORY": "other/engine"}); err == nil {
		t.Fatal("conflicting repository authority accepted")
	}
}

func TestMergeConsumerVariablesRefusesConflictingBuilderWithoutPartialBindings(t *testing.T) {
	t.Parallel()

	plan := map[string]string{"CI_IMAGE_BUILDER": "buildah"}
	runtime := map[string]string{"A_RUNTIME_BINDING": "private-value", "CI_IMAGE_BUILDER": "kaniko"}
	got, err := MergeConsumerVariables(plan, runtime)

	if got != nil || !errors.Is(err, errs.ErrValidation) || !strings.Contains(err.Error(), "CI_IMAGE_BUILDER") {
		t.Fatalf("conflicting builder did not refuse the complete binding: %v, %v", got, err)
	}

	if strings.Contains(err.Error(), "buildah") || strings.Contains(err.Error(), "kaniko") || strings.Contains(err.Error(), "private-value") {
		t.Fatal("conflict error exposed a variable value")
	}

	if len(plan) != 1 || len(runtime) != 2 || runtime["CI_IMAGE_BUILDER"] != "kaniko" {
		t.Fatal("failed merge mutated its inputs")
	}
}
