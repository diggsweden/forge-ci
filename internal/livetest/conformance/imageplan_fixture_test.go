// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package conformance_test

// These fixture and evidence checks run without a provider or the live tag.
import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/imageledger"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
)

//go:embed testdata/layer/resolved-image-plan.yml
var resolvedImagePlanFixture string

const resolvedImagePlanContainerfile = "FROM scratch AS unused\nCOPY missing-unused-file /unused\nFROM scratch AS selected\nCOPY marker.sh /marker.sh\n"

func imagePlanMarker(name, version string) string {
	return "#!/bin/sh\nprintf 'resolved image policy " + name + " " + version + "\\n'\n"
}

func imagePlanSHA256(body []byte) string {
	digest := sha256.Sum256(body)

	return hex.EncodeToString(digest[:])
}

func imagePlanBuildJob(job, name string) bool {
	suffix, found := strings.CutPrefix(job, "Build "+name+" (amd64)")

	return found && (suffix == "" || strings.HasPrefix(suffix, " ("))
}

func TestResolvedImagePlanBuildJobNames(t *testing.T) {
	for name, want := range map[string]bool{
		"Build steady (amd64)": true,
		"Build steady (amd64) (map[arch:amd64 cache-tag:cache-arch-steady-amd64])": true,
		"Build changing (amd64) (map[arch:amd64])":                                 false,
		"Build steady (arm64)":              false,
		"Publish steady (map[archs:amd64])": false,
		"Build steady (amd64)-other":        false,
	} {
		if imagePlanBuildJob(name, "steady") != want {
			t.Errorf("job %q: want match=%t", name, want)
		}
	}
}

// Both paths must have positive execution evidence. Digest equality alone
// cannot distinguish reuse from a reproducible Kaniko build.
func imagePlanCacheEvidence(log, ref string, hit bool) (string, error) {
	const (
		reuse   = "Architecture cache reused by digest: "
		execute = "kaniko replaces this container's root filesystem with the image's; the layout, the context and /kaniko are kept."
	)

	reused := strings.Count(log, reuse)
	built := strings.Count(log, execute)

	if hit && reused == 1 && built == 0 && strings.Contains(log, reuse+ref+"\n") {
		return reuse + ref, nil
	}

	if !hit && reused == 0 && built == 1 && strings.Contains(log, " builds when its layout is exported\n") {
		return execute, nil
	}

	return "", fmt.Errorf("cache execution evidence disagrees: want hit=%t, reuse markers=%d, executor markers=%d: %w", hit, reused, built, errs.ErrValidation)
}

func imagePlanReview(tree map[string][]byte, repository string) (map[string]imageledger.Entry, error) {
	var entries []imageledger.Entry
	if err := json.Unmarshal(tree["release-images.json"], &entries); err != nil {
		return nil, err
	}

	if len(entries) != 2 {
		return nil, fmt.Errorf("expected exactly two base entries, got %d: %w", len(entries), errs.ErrValidation)
	}

	result := make(map[string]imageledger.Entry, 2)

	for _, entry := range entries {
		name := entry.Flavor
		if (name != "steady" && name != "changing") || result[name].Ref != "" {
			return nil, fmt.Errorf("unexpected or duplicate base flavor %q: %w", name, errs.ErrValidation)
		}

		if err := entry.ValidateBeforeCapture("v0.0.1"); err != nil {
			return nil, err
		}

		if entry.ImageKind != imageledger.ImageKindBase || entry.Role != "base" || entry.Ref != repository+"@"+entry.Digest || entry.Provenance["review_fixture"] != "resolved-image-plan" {
			return nil, fmt.Errorf("unexpected base lineage for %s: %w", name, errs.ErrValidation)
		}

		var scan struct {
			SchemaVersion int    `json:"SchemaVersion"`
			ArtifactName  string `json:"ArtifactName"`
		}
		if err := json.Unmarshal(tree["trivy-results-"+name+"-amd64.json"], &scan); err != nil || scan.SchemaVersion != 2 || scan.ArtifactName == "" {
			return nil, fmt.Errorf("missing or invalid amd64 scan for %s: %w", name, errs.ErrValidation)
		}

		inventory := tree["inventory-"+name+"-amd64.cyclonedx.json"]

		var bom struct {
			Format string `json:"bomFormat"`
		}
		if err := json.Unmarshal(inventory, &bom); err != nil || bom.Format != "CycloneDX" {
			return nil, fmt.Errorf("missing or invalid amd64 inventory for %s: %w", name, errs.ErrValidation)
		}

		delivered := tree["base-sbom-"+name+".cyclonedx.json"]
		if len(delivered) == 0 || string(delivered) != string(inventory) || entry.SBOM != "premade-sboms/base-sbom-"+name+".cyclonedx.json" || entry.SBOMSHA256 != imagePlanSHA256(delivered) {
			return nil, fmt.Errorf("ledger does not pin the delivered amd64 premade inventory for %s: %w", name, errs.ErrValidation)
		}

		result[name] = entry
	}

	return result, nil
}

