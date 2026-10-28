// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package pipeline_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/config"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
)

func plannedContainers(t *testing.T, containers string) []pipeline.PlannedContainer {
	t.Helper()

	cfg, err := config.Parse([]byte("artifacts:\n  - name: app\n    project-type: go\ncontainers:\n" + containers))
	if err != nil {
		t.Fatal(err)
	}

	if err = config.Validate(cfg); err != nil {
		t.Fatal(err)
	}

	if err = config.Derive(cfg); err != nil {
		t.Fatal(err)
	}

	return pipeline.NewConfigPlan(cfg).Containers.All
}

func releaseImagesInput() pipeline.ReleaseImagesInput {
	return pipeline.ReleaseImagesInput{ImageRepository: "ghcr.io/acme/app", Repository: "Acme/App", ReleaseTag: "v1.2.3"}
}

// Two containers share one repository as tag variants: each gets its own
// staging candidate, its final tag, and one build per selected architecture.
func TestPlanReleaseImages_PublishesTagVariantsOfOneRepository(t *testing.T) {
	t.Parallel()

	containers := plannedContainers(t, `  - name: app
    container-file: Containerfile.alpine
    platforms: linux/amd64,linux/arm64
    build-args: {B: two, A: one}
  - name: app-distroless
    tag-suffix: -distroless
    platforms: linux/amd64,linux/arm64
    enable-scan: false
`)

	in := releaseImagesInput()
	in.ReleasePlatforms = "linux/arm64"

	plan, err := pipeline.PlanReleaseImages(containers, in)
	if err != nil {
		t.Fatal(err)
	}

	want := []pipeline.ReleaseImage{
		{Name: "app", ImageRepository: "ghcr.io/acme/app", CandidateTag: "staging-v1.2.3", FinalTagName: "v1.2.3", Archs: "arm64", Scan: true},
		{Name: "app-distroless", ImageRepository: "ghcr.io/acme/app", CandidateTag: "staging-v1.2.3-distroless", FinalTagName: "v1.2.3-distroless", Archs: "arm64"},
	}
	if len(plan.Images) != len(want) || plan.Images[0] != want[0] || plan.Images[1] != want[1] {
		t.Fatalf("images = %+v, want %+v", plan.Images, want)
	}

	if len(plan.Builds) != 2 {
		t.Fatalf("builds = %+v, want one arm64 build per container", plan.Builds)
	}

	first := plan.Builds[0]
	if first.Arch != "arm64" || first.Platform != "linux/arm64" || first.ContainerFile != "Containerfile.alpine" || first.Context != "." || first.BuildArgs != "A=one\nB=two" || first.CandidateTag != "staging-v1.2.3" {
		t.Fatalf("first build = %+v", first)
	}
}

// A release publishes to one full repository, written out: a registry or
// owner is never inferred.
func TestPlanReleaseImages_PublishesToTheOneFullRepository(t *testing.T) {
	t.Parallel()

	containers := plannedContainers(t, "  - name: app\n")

	for repository, want := range map[string]string{
		"registry.example:5000/Team/App": "registry.example:5000/team/app",
		"localhost/app":                  "localhost/app",
		"acme/app":                       "",
		"app":                            "",
	} {
		in := releaseImagesInput()
		in.ImageRepository = repository

		plan, err := pipeline.PlanReleaseImages(containers, in)
		if want == "" {
			if !errors.Is(err, errs.ErrUsage) {
				t.Errorf("%s: err = %v, want a refusal of a repository without its registry", repository, err)
			}

			continue
		}

		if err != nil || plan.Images[0].ImageRepository != want {
			t.Errorf("%s: plan = %+v, err = %v, want %s", repository, plan.Images, err, want)
		}
	}
}

func TestPlanReleaseImages_RefusesWhatItDoesNotCarryOut(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		containers string
		tag        string
		platforms  string
		want       string
	}{
		"same final tag":   {containers: "  - name: app\n  - name: other\n", want: "would both publish ghcr.io/acme/app:v1.2.3"},
		"arch tag":         {containers: "  - name: app\n  - name: other\n    tag-suffix: -amd64\n", want: "would both publish ghcr.io/acme/app:staging-v1.2.3-amd64"},
		"image-name":       {containers: "  - name: app\n    image-name: acme/other\n", want: "tag-suffix"},
		"build secrets":    {containers: "  - name: app\n    build-secrets: [PRIVATE_REGISTRY_CREDENTIAL]\n", want: "build-secrets"},
		"extraction":       {containers: "  - name: app\n    extract: {binary: {paths: [/app]}}\n", want: "extract"},
		"scan threshold":   {containers: "  - name: app\n    scan-severity: CRITICAL\n", want: "gate at CRITICAL,HIGH"},
		"foreign platform": {containers: "  - name: app\n    platforms: linux/s390x\n", want: "linux/amd64 and linux/arm64"},
		"undeclared":       {containers: "  - name: app\n", platforms: "linux/arm64", want: "not declared"},
		"build metadata":   {containers: "  - name: app\n", tag: "v1.2.3+build", want: "release tag"},
		"not a version":    {containers: "  - name: app\n", tag: "latest", want: "release tag"},
		"outside file":     {containers: "  - name: app\n    container-file: ../layer/Containerfile\n", want: "inside the repository"},
		"absolute context": {containers: "  - name: app\n    context: /etc\n", want: "inside the repository"},
	} {
		in := releaseImagesInput()
		in.ReleasePlatforms = tc.platforms

		if tc.tag != "" {
			in.ReleaseTag = tc.tag
		}

		_, err := pipeline.PlanReleaseImages(plannedContainers(t, tc.containers), in)
		refused := errors.Is(err, errs.ErrInvalidConfig) || errors.Is(err, errs.ErrUsage)

		if !refused || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q", name, err, tc.want)
		}
	}

	if _, err := pipeline.PlanReleaseImages(nil, releaseImagesInput()); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Errorf("no containers: err = %v, want ErrInvalidConfig", err)
	}
}
