// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
	"gopkg.in/yaml.v3"
)

// The engine scans every exported layout. Its provider must install the scanner
// whenever it exports one, offer no consumer scan toggle, and preserve reports
// after a blocking scan rather than silently losing the evidence.
func TestPublishedLayoutScanCannotLosePrerequisiteOrReports(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(reporoot.Path(t), ".github/workflows/publish-container.yml"))
	if err != nil {
		t.Fatal(err)
	}

	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				If   string            `yaml:"if"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}

	checked := false

	for _, job := range workflow.Jobs {
		install, build := -1, -1

		for index, step := range job.Steps {
			switch step.Name {
			case "Install trivy":
				install = index

				if strings.Contains(step.If, "enable-scan") || !strings.Contains(step.If, "push-image") {
					t.Fatalf("consumer can disable scan prerequisite: %s", step.If)
				}
			case "Write build plan":
				if _, ok := step.Env["ENABLE_SCAN"]; ok {
					t.Fatalf("the build plan carries a scan toggle the engine does not read: %v", step.Env)
				}
			case "Build and scan retained layout":
				build = index
			case "Save container scan SARIF artifact", "Save GitLab container-scanning report", "Upload container scan results to Code Scanning":
				if !strings.Contains(step.If, "always()") || strings.Contains(step.If, "enable-scan") {
					t.Fatalf("blocking findings lose reports at %s: %s", step.Name, step.If)
				}
			}
		}

		if build >= 0 {
			checked = true

			if install < 0 || install >= build {
				t.Fatal("layout scan runs before its scanner prerequisite")
			}
		}
	}

	if !checked {
		t.Fatal("no exported-layout build was checked")
	}
}

// Snapshot images publish too: their providers install the scanner for every
// image, whatever the plan's legacy scan field says, and the engine scans.
func TestSnapshotPublicationAlwaysInstallsTheScanner(t *testing.T) {
	t.Parallel()

	root := reporoot.Path(t)

	github, err := os.ReadFile(filepath.Join(root, ".github/workflows/snapshot-images.yml"))
	if err != nil {
		t.Fatal(err)
	}

	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				If   string            `yaml:"if"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(github, &workflow); err != nil {
		t.Fatal(err)
	}

	checked := false

	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Name != "Install configured evidence tools" {
				continue
			}

			checked = true

			if step.If != "" || step.Env["SCAN"] != "" || !strings.Contains(step.Run, "install_trivy") || strings.Contains(step.Run, "SCAN") {
				t.Fatalf("the snapshot scanner install is conditional: if=%q env=%v run=%s", step.If, step.Env, step.Run)
			}
		}
	}

	if !checked {
		t.Fatal("no GitHub snapshot evidence-tools step was checked")
	}

	forgejo, err := os.ReadFile(filepath.Join(root, ".forgejo/bin/snapshot-images.sh"))
	if err != nil {
		t.Fatal(err)
	}

	_, tools, found := strings.Cut(string(forgejo), "snapshot_evidence_tools() {")
	tools, _, _ = strings.Cut(tools, "\n}\n")

	if !found || strings.Contains(tools, "--field scan") || !strings.Contains(tools, "\tinstall_trivy\n") {
		t.Fatalf("the Forgejo/GitLab snapshot scanner install is conditional:\n%s", tools)
	}
}

func TestRuntimePublicationInstallsScannerBeforeItsMandatoryGate(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(reporoot.Path(t), ".github/workflows/self-runtime-container.yml"))
	if err != nil {
		t.Fatal(err)
	}

	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Run  string            `yaml:"run"`
				Uses string            `yaml:"uses"`
				If   string            `yaml:"if"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}

	checked := false

	for _, job := range workflow.Jobs {
		install, build := -1, -1

		for index, step := range job.Steps {
			if strings.Contains(step.Run, "source scripts/bootstrap/install-trivy.sh") && strings.Contains(step.Run, "install_trivy") {
				install = index
			}

			if step.Env["BUILD_MODE"] == "push-by-digest" && strings.Contains(step.Run, "forge-ci container build") {
				build = index
			}

			if strings.HasPrefix(step.Uses, "aquasecurity/trivy-action@") {
				t.Fatal("runtime publication has a second advisory scan outside the engine policy")
			}

			if step.Name == "Save runtime scan reports" || step.Name == "Upload trivy SARIF to Code Scanning" {
				if !strings.Contains(step.If, "always()") {
					t.Fatalf("runtime gate failures lose evidence at %s", step.Name)
				}
			}
		}

		if build >= 0 {
			checked = true

			if install < 0 || install >= build {
				t.Fatal("runtime publication scans before installing Trivy")
			}
		}
	}

	if !checked {
		t.Fatal("no runtime publication build was checked")
	}
}
