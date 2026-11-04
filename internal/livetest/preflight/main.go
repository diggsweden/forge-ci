// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// Command preflight validates the live tier's inputs without touching a
// provider: the contract for this suite's lab account and its CA. The run
// script calls it before building anything, so a missing or stale contract is
// reported in seconds with where the suite looked, and an accepted one is
// explained: endpoints, capabilities, credential lifetime and the run identity
// the suite's prefix derives.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/diggsweden/forge-ci/internal/livetest"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "classify-run" {
		status, err := strconv.Atoi(os.Args[2])
		if err != nil || status < 0 || status > 255 {
			usage()
		}

		result, err := classifyRun(status, os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "live result: %v\n", err)
			os.Exit(2)
		}

		fmt.Printf("%d %s\n", result.ExitCode, result.Verdict)

		return
	}

	if len(os.Args) >= 3 && strings.HasPrefix(os.Args[1], "campaign-") {
		if err := campaign(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "campaign: %v\n", err)
			os.Exit(2)
		}

		return
	}
	// `preflight runner-arch` prints only the architecture the lab's runners
	// execute, which the run script builds the runner binaries for.
	// `preflight consumer-trial-layer` prints only the URL a Forgejo consumer
	// trial serves the layer under test from, which the lab's kaniko job image
	// names as its source. `preflight host-tools` refuses when a pinned host
	// tool the scenarios run does not resolve to a real executable.
	if len(os.Args) == 2 {
		fact, err := preflightFact(os.Args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "live preflight: %v\n", err)
			os.Exit(2)
		}

		fmt.Println(fact)

		return
	}

	if len(os.Args) != 1 {
		usage()
	}

	summary, err := livetest.CheckLiveInputs(time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "live preflight: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("live preflight: %d provider(s), generation %s, namespace %s\n\n%s",
		summary.Selected, summary.Generation, livetest.ResourcePrefix, summary.Explanation)
}

// classifyRun classifies the run and writes its verdict.json beside the
// retained log. A clean exit must have a readable log to be judged; any other
// status is a failure the log can at most explain, so a log that cannot be
// opened or read then leaves the status to classify it alone.
func classifyRun(status int, path string) (livetest.LiveRunResult, error) {
	var log io.Reader

	file, err := os.Open(path) //nolint:gosec // Explicit operator-owned retained log.
	if err == nil {
		defer func() { _ = file.Close() }()

		log = file
	} else if status == 0 {
		return livetest.LiveRunResult{}, err
	}

	// A log that breaks off fails a clean exit; a failed one keeps its status.
	verdict, err := livetest.ReadLiveRun(log, status)
	if err != nil && status == 0 {
		return livetest.LiveRunResult{}, err
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "live result: %v\n", err)
	}

	verdict.Run = livetest.LiveRunHeaderFrom(os.Getenv)
	relativePackages(&verdict)

	// The verdict file serves tooling; the Live result line stays the
	// human's, so a verdict that cannot be written does not change it.
	if writeErr := livetest.WriteLiveRunVerdict(filepath.Dir(path), verdict); writeErr != nil {
		fmt.Fprintf(os.Stderr, "live result: %v\n", writeErr)
	}

	return verdict.Result(), nil
}

// relativePackages names each package by its directory relative to the
// repository, where the run script runs the classifier, not by the absolute
// path the log has. A path it cannot relate is kept as it is.
func relativePackages(verdict *livetest.LiveRunVerdict) {
	root, err := os.Getwd()
	if err != nil {
		return
	}

	relative := func(dir string) string {
		if rel, relErr := filepath.Rel(root, dir); relErr == nil && filepath.IsLocal(rel) {
			return "./" + filepath.ToSlash(rel)
		}

		return dir
	}

	for i := range verdict.Tests {
		verdict.Tests[i].Package = relative(verdict.Tests[i].Package)
	}

	for i := range verdict.Infrastructure {
		verdict.Infrastructure[i].Package = relative(verdict.Infrastructure[i].Package)
	}
}

// campaign runs a campaign mode. `campaign-env-check <file>` validates a
// campaign.env before campaign-prepare puts it in place; `campaign-plan <dir>
// <forgejo|gitlab> <cases-file>` prints the lane the script runs, for the
// commit checked out in the working directory; `campaign-runtime-targets
// <repository> <forgejo|gitlab>=<cases-file>... [flavor=<job-image flavor>...]`
// prints the runtime targets campaign-prepare builds for those lanes, one a
// line, and refuses a case naming a nanolinter image of another flavor.
func campaign(mode string, args []string) error {
	switch {
	case mode == "campaign-env-check" && len(args) == 1:
		_, err := livetest.ReadCampaignEnv(args[0])

		return err
	case mode == "campaign-runtime-targets" && len(args) >= 2:
		var lanes, flavors []string

		for _, arg := range args[1:] {
			if flavor, isFlavor := strings.CutPrefix(arg, "flavor="); isFlavor {
				flavors = append(flavors, flavor)
			} else {
				lanes = append(lanes, arg)
			}
		}

		targets, err := livetest.CampaignRuntimeTargets(args[0], lanes, flavors)
		for _, target := range targets {
			fmt.Println(target)
		}

		return err
	case mode == "campaign-plan" && len(args) == 3:
		head, err := exec.CommandContext(context.Background(), "git", "rev-parse", "--verify", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("read HEAD: %w", err)
		}

		settings, err := livetest.CampaignRunSettings(args[1], os.Getenv)
		if err != nil {
			return err
		}

		plan, err := livetest.PlanCampaignLane(args[0], args[1], args[2], strings.TrimSpace(string(head)), settings)
		if err != nil {
			return err
		}

		return livetest.WriteCampaignPlan(os.Stdout, plan)
	default:
		usage()

		return nil
	}
}

// preflightFact returns the single fact one preflight mode prints.
func preflightFact(mode string) (string, error) {
	switch mode {
	case "signing-inputs":
		err := livetest.CheckReusedSigningInputs(os.Getenv("FORGE_CI_LIVE_SIGNING_KEY_DIR"))

		return "reused signing input metadata accepted (key validity remains signer-verified)", err
	case "runner-arch":
		return livetest.LabRunnerArch()
	case "consumer-trial-layer":
		return livetest.ConsumerTrialLayerURL()
	case "host-tools":
		return "host tools resolved: " + strings.Join(livetest.LauncherHostTools, " "), livetest.CheckHostTools(context.Background(), livetest.LauncherHostTools)
	default:
		usage()

		return "", nil
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "       preflight classify-run <exit-code> <retained-log>")
	fmt.Fprintln(os.Stderr, "       preflight campaign-env-check <campaign.env>")
	fmt.Fprintln(os.Stderr, "       preflight campaign-plan <dir> <forgejo|gitlab> <cases-file>")
	fmt.Fprintln(os.Stderr, "       preflight campaign-runtime-targets <repository> <forgejo|gitlab>=<cases-file>... [flavor=<flavor>...]")
	fmt.Fprintln(os.Stderr, "usage: preflight [signing-inputs | runner-arch | consumer-trial-layer | host-tools] (signing-inputs checks only FORGE_CI_LIVE_SIGNING_KEY_DIR; host-tools checks only this host; other modes read the forgeci lab contract)")
	os.Exit(2)
}
