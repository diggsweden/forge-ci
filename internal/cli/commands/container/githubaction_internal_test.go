// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
)

func TestKanikoActionBuildPlanRemovesHostPathsAndRejectsUnsupportedInputs(t *testing.T) {
	t.Parallel()

	for _, change := range []string{"valid", "secret", "unknown field", "structured value", "load mode", "context escape", "absolute file", "symlink escape", "scan not deferred", "scan deferral missing", "unparsable defer-scan"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			workspace := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(workspace, "source"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "source", "Containerfile"), []byte("FROM scratch\n"), 0o600))

			build := map[string]any{"mode": "oci", "context": ".", "file": "Containerfile", "secrets": "", "auth-file": "/host/private/config.json", "output-dir": "/host/private/layout", "labels": "label=spaces and commas, preserved", "defer-scan": "true"}

			switch change {
			case "secret":
				build["secrets"] = "id=private,src=/host/private/secret"
			case "unknown field":
				build["registry-password"] = "must-not-be-baked"
			case "structured value":
				build["build-args"] = []string{"invalid"}
			case "load mode":
				build["mode"] = "load"
			case "context escape":
				build["context"] = ".."
			case "absolute file":
				build["file"] = "/host/Containerfile"
			case "symlink escape":
				require.NoError(t, os.Symlink(workspace, filepath.Join(workspace, "source", "escape")))

				build["context"] = "escape"
			case "scan not deferred":
				build["defer-scan"] = "false"
			case "scan deferral missing":
				delete(build, "defer-scan")
			case "unparsable defer-scan":
				build["defer-scan"] = "later"
			}

			body, err := json.Marshal(map[string]any{"container build": build})
			require.NoError(t, err)
			planPath := filepath.Join(t.TempDir(), "plan.json")
			require.NoError(t, os.WriteFile(planPath, body, 0o600))

			got, err := kanikoActionBuildPlan(workspace, planPath)
			if change != "valid" {
				require.ErrorIs(t, err, errs.ErrValidation)
				require.Nil(t, got)

				return
			}

			require.NoError(t, err)
			require.NotContains(t, string(got), "/host/")
			require.Contains(t, string(got), "spaces and commas, preserved")
			require.Equal(t, "/host/private/config.json", build["auth-file"], "preparation must not mutate the caller's plan")
		})
	}
}

// The action carries no scanner, so it prepares on a hosted runner without
// trivy; a plan that would scan inside it is refused before anything is written.
func TestPrepareKanikoActionNeedsNoTrivyAndAdmitsOnlyDeferringPlans(t *testing.T) {
	for _, tc := range []struct {
		name, deferScan string
		wantErr         error
	}{
		{name: "deferred without trivy", deferScan: "true"},
		{name: "non-deferring plan refused", deferScan: "false", wantErr: errs.ErrValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			t.Setenv("FORGE_CI_PROVIDER", "local")
			t.Setenv("FORGE_CI_RUNNER", "github")
			t.Setenv("GITHUB_OUTPUT", filepath.Join(t.TempDir(), "output"))

			workspace := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(workspace, "source"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "source", "Containerfile"), []byte("FROM scratch\n"), 0o600))

			body, err := json.Marshal(map[string]any{"container build": map[string]any{"mode": "oci", "context": ".", "file": "Containerfile", "defer-scan": tc.deferScan}})
			require.NoError(t, err)
			planPath := filepath.Join(t.TempDir(), "plan.json")
			require.NoError(t, os.WriteFile(planPath, body, 0o600))

			action := filepath.Join(workspace, ".forge-ci-kaniko-action")

			err = prepareKanikoActionCmd().Run(t.Context(), []string{
				"prepare-kaniko-action", "--workspace", workspace, "--plan", planPath,
				"--job-image", "registry.invalid/engine/kaniko@sha256:" + strings.Repeat("a", 64),
			})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.NoDirExists(t, action)

				return
			}

			require.NoError(t, err)
			require.NoFileExists(t, filepath.Join(action, "trivy"))

			dockerfile, err := os.ReadFile(filepath.Join(action, "Dockerfile"))
			require.NoError(t, err)
			require.NotContains(t, string(dockerfile), "trivy")
		})
	}
}

func TestPrepareKanikoBaseActionAdmitsRootAndChildPlans(t *testing.T) {
	for _, mode := range []string{"root", "child", "wrong parent", "wrong flavor", "mixed modes"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FORGE_CI_PROVIDER", "local")
			t.Setenv("FORGE_CI_RUNNER", "github")
			t.Setenv("GITHUB_OUTPUT", filepath.Join(t.TempDir(), "output"))
			workspace := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(workspace, "source"), 0o700))

			id := strings.Repeat("a", 64)
			repository := "registry.example/org/base"
			plan := pipeline.ReleaseImagesPlan{
				Images: []pipeline.ReleaseImage{{Name: "base", Role: "base", ImageKind: "base", Flavor: "go", BaseInputID: id, Scan: true,
					ImageRepository: repository, Archs: "amd64", FinalTagName: id + "-go", CandidateTag: "staging-" + id + "-go"}},
				Builds: []pipeline.ReleaseImageBuild{{Name: "base", Arch: "amd64", Platform: "linux/amd64", ContainerFile: "Containerfile", Context: ".",
					ImageRepository: repository, FinalTagName: id + "-go", CandidateTag: "staging-" + id + "-go"}},
			}

			args := []string{"prepare-kaniko-action", "--base", "--workspace", workspace,
				"--plan", filepath.Join(workspace, "plan.json"), "--image-repository", repository,
				"--job-image", "registry.invalid/kaniko@sha256:" + strings.Repeat("d", 64)}
			if mode == "child" || mode == "wrong parent" {
				ref := "registry.example/parent@sha256:" + strings.Repeat("b", 64)
				parent := `{"ref":"` + ref + `","input_id":"` + strings.Repeat("c", 64) + `"}`
				plan.Images[0].ProvenanceJSON = `{"parent_base":` + parent + `}`
				plan.Builds[0].BuildArgs = "PARENT=" + ref
				plan.Builds[0].RegistryInputs = map[string]string{"PARENT": ref}

				if mode == "wrong parent" {
					parent = strings.ReplaceAll(parent, "/parent@", "/other@")
				}

				args = append(args, "--parent-base-json", parent)
			}

			if mode == "wrong flavor" {
				args = append(args, "--stage-flavors", "java")
			}

			if mode == "mixed modes" {
				args = append(args, "--snapshot")
			}

			body, err := json.Marshal(plan)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "plan.json"), body, 0o600))
			err = prepareKanikoActionCmd().Run(t.Context(), args)
			action := filepath.Join(workspace, ".forge-ci-kaniko-action")

			if mode != "root" && mode != "child" {
				require.Error(t, err)
				require.NoDirExists(t, action)

				return
			}

			require.NoError(t, err)
			frozen, err := os.ReadFile(filepath.Join(action, "plan.json"))
			require.NoError(t, err)
			require.JSONEq(t, string(body), string(frozen))
		})
	}
}