func TestResolvedImagePlanCacheEvidence(t *testing.T) {
	ref := "registry.example/base@sha256:" + strings.Repeat("a", 64)
	hit := "Architecture cache reused by digest: " + ref + "\n"
	build := "kaniko: local builds when its layout is exported\nkaniko replaces this container's root filesystem with the image's; the layout, the context and /kaniko are kept.\n"

	for _, tc := range []struct {
		name, log  string
		hit, valid bool
	}{
		{"actual reuse", hit, true, true}, {"actual build", build, false, true},
		{"digest alone", ref, true, false}, {"empty miss", "", false, false},
		{"fresh identical build", build, true, false}, {"unexpected reuse", hit, false, false},
		{"contradictory", hit + build, true, false}, {"wrong digest", strings.ReplaceAll(hit, "aaaa", "bbbb"), true, false},
		{"duplicate reuse", hit + hit, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := imagePlanCacheEvidence(tc.log, ref, tc.hit); (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}

func TestResolvedImagePlanReviewRequiresDeliveredEvidence(t *testing.T) {
	for _, broken := range []string{"", "scan", "inventory", "pin", "delivery", "duplicate", "lineage"} {
		t.Run(broken, func(t *testing.T) {
			tree := make(map[string][]byte)
			entries := make([]imageledger.Entry, 0, 2)

			for _, name := range []string{"steady", "changing"} {
				id := strings.Repeat("a", 64)
				ref := "registry.example/base"
				bom := []byte(`{"bomFormat":"CycloneDX"}`)
				entries = append(entries, imageledger.Entry{
					Role: "base", ImageKind: imageledger.ImageKindBase, Flavor: name,
					Digest: "sha256:" + id, Ref: ref + "@sha256:" + id, BaseInputID: id,
					FinalTag: ref + ":" + id + "-" + name, CandidateTag: ref + ":staging-" + id + "-" + name,
					Provenance: map[string]any{"review_fixture": "resolved-image-plan"},
					SBOM:       "premade-sboms/base-sbom-" + name + ".cyclonedx.json", SBOMSHA256: imagePlanSHA256(bom),
				})
				tree["base-sbom-"+name+".cyclonedx.json"] = bom
				tree["inventory-"+name+"-amd64.cyclonedx.json"] = bom
				tree["trivy-results-"+name+"-amd64.json"] = []byte(`{"SchemaVersion":2,"ArtifactName":"layout"}`)
			}

			switch broken {
			case "scan":
				delete(tree, "trivy-results-changing-amd64.json")
			case "inventory":
				tree["inventory-changing-amd64.cyclonedx.json"] = []byte(`{}`)
			case "pin":
				entries[1].SBOMSHA256 = strings.Repeat("b", 64)
			case "delivery":
				delete(tree, "base-sbom-changing.cyclonedx.json")
			case "duplicate":
				entries[1] = entries[0]
			case "lineage":
				entries[1].Provenance = nil
			}

			body, err := json.Marshal(entries)
			if err != nil {
				t.Fatal(err)
			}

			tree["release-images.json"] = body
			if _, err := imagePlanReview(tree, "registry.example/base"); (err == nil) != (broken == "") {
				t.Fatalf("review for %q: %v", broken, err)
			}
		})
	}
}

func TestResolvedImagePlanPolicyPhases(t *testing.T) {
	var fixture struct {
		Jobs map[string]struct {
			Steps []probeStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(resolvedImagePlanFixture), &fixture); err != nil {
		t.Fatal(err)
	}

	var script string

	for _, step := range fixture.Jobs["policy"].Steps {
		if step.ID == "policy" {
			script = step.Run
		}
	}

	if script == "" {
		t.Fatal("missing fixture policy")
	}

	dir := t.TempDir()
	for _, name := range []string{"steady", "changing"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, name, "Containerfile"), []byte(resolvedImagePlanContainerfile), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var previous pipeline.ReleaseImagesPlan

	for _, phase := range []string{"cold", "partial", "warm"} {
		for _, name := range []string{"steady", "changing"} {
			version := "v1"
			if phase != "cold" && name == "changing" {
				version = "v2"
			}

			if err := os.WriteFile(filepath.Join(dir, name, "marker.sh"), []byte(imagePlanMarker(name, version)), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		if err := os.WriteFile(filepath.Join(dir, "canary-phase"), []byte(phase+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		output := filepath.Join(dir, "output-"+phase)
		// Inert collaborator supplies only the opaque native builder identity;
		// execute the actual fixture policy and admit it with the real engine.
		cmd := exec.CommandContext(t.Context(), "/bin/bash", "--noprofile", "--norc", "-c", "forge-ci() { test \"$*\" = 'container builder-identity' || return 1; printf '%064d\\n' 1; }; "+script)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "LC_ALL=C", "BASE_IMAGE_REPOSITORY=registry.example/base", "FORGEJO_OUTPUT=" + output}

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s policy: %v: %s", phase, err, out)
		}

		body, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}

		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		if len(lines) != 3 || lines[1] != "artifact=resolved-base-review-"+phase {
			t.Fatalf("unexpected phase output: %s", body)
		}

		plan, err := pipeline.ReadImageBuildPlan(strings.TrimPrefix(lines[0], "image-plan="), "registry.example/base", "v0.0.1")
		if err != nil || len(plan.Builds) != 2 || len(plan.Images) != 2 {
			t.Fatalf("%s plan: %+v: %v", phase, plan, err)
		}

		for idx, image := range plan.Images {
			build := plan.Builds[idx]
			if !image.Scan || image.Revision != image.BaseInputID || build.Revision != image.Revision || build.CacheTag != image.CacheTagPrefix+"-amd64" || build.RequiredLabels != "org.example.base-input-id="+image.BaseInputID || build.BuilderMaterialID == "" {
				t.Fatalf("unbound cache plan: %+v %+v", image, build)
			}

			if phase != "cold" {
				changed := previous.Images[idx].Revision != image.Revision
				if changed != (phase == "partial" && image.Name == "changing") || image.CacheTagPrefix != previous.Images[idx].CacheTagPrefix {
					t.Fatalf("%s changed the wrong content/slot: %+v", phase, image)
				}
			}
		}

		previous = plan

		final, err := pipeline.ReadImageBuildPlan(strings.TrimPrefix(lines[2], "final-plan="), "registry.example/base", "v0.0.1")
		if err != nil || len(final.Builds) != 1 || len(final.Images) != 1 {
			t.Fatalf("%s final plan: %+v: %v", phase, final, err)
		}

		image, build := final.Images[0], final.Builds[0]
		if path := imageledger.DefaultSBOMPath(imageledger.ImageKind(image.ImageKind), image.Flavor, image.Role); path != "dist/image-sbom-distroless.cyclonedx.json" {
			t.Fatalf("final fixture disagrees with production default SBOM path: %s", path)
		}

		if image.Role != "distroless" || image.ImageKind != "release" || !image.Scan || image.Archs != "amd64" || build.Platform != "linux/amd64" || build.Context != "steady" || build.Target != "selected" || build.CacheTag != "" || image.CacheTagPrefix != "" || image.CandidateTag != "staging-v0.0.1" || image.FinalTagName != "v0.0.1" {
			t.Fatalf("unexpected final release scope: %+v %+v", image, build)
		}
	}
}
