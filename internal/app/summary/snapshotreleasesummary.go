// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/diggsweden/forge-ci/internal/domain/ci"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/diggsweden/forge-ci/internal/domain/projecttype"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
	domainsummary "github.com/diggsweden/forge-ci/internal/domain/summary"
	domainversion "github.com/diggsweden/forge-ci/internal/domain/version"
)

// SnapshotReleaseSummaryInput drives `summary snapshot-release`. BuildStageJSON and
// PublishStageJSON supply job results; SnapshotArtifactsJSON has npm package
// metadata + a publish-status sentinel ("already-exists" → "skipped"
// rendering with a clarifying note).
type SnapshotReleaseSummaryInput struct {
	ProjectType           projecttype.Type
	ReleaseRef            string
	ReleaseSHA            string
	ReleaseActor          string
	ReleaseRepository     string
	RunURL                string
	BuildStageJSON        string
	PublishStageJSON      string
	SnapshotArtifactsJSON string
	URLs                  provider.WebURLBuilder // nil when the platform has no web UI
	ServerURL             string
	Now                   time.Time
}

// SnapshotReleaseSummary appends the snapshot-release block to the step summary and
// prints a short w banner.
//
//nolint:cyclop // renders one summary block per snapshot-release artifact category.
func SnapshotReleaseSummary(ctx context.Context, sink ci.SummarySink, out io.Writer, in SnapshotReleaseSummaryInput) error {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	short := provider.ShortSHA(in.ReleaseSHA)

	build, err := stageResultFor(in.BuildStageJSON, "dev-build")
	if err != nil {
		return fmt.Errorf("snapshot-build-stage result-json: %w", err)
	}

	publish, err := stageResultFor(in.PublishStageJSON, "dev-publish")
	if err != nil {
		return fmt.Errorf("snapshot-publish-stage result-json: %w", err)
	}

	target := func(stage domainsummary.StageResultEnvelope, key string) string {
		return string(stage.TargetResult(key))
	}
	npmStatus := target(publish, pipeline.TargetNPM)
	buildMavenStatus := target(build, pipeline.TargetMaven)
	buildNPMStatus := target(build, pipeline.TargetNPM)
	buildGradleStatus := target(build, pipeline.TargetGradle)
	buildGoStatus := target(build, pipeline.TargetGo)
	buildCargoStatus := target(build, pipeline.TargetCargo)
	buildGradleAndroidStatus := target(build, pipeline.TargetGradleAndroid)
	buildXcodeStatus := target(build, pipeline.TargetXcodeIOS)
	cargoSBOMStatus := target(publish, pipeline.TargetCargoContainerFirst)
	goSBOMStatus := target(publish, pipeline.TargetGoContainerFirst)
	sbomStatus := target(publish, pipeline.TargetSBOM)

	metadata, err := parseSnapshotMetadata(in.SnapshotArtifactsJSON)
	if err != nil {
		return err
	}

	npmPackageName, npmPackageVersion, npmPublishStatus := metadata.Name, metadata.Version, metadata.Status
	if in.ProjectType == projecttype.NPM && npmStatus == string(domainsummary.ResultSuccess) && strings.TrimSpace(in.SnapshotArtifactsJSON) != "" && (npmPackageName == "" || npmPackageVersion == "") {
		return fmt.Errorf("successful NPM publication has incomplete artifact metadata: %w", errs.ErrMalformedInput)
	}

	_, _ = fmt.Fprintf(out, "================================================\n")
	_, _ = fmt.Fprintf(out, "Generating Dev Release Summary\n")
	_, _ = fmt.Fprintf(out, "================================================\n")
	// The banner goes to the job log, where a line break would start a new
	// line the runner may read as a workflow command.
	_, _ = fmt.Fprintf(out, "Project Type: %s\n", domainsummary.SanitizeCell(string(in.ProjectType)))
	_, _ = fmt.Fprintf(out, "Branch: %s\n", domainsummary.SanitizeCell(in.ReleaseRef))
	_, _ = fmt.Fprintf(out, "Commit: %s\n", short)

	pkgLabel := npmPackageName
	if pkgLabel == "" {
		pkgLabel = "none"
	}

	verLabel := npmPackageVersion
	if verLabel == "" {
		verLabel = "none"
	}

	_, _ = fmt.Fprintf(out, "NPM Package: %s@%s\n\n", pkgLabel, verLabel)

	var builder strings.Builder

	_, _ = fmt.Fprintf(&builder, "# Dev Release Summary\n\n")
	_, _ = fmt.Fprintf(&builder, "## Build Information\n")
	_, _ = fmt.Fprintf(&builder, "| Property | Value |\n")
	_, _ = fmt.Fprintf(&builder, "|----------|-------|\n")
	_, _ = fmt.Fprintf(&builder, "| **Project Type** | `%s` |\n", domainsummary.SanitizeCell(string(in.ProjectType)))
	_, _ = fmt.Fprintf(&builder, "| **Branch** | `%s` |\n", domainsummary.SanitizeCell(in.ReleaseRef))
	_, _ = fmt.Fprintf(&builder, "| **Commit** | `%s` |\n", domainsummary.SanitizeCell(short))
	_, _ = fmt.Fprintf(&builder, "| **Built By** | @%s |\n", domainsummary.SanitizeCell(in.ReleaseActor))
	_, _ = fmt.Fprintf(&builder, "| **Built At** | %s |\n\n", now.UTC().Format("2006-01-02 15:04:05 UTC"))
	_, _ = fmt.Fprintf(&builder, "## Job Status\n")
	_, _ = fmt.Fprintf(&builder, "| Job | Status |\n")
	_, _ = fmt.Fprintf(&builder, "|-----|--------|\n")
	_, _ = fmt.Fprintf(&builder, "| Build Maven | %s |\n", domainsummary.StatusIcon(buildMavenStatus))
	_, _ = fmt.Fprintf(&builder, "| Build NPM | %s |\n", domainsummary.StatusIcon(buildNPMStatus))
	_, _ = fmt.Fprintf(&builder, "| Build Gradle | %s |\n", domainsummary.StatusIcon(buildGradleStatus))
	_, _ = fmt.Fprintf(&builder, "| Build Go | %s |\n", domainsummary.StatusIcon(buildGoStatus))
	_, _ = fmt.Fprintf(&builder, "| Build Cargo | %s |\n", domainsummary.StatusIcon(buildCargoStatus))
	_, _ = fmt.Fprintf(&builder, "| Build Gradle Android | %s |\n", domainsummary.StatusIcon(buildGradleAndroidStatus))
	_, _ = fmt.Fprintf(&builder, "| Build Xcode | %s |\n", domainsummary.StatusIcon(buildXcodeStatus))

	if in.ProjectType == projecttype.NPM {
		row := fmt.Sprintf("| Publish NPM Package | %s |\n", domainsummary.StatusIcon(npmStatus))
		// The sentinel explains a successful job that published nothing; on a
		// failed or skipped job it would call the failure a harmless skip.
		if npmPublishStatus == "already-exists" && npmStatus == string(domainsummary.ResultSuccess) {
			row = fmt.Sprintf("| Publish NPM Package | %s (already published — skipped) |\n",
				domainsummary.StatusIcon(npmStatus))
		}

		builder.WriteString(row)
	}

	_, _ = fmt.Fprintf(&builder, "| Cargo SBOM | %s |\n", domainsummary.StatusIcon(cargoSBOMStatus))
	_, _ = fmt.Fprintf(&builder, "| Go SBOM | %s |\n", domainsummary.StatusIcon(goSBOMStatus))
	_, _ = fmt.Fprintf(&builder, "| Dev SBOMs | %s |\n", domainsummary.StatusIcon(sbomStatus))

	_, _ = fmt.Fprintf(&builder, "\n## Published Artifacts\n")

	if in.ProjectType == projecttype.NPM {
		switch {
		case npmPackageName != "" && npmPackageVersion != "" && npmStatus == string(domainsummary.ResultSuccess):
			_, _ = fmt.Fprintf(&builder, "\n### NPM Package\n")

			if npmPublishStatus == "already-exists" {
				_, _ = fmt.Fprintf(&builder, "> **Note:** Version already existed in registry — publish was skipped (same commit SHA).\n\n")
			}

			_, _ = fmt.Fprintf(&builder, "```\n%s@%s\n```\n\n", npmPackageName, npmPackageVersion)
			_, _ = fmt.Fprintf(&builder, "```bash\nnpm install %s@%s\nnpm install %s@dev\n```\n",
				npmPackageName, npmPackageVersion, npmPackageName)
		case npmStatus == string(domainsummary.ResultSuccess):
			_, _ = fmt.Fprintf(&builder, "\n### NPM Package\nPublished; package metadata unavailable\n")
		default:
			_, _ = fmt.Fprintf(&builder, "\n### NPM Package\nNot published\n")
		}
	}

	_, _ = fmt.Fprintf(&builder, "\n## Resources\n")
	builder.WriteString(resourceLine("Packages", domainsummary.PackagesURL(in.URLs, in.ServerURL, in.ReleaseRepository)))
	builder.WriteString(resourceLine("Workflow Run", in.RunURL))
	builder.WriteString("\n")
	_, _ = fmt.Fprintf(&builder, "These are development artifacts tagged with `dev` and are not intended for production use.\n")

	if err := sink.Append(ctx, builder.String()); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "✓ Dev release summary generated successfully\n")

	return nil
}

