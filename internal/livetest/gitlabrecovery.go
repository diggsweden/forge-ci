// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"bytes"
	"fmt"
	"strings"

	gitlabapi "gitlab.com/gitlab-org/api/client-go"
	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/domain/imageledger"
)

const GitLabRecoveryCheckpoint = "Fixture: candidate checkpoint"
const GitLabRecoveryPauseVariable = "FIXTURE_PAUSE_AFTER_CANDIDATE"

// GitLabRecoveryObservation recognizes the current DAG's interruption boundary.
// A running consumer build is not a checkpoint, even if it wrote a registry tag.
func GitLabRecoveryObservation(jobs []*gitlabapi.Job) (publisher, checkpoint int64, err error) {
	buildSucceeded := false
	for _, job := range jobs {
		if job.Status == "failed" || job.Status == "canceled" {
			return 0, 0, fmt.Errorf("consumer failed before the interruption checkpoint: %s", job.Name)
		}
		if (strings.HasPrefix(job.Name, "Sign:") || strings.HasPrefix(job.Name, "Publish:")) && job.Status != "created" && job.Status != "skipped" {
			return 0, 0, fmt.Errorf("downstream job %s advanced before checkpoint cancellation: %s", job.Name, job.Status)
		}
		switch job.Name {
		case "Build: release files":
			buildSucceeded = job.Status == "success"
		case "Images: publish":
			if job.Status == "success" {
				publisher = job.ID
			}
		case GitLabRecoveryCheckpoint:
			if job.Status == "success" {
				return 0, 0, fmt.Errorf("checkpoint completed without controller cancellation")
			}
			if job.Status == "running" {
				checkpoint = job.ID
			}
		}
	}
	if publisher == 0 || checkpoint == 0 || !buildSucceeded {
		return 0, 0, nil
	}
	return publisher, checkpoint, nil
}

// GitLabRecoveryParent extends the captured engine parent, rather than retaining
// a second copy of its inputs, forwarding policy or child DAG. The wrapper is a
// component in the independently pinned test policy project. component.sha and
// component.reference must still identify the engine, not that wrapper project.
// extensionRef pins a separate, immutable commit containing only the test gate.
func GitLabRecoveryParent(parent []byte, engineSHA, policyProject, extensionRef string) ([]byte, error) {
	parent = bytes.ReplaceAll(parent, []byte("$[[ component.sha ]]"), []byte(engineSHA))
	parent = bytes.ReplaceAll(parent, []byte("$[[ component.reference ]]"), []byte(engineSHA))
	decoder := yaml.NewDecoder(bytes.NewReader(parent))
	var spec, jobs map[string]any
	if err := decoder.Decode(&spec); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&jobs); err != nil {
		return nil, err
	}
	release, ok := jobs["release"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("recovery: captured parent has no release job")
	}
	trigger, ok := release["trigger"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("recovery: captured parent has no child trigger")
	}
	includes, ok := trigger["include"].([]any)
	if !ok || len(includes) != 1 {
		return nil, fmt.Errorf("recovery: expected one production child include")
	}
	trigger["include"] = append(includes, map[string]any{
		"project": policyProject, "ref": extensionRef, "file": "/recovery-checkpoint.yml",
	})
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	if err := encoder.Encode(spec); err != nil {
		return nil, err
	}
	if err := encoder.Encode(jobs); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// GitLabRecoveryExtension is child-only test policy. It does not replace the
// signer script, role, environment or artifact source. No production template
// reads the fixture variable. A retry receives its new project-variable value;
// the original running shell deliberately cannot notice that change.
func GitLabRecoveryExtension(image string) ([]byte, error) {
	return yaml.Marshal(map[string]any{
		GitLabRecoveryCheckpoint: map[string]any{
			"stage": "sign", "image": map[string]any{"name": image, "entrypoint": []string{""}},
			"variables": map[string]string{"GIT_STRATEGY": "empty", "GIT_SUBMODULE_STRATEGY": "none"},
			"needs":     []any{map[string]any{"job": "Images: publish", "artifacts": false}},
			"script": []string{`set -eu
case "${FIXTURE_PAUSE_AFTER_CANDIDATE:?}" in
  true)
    echo fci-live-candidate-checkpoint-ready
    sleep 1200
    echo 'ERROR: controller did not cancel the candidate checkpoint' >&2
    exit 1
    ;;
  false) echo fci-live-candidate-checkpoint-cleared ;;
  *) exit 1 ;;
esac`},
		},
		"Sign: files and images": map[string]any{
			"needs": []any{
				map[string]any{"job": "Images: publish", "artifacts": true},
				map[string]any{"job": GitLabRecoveryCheckpoint, "artifacts": false},
			},
		},
	})
}

// GitLabRecoveryCandidate admits exactly the single-image recovery case. Both
// tags belong to the source repository; the signer later rewrites the final
// destination to the separate publication repository. Never infer identity from
// whichever mutable tag happens to exist in the registry.
func GitLabRecoveryCandidate(ledger []byte, repository, finalTag string) (imageledger.Entry, error) {
	entries, err := imageledger.Parse(ledger)
	if err != nil {
		return imageledger.Entry{}, err
	}
	if len(entries) != 1 {
		return imageledger.Entry{}, fmt.Errorf("recovery requires exactly one candidate, got %d", len(entries))
	}
	entry := entries[0]
	if err := entry.Validate(finalTag); err != nil {
		return imageledger.Entry{}, err
	}
	if entry.ImageKind != imageledger.ImageKindRelease || entry.CandidateTag != repository+":staging-"+finalTag ||
		entry.FinalTag != repository+":"+finalTag || entry.Ref != repository+"@"+entry.Digest {
		return imageledger.Entry{}, fmt.Errorf("recovery receipt does not bind the exact candidate: %+v", entry)
	}
	return entry, nil
}
