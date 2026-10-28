// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package pipeline

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	domaincontainer "github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/version"
)

// releaseImagesScanSeverity is the one gate threshold the release-images
// workflow applies: the publication gate of every forge-ci image build.
const releaseImagesScanSeverity = "CRITICAL,HIGH"

// ReleaseImagesInput is what the release-images plan needs beyond the
// declaration: where images go and which release and platforms this run builds.
//
// A release publishes to one image repository, the one the signer is bound
// to; its containers are tag variants there, told apart by tag-suffix.
type ReleaseImagesInput struct {
	ImageRepository  string // full repository, e.g. ghcr.io/owner/app
	Repository       string // source owner/repository, which names the default image
	ReleaseTag       string // e.g. v1.2.3
	ReleasePlatforms string // RELEASE_PLATFORMS; empty builds every declared platform
}

// ReleaseImageBuild is one native architecture build of one container.
type ReleaseImageBuild struct {
	Name              string `json:"name"`
	Arch              string `json:"arch"`
	Platform          string `json:"platform"`
	ContainerFile     string `json:"container-file"`
	Context           string `json:"context"`
	Target            string `json:"target"`
	BuildArgs         string `json:"build-args"`                    // newline-separated KEY=VALUE, sorted
	Labels            string `json:"labels,omitempty"`              // per-image public labels, before workflow-wide labels
	BuilderMaterialID string `json:"builder-material-id,omitempty"` // optional opaque identity captured on this native runner
	Revision          string `json:"revision,omitempty"`            // content revision for reusable bases; empty uses the release commit
	CacheTag          string `json:"cache-tag,omitempty"`           // bounded architecture slot in the admitted repository
	RequiredLabels    string `json:"required-labels,omitempty"`
	ImageRepository   string `json:"image-repository"`
	CandidateTag      string `json:"candidate-tag"`
	// FinalTagName is the tag the signer promotes the image to, which the
	// build records as its org.opencontainers.image.ref.name label.
	FinalTagName string `json:"final-tag-name"`
}

// ReleaseImage is one container's publication: its index under the staging
// candidate tag, the final tag the signer promotes it to, and its scan.
type ReleaseImage struct {
	Name            string `json:"name"`
	ImageRepository string `json:"image-repository"`
	CandidateTag    string `json:"candidate-tag"`
	FinalTagName    string `json:"final-tag-name"`
	Archs           string `json:"archs"` // newline-separated, as assemble --archs takes them
	Scan            bool   `json:"scan"`
	ImageKind       string `json:"image-kind,omitempty"`
	Role            string `json:"role,omitempty"`
	Flavor          string `json:"flavor,omitempty"`
	BaseRef         string `json:"base-ref,omitempty"`
	BaseInputID     string `json:"base-input-id,omitempty"`
	MovingTagName   string `json:"moving-tag-name,omitempty"`
	RequiredLabels  string `json:"required-labels,omitempty"`
	ProvenanceJSON  string `json:"provenance-json,omitempty"`
	Revision        string `json:"revision,omitempty"`
	CacheTagPrefix  string `json:"cache-tag-prefix,omitempty"`
}

// ReleaseImagesPlan is the build matrix (container × architecture) and the
// publication matrix (one entry per container) of a release.
type ReleaseImagesPlan struct {
	Builds []ReleaseImageBuild `json:"builds"`
	Images []ReleaseImage      `json:"images"`
}

// PlanReleaseImages turns the declared containers into the release-images
// matrices. It refuses, before any runner starts, what the workflow does not
// do: build secrets, binary extraction, a scan threshold other than its gate,
// platforms other than linux/amd64 and linux/arm64, and two containers that
// would publish the same tag.
func PlanReleaseImages(containers []PlannedContainer, in ReleaseImagesInput) (ReleaseImagesPlan, error) {
	if len(containers) == 0 {
		return ReleaseImagesPlan{}, fmt.Errorf("artifacts.yml declares no containers to release: %w", errs.ErrInvalidConfig)
	}

	// Build metadata (+...) is not a character an image tag may hold.
	if _, ok := version.ParseSemverTag(in.ReleaseTag); !ok || strings.Contains(in.ReleaseTag, "+") {
		return ReleaseImagesPlan{}, fmt.Errorf("release tag %q must be vMAJOR.MINOR.PATCH with an optional -prerelease: %w", in.ReleaseTag, errs.ErrUsage)
	}

	var plan ReleaseImagesPlan

	published := map[string]string{}

	for _, container := range containers {
		image, builds, err := planReleaseImage(container, in)
		if err != nil {
			return ReleaseImagesPlan{}, err
		}

		// The final tag, the staging candidate and its per-architecture tags:
		// a tag-suffix such as -amd64 would otherwise overwrite another
		// image's architecture tag.
		tags := []string{image.FinalTagName, image.CandidateTag}
		for _, build := range builds {
			tags = append(tags, image.CandidateTag+"-"+build.Arch)
		}

		for _, tag := range tags {
			ref := image.ImageRepository + ":" + tag
			if other, taken := published[ref]; taken {
				return ReleaseImagesPlan{}, fmt.Errorf("containers %q and %q would both publish %s; give one a distinct image-name or tag-suffix: %w", other, container.Name, ref, errs.ErrInvalidConfig)
			}

			published[ref] = container.Name
		}

		plan.Images = append(plan.Images, image)
		plan.Builds = append(plan.Builds, builds...)
	}

	return plan, nil
}

