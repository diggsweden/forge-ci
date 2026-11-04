// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

func TestCampaignScenariosUseRecipesWithExplicitBoundedInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, extra, recipe string }{
		{"TestGitLabRelease_ConsumerBranchProtection", "checkout=@NATIVE_CONSUMER", "test-live-gitlab-consumer-branch-protection"},
		{"TestGitLabRelease_BuildImageCapability", "build-archive=@BUILD_PROBE_ARCHIVE", "test-live-gitlab-build-image-capability"},
		{"TestGitLabRelease_ConsumerTrial", "checkout=@NATIVE_CONSUMER job-archive=/job build-archive=/build signer-archive=/signer", "test-live-gitlab-consumer-release"},
		{"TestGitLabRelease_RejectsPreseededProvenance", "checkout=@NATIVE_CONSUMER job-archive=/job build-archive=/build signer-archive=/signer", "test-live-gitlab-consumer-release"},
		{"TestGitLabRelease_ConsumerInterruptedRecovery", "checkout=@NATIVE_CONSUMER job-archive=/job build-archive=/build signer-archive=/signer version=0.3.0", "test-live-gitlab-consumer-release"},
		{"TestGitLabWorkflow_InterruptedJob", "", "test-live-gitlab-interrupted-job"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			consumer := t.TempDir()
			dir, cases := newCampaign(t, "CAMPAIGN_COMMIT="+campaignHead+"\nNATIVE_CONSUMER="+consumer+"\nBUILD_PROBE_ARCHIVE=/probe.tar\n", "scenario "+tc.name+" "+tc.extra+"\n")

			plan, err := PlanCampaignLane(dir, "gitlab", cases, campaignHead, CampaignRun{})
			if err != nil || len(plan.Cases) != 1 || plan.Cases[0].Args[0] != tc.recipe {
				t.Fatalf("scenario recipe plan=%+v err=%v", plan, err)
			}

			if tc.recipe == "test-live-gitlab-consumer-release" && !strings.Contains(strings.Join(plan.Cases[0].Args, "\n"), "scenario="+tc.name) {
				t.Fatal("consumer case selected a different test")
			}
		})
	}
}

func TestCampaignScenarioInputsRefuseBeforeAnyCaseCanExecute(t *testing.T) {
	t.Parallel()

	consumer := t.TempDir()
	for name, line := range map[string]string{
		"missing checkout":         "scenario TestGitLabRelease_ConsumerTrial job-archive=/job build-archive=/build signer-archive=/signer",
		"duplicate checkout":       "scenario TestGitLabRelease_ConsumerBranchProtection checkout=@NATIVE_CONSUMER checkout=@NATIVE_CONSUMER",
		"relative checkout":        "scenario TestGitLabRelease_ConsumerBranchProtection checkout=relative",
		"extra protection input":   "scenario TestGitLabRelease_ConsumerBranchProtection checkout=@NATIVE_CONSUMER version=0.3.0",
		"missing probe archive":    "scenario TestGitLabRelease_BuildImageCapability job-archive=/job",
		"duplicate case selection": "scenario TestGitLabRelease_ConsumerTrial checkout=@NATIVE_CONSUMER job-archive=/job build-archive=/build signer-archive=/signer scenario=TestGitLabRelease_ConsumerTrial",
		"missing recovery version": "scenario TestGitLabRelease_ConsumerInterruptedRecovery checkout=@NATIVE_CONSUMER job-archive=/job build-archive=/build signer-archive=/signer",
		"preseeded next version":   "scenario TestGitLabRelease_RejectsPreseededProvenance checkout=@NATIVE_CONSUMER job-archive=/job build-archive=/build signer-archive=/signer version=0.3.0 next-version=0.3.1 version-bump=npm",
		"interruption extra input": "scenario TestGitLabWorkflow_InterruptedJob force=true",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir, cases := newCampaign(t, "CAMPAIGN_COMMIT="+campaignHead+"\nNATIVE_CONSUMER="+consumer+"\n", "scenario TestFirst\n"+line+"\n")

			plan, err := PlanCampaignLane(dir, "gitlab", cases, campaignHead, CampaignRun{})
			if !errors.Is(err, errs.ErrValidation) || len(plan.Cases) != 0 {
				t.Fatalf("invalid scenario left executable cases: plan=%+v err=%v", plan, err)
			}
		})
	}

	dir, cases := newCampaign(t, "CAMPAIGN_COMMIT="+campaignHead+"\n", "scenario TestGitLabWorkflow_InterruptedJob\n")
	if _, err := PlanCampaignLane(dir, "forgejo", cases, campaignHead, CampaignRun{}); !errors.Is(err, errs.ErrValidation) {
		t.Fatal("GitLab-only interruption accepted on another provider")
	}

	if _, err := campaignCaseArgs(filepath.Dir(dir), "gitlab", nil, []string{"scenario"}); !errors.Is(err, errs.ErrValidation) {
		t.Fatal("incomplete scenario accepted")
	}
}

func TestCampaignBuildahCapabilityCannotRunUnderKanikoSelection(t *testing.T) {
	t.Parallel()

	for _, declaration := range []string{"", "setting image-builder=kaniko\n"} {
		dir, cases := newCampaign(t, "CAMPAIGN_COMMIT="+campaignHead+"\n", declaration+"scenario TestFirst\nscenario TestGitLabRelease_BuildImageCapability build-archive=/probe.tar\n")

		run, err := CampaignRunSettings("gitlab", func(key string) string {
			if key == GitLabImageBuilderEnv {
				return ImageBuilderKaniko
			}

			return ""
		})
		if err != nil {
			t.Fatal(err)
		}

		if plan, err := PlanCampaignLane(dir, "gitlab", cases, campaignHead, run); !errors.Is(err, errs.ErrValidation) || len(plan.Cases) != 0 {
			t.Fatalf("Buildah probe reached a Kaniko lane: plan=%+v err=%v", plan, err)
		}
	}
}