type snapshotMetadata struct {
	Name    string `json:"npm_package_name"`
	Version string `json:"npm_package_version"`
	Status  string `json:"npm_publish_status"`
}

var snapshotPackageName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)

func parseSnapshotMetadata(value string) (snapshotMetadata, error) {
	if strings.TrimSpace(value) == "" {
		return snapshotMetadata{}, nil
	}

	var metadata *snapshotMetadata
	if err := json.Unmarshal([]byte(value), &metadata); err != nil {
		return snapshotMetadata{}, fmt.Errorf("parse snapshot artifact metadata: %w: %w", err, errs.ErrMalformedInput)
	}

	if metadata == nil {
		return snapshotMetadata{}, fmt.Errorf("snapshot artifact metadata must be an object: %w", errs.ErrMalformedInput)
	}

	if metadata.Name != "" && (len(metadata.Name) > 214 || !snapshotPackageName.MatchString(metadata.Name)) {
		return snapshotMetadata{}, fmt.Errorf("invalid NPM package name in artifact metadata: %w", errs.ErrMalformedInput)
	}

	if metadata.Version != "" {
		parsed, ok := domainversion.ParseSemver(metadata.Version)
		if !ok || parsed.Version != metadata.Version {
			return snapshotMetadata{}, fmt.Errorf("invalid NPM package version in artifact metadata: %w", errs.ErrMalformedInput)
		}
	}

	return *metadata, nil
}