func planReleaseImage(container PlannedContainer, in ReleaseImagesInput) (ReleaseImage, []ReleaseImageBuild, error) {
	if err := supportedByReleaseImages(container); err != nil {
		return ReleaseImage{}, nil, err
	}

	repository, err := releaseImageRepository(in.ImageRepository)
	if err != nil {
		return ReleaseImage{}, nil, err
	}

	platforms, err := domaincontainer.SelectPlatforms(splitPlatforms(container.Platforms), in.ReleasePlatforms)
	if err != nil {
		return ReleaseImage{}, nil, fmt.Errorf("container %q: %w", container.Name, err)
	}

	finalTagName := in.ReleaseTag + container.TagSuffix
	image := ReleaseImage{
		Name:            container.Name,
		ImageRepository: repository,
		CandidateTag:    "staging-" + finalTagName,
		FinalTagName:    finalTagName,
		Scan:            container.EnableScan,
	}

	buildArgs := make([]string, 0, len(container.BuildArgs))
	for _, key := range slices.Sorted(maps.Keys(container.BuildArgs)) {
		buildArgs = append(buildArgs, key+"="+container.BuildArgs[key])
	}

	builds := make([]ReleaseImageBuild, 0, len(platforms))
	archs := make([]string, 0, len(platforms))

	for _, platform := range platforms {
		arch, ok := strings.CutPrefix(platform, "linux/")
		if !ok || (arch != domaincontainer.ArchAMD64 && arch != domaincontainer.ArchARM64) {
			return ReleaseImage{}, nil, fmt.Errorf("container %q platform %q: release images build linux/amd64 and linux/arm64: %w", container.Name, platform, errs.ErrInvalidConfig)
		}

		archs = append(archs, arch)
		builds = append(builds, ReleaseImageBuild{
			Name:            container.Name,
			Arch:            arch,
			Platform:        platform,
			ContainerFile:   container.ContainerFile,
			Context:         container.Context,
			Target:          container.Target,
			BuildArgs:       strings.Join(buildArgs, "\n"),
			ImageRepository: repository,
			CandidateTag:    image.CandidateTag,
			FinalTagName:    image.FinalTagName,
		})
	}

	image.Archs = strings.Join(archs, "\n")

	return image, builds, nil
}

// supportedByReleaseImages names the declared features the release-images
// workflow does not carry out, instead of building without them.
// releaseImageRepository is the release's one image repository, written out
// in full so no registry or owner is inferred.
func releaseImageRepository(repository string) (string, error) {
	authority, _, hasPath := strings.Cut(repository, "/")

	hasRegistry := strings.EqualFold(authority, "localhost") || strings.ContainsAny(authority, ".:")
	if !hasPath || !hasRegistry {
		return "", fmt.Errorf("release images: image repository %q must be a full repository such as ghcr.io/owner/app: %w", repository, errs.ErrUsage)
	}

	return domaincontainer.ResolveImageName(domaincontainer.ResolveImageNameInput{ImageName: repository})
}

func supportedByReleaseImages(container PlannedContainer) error {
	switch {
	case container.ImageName != "":
		return fmt.Errorf("container %q declares image-name: a release signs one image repository, the run's image-repository; tell its images apart with tag-suffix: %w", container.Name, errs.ErrInvalidConfig)
	case len(container.BuildSecrets) > 0:
		return fmt.Errorf("container %q declares build-secrets, which release images do not pass to the build: %w", container.Name, errs.ErrInvalidConfig)
	case container.ExtractBinaryTarget != "" || len(container.ExtractBinaryPaths) > 0:
		return fmt.Errorf("container %q declares extract, which release images do not carry out: %w", container.Name, errs.ErrInvalidConfig)
	case container.ScanSeverity != "" && container.ScanSeverity != releaseImagesScanSeverity:
		return fmt.Errorf("container %q scan-severity %q: release images gate at %s; use enable-scan: false to skip the gate: %w", container.Name, container.ScanSeverity, releaseImagesScanSeverity, errs.ErrInvalidConfig)
	// The build job places the checkout beside the layer's own files, so a
	// path out of the repository would build from those.
	case !filepath.IsLocal(container.ContainerFile) || !filepath.IsLocal(container.Context):
		return fmt.Errorf("container %q: container-file %q and context %q must be paths inside the repository: %w", container.Name, container.ContainerFile, container.Context, errs.ErrInvalidConfig)
	}

	return nil
}

func splitPlatforms(declared string) []string {
	var platforms []string

	for platform := range strings.SplitSeq(declared, ",") {
		if platform = strings.TrimSpace(platform); platform != "" {
			platforms = append(platforms, platform)
		}
	}

	return platforms
}
