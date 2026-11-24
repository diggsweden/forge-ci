// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diggsweden/forge-ci/internal/cliio"
	domaincontainer "github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/security"
	"github.com/diggsweden/forge-ci/internal/pathsafe"
	"github.com/diggsweden/forge-ci/internal/retry"
)

// The scan settings are those of `security scan container-json`: Trivy's
// vulnerability database download fails transiently, so a failed scan is run
// again, three times in all with a linear backoff.
const (
	imageEvidenceTrivyTimeout           = "30m"
	imageEvidenceTrivyAttempts          = 3
	defaultImageEvidenceTrivyRetryDelay = 20 * time.Second
)

// ImageEvidenceCopier copies one platform of a multi-platform OCI layout into
// a single-platform OCI layout.
type ImageEvidenceCopier interface {
	CopyOCILayoutToOCILayout(ctx context.Context, sourceLayout, destLayout, osName, arch string) error
}

// ImageEvidenceTrivy is the Trivy surface needed for local OCI-layout evidence.
type ImageEvidenceTrivy interface {
	RunInherit(ctx context.Context, stdout, stderr io.Writer, args ...string) (int, error)
}

// ImageEvidenceSyft is the Syft surface needed for optional CycloneDX output.
type ImageEvidenceSyft interface {
	Generate(ctx context.Context, target string, outputs map[string]string, errOut io.Writer) error
}

// ImageEvidenceInput drives image evidence generation in one of two modes:
// one OCILayout scanned into TrivyOutput (and SBOMOutput when set), or a
// multi-platform ScanLayout split per Platforms and each platform scanned
// into TrivyOutputTemplate.
type ImageEvidenceInput struct {
	OCILayout           string
	TrivyOutput         string
	SBOMOutput          string
	ScanLayout          string
	Platforms           []string
	TrivyOutputTemplate string
	// TrivyRetryDelay is the base of the linear backoff between scan
	// attempts. Zero takes the default.
	TrivyRetryDelay time.Duration
	// Gate, when set, judges each platform's own layout after its evidence is
	// recorded, with the publication policy; its error fails the run. Nil
	// records evidence only.
	Gate func(ctx context.Context, layout, platform string) error
}

// imageEvidenceScan is how one Trivy scan runs: the backoff of the bounded
// retry around it, and the platform it scans, os/arch, which is passed to
// Trivy as --platform when the scan names one, as the scan gate does, so
// Trivy reads that platform's image and no other.
type imageEvidenceScan struct {
	delay    time.Duration
	platform string
}

// forPlatform is the scan of one platform's layout.
func (scan imageEvidenceScan) forPlatform(platform imageEvidencePlatform) imageEvidenceScan {
	scan.platform = platform.OS + "/" + platform.Arch

	return scan
}

// imageEvidenceDeps is the collaborator set the evidence pipeline shares: the
// copier that splits a multi-platform layout, the two scanners (trivy, syft),
// and the two writers it narrates on. They thread down the scan tree; the
// innermost trivy primitive (runImageEvidenceTrivy) keeps explicit parameters
// so it is plain what it touches.
type imageEvidenceDeps struct {
	copier ImageEvidenceCopier
	trivy  ImageEvidenceTrivy
	syft   ImageEvidenceSyft
	out    io.Writer // progress, user-facing
	stderr io.Writer // underlying tool output
}

// ImageEvidence scans an OCI layout with Trivy and optionally writes a CycloneDX SBOM with Syft.
func ImageEvidence(ctx context.Context, copier ImageEvidenceCopier, trivy ImageEvidenceTrivy, syft ImageEvidenceSyft, out, stderr io.Writer, in ImageEvidenceInput) error {
	if err := validateImageEvidenceOutputPaths([]string{in.TrivyOutput, in.SBOMOutput}); err != nil {
		return err
	}

	scan := imageEvidenceScan{delay: retry.Delay(in.TrivyRetryDelay, defaultImageEvidenceTrivyRetryDelay)}
	deps := imageEvidenceDeps{copier: copier, trivy: trivy, syft: syft, out: out, stderr: stderr}

	if imageEvidenceMultiArchRequested(in) {
		return imageEvidenceMultiArch(ctx, deps, in, scan)
	}

	if err := validateSingleImageEvidenceInput(in); err != nil {
		return err
	}

	return scanImageEvidenceLayout(ctx, deps, in.OCILayout, in.TrivyOutput, in.SBOMOutput, scan)
}

func validateSingleImageEvidenceInput(in ImageEvidenceInput) error {
	if in.OCILayout == "" {
		return fmt.Errorf("oci layout is required: %w", errs.ErrUsage)
	}

	if in.TrivyOutput == "" {
		return fmt.Errorf("trivy output is required: %w", errs.ErrUsage)
	}

	return requireImageEvidenceDir(in.OCILayout, "oci layout")
}

func imageEvidenceMultiArchRequested(in ImageEvidenceInput) bool {
	return in.ScanLayout != "" || len(in.Platforms) > 0 || in.TrivyOutputTemplate != ""
}

func imageEvidenceMultiArch(ctx context.Context, deps imageEvidenceDeps, in ImageEvidenceInput, scan imageEvidenceScan) error {
	platforms, err := validateMultiArchImageEvidenceInput(in)
	if err != nil {
		return err
	}

	var firstScanErr error

	for _, platform := range platforms {
		scanErr, fatalErr := imageEvidenceScanOnePlatform(ctx, deps, in, scan, platform)
		if fatalErr != nil {
			return fatalErr
		}

		if scanErr != nil && firstScanErr == nil {
			firstScanErr = scanErr
		}
	}

	return firstScanErr
}

// imageEvidenceScanOnePlatform extracts one platform into a temp OCI layout
// and scans it. The first return value is a non-fatal scan/cleanup error (the
// caller keeps going); the second aborts the whole run.
func imageEvidenceScanOnePlatform(ctx context.Context, deps imageEvidenceDeps, in ImageEvidenceInput, scan imageEvidenceScan, platform imageEvidencePlatform) (error, error) {
	layout, err := os.MkdirTemp("", "image-evidence-"+platform.Arch+".*")
	if err != nil {
		return nil, fmt.Errorf("create image evidence temp layout: %w", err)
	}

	if err := deps.copier.CopyOCILayoutToOCILayout(ctx, in.ScanLayout, layout, platform.OS, platform.Arch); err != nil {
		_ = os.RemoveAll(layout)

		return nil, err
	}

	trivyOutput := renderImageEvidenceTemplate(in.TrivyOutputTemplate, platform)

	var scanErr error
	if err := scanImageEvidenceLayout(ctx, deps, layout, trivyOutput, "", scan.forPlatform(platform)); err != nil {
		scanErr = err
	} else if in.Gate != nil {
		scanErr = in.Gate(ctx, layout, platform.OS+"/"+platform.Arch)
	}

	if err := os.RemoveAll(layout); err != nil && scanErr == nil {
		scanErr = fmt.Errorf("remove image evidence temp layout: %w", err)
	}

	return scanErr, nil
}

func validateMultiArchImageEvidenceInput(in ImageEvidenceInput) ([]imageEvidencePlatform, error) {
	if in.OCILayout != "" || in.TrivyOutput != "" || in.SBOMOutput != "" {
		return nil, fmt.Errorf("multi-arch image evidence uses --scan-layout with --trivy-output-template, not single-image source/output flags: %w", errs.ErrUsage)
	}

	platforms, err := parseImageEvidencePlatforms(in.Platforms)
	if err != nil {
		return nil, err
	}

	if err := validateMultiArchImageEvidenceTemplate(in.TrivyOutputTemplate, platforms); err != nil {
		return nil, err
	}

	if in.ScanLayout == "" {
		return nil, fmt.Errorf("scan layout is required for multi-arch image evidence: %w", errs.ErrUsage)
	}

	if err := requireImageEvidenceDir(in.ScanLayout, "scan layout"); err != nil {
		return nil, err
	}

	return platforms, nil
}

// validateMultiArchImageEvidenceTemplate checks the per-arch output template.
func validateMultiArchImageEvidenceTemplate(tpl string, platforms []imageEvidencePlatform) error {
	if tpl == "" {
		return fmt.Errorf("trivy output template is required: %w", errs.ErrUsage)
	}

	if err := validateImageEvidenceOutputTemplate(tpl, "trivy output template", platforms); err != nil {
		return err
	}

	paths := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		paths = append(paths, renderImageEvidenceTemplate(tpl, platform))
	}

	return validateImageEvidenceOutputPaths(paths)
}

func validateImageEvidenceOutputPaths(paths []string) error {
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}

		absolute, err := canonicalSnapshotPath(path)
		if err != nil {
			return err
		}

		if seen[absolute] {
			return fmt.Errorf("image evidence output paths collide: %w", errs.ErrUsage)
		}

		seen[absolute] = true
	}

	return nil
}

type imageEvidencePlatform struct {
	Raw  string
	OS   string
	Arch string
}

func parseImageEvidencePlatforms(values []string) ([]imageEvidencePlatform, error) {
	raws := splitImageEvidenceList(values)
	if len(raws) == 0 {
		return nil, fmt.Errorf("at least one --platform is required for multi-arch image evidence: %w", errs.ErrUsage)
	}

	seen := make(map[string]bool, len(raws))

	platforms := make([]imageEvidencePlatform, 0, len(raws))
	for _, raw := range raws {
		// Evidence scans take exactly os/arch: the OCI variant component is
		// layered out here (the per-arch copy selects os/arch only),
		// on top of the domain parser that owns the OCI format rule.
		parsed, err := domaincontainer.ParsePlatform(raw)
		if err != nil || parsed.Variant != "" {
			return nil, fmt.Errorf("platform %q must be os/arch, for example linux/amd64: %w", raw, errs.ErrUsage)
		}

		osName, arch := parsed.OS, parsed.Arch

		if seen[raw] {
			return nil, fmt.Errorf("duplicate platform %q: %w", raw, errs.ErrUsage)
		}

		seen[raw] = true
		platforms = append(platforms, imageEvidencePlatform{Raw: raw, OS: osName, Arch: arch})
	}

	return platforms, nil
}

func splitImageEvidenceList(values []string) []string {
	var out []string

	for _, value := range values {
		for _, field := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\t' || r == ' '
		}) {
			if field != "" {
				out = append(out, field)
			}
		}
	}

	return out
}

func validateImageEvidenceOutputTemplate(tpl, label string, platforms []imageEvidencePlatform) error {
	if !strings.Contains(tpl, "{arch}") && !strings.Contains(tpl, "{platform}") {
		return fmt.Errorf("%s must contain {arch} or {platform}: %w", label, errs.ErrUsage)
	}

	seen := make(map[string]bool, len(platforms))
	for _, platform := range platforms {
		path := renderImageEvidenceTemplate(tpl, platform)
		if path == "" {
			return fmt.Errorf("%s rendered an empty path for %s: %w", label, platform.Raw, errs.ErrUsage)
		}

		if seen[path] {
			return fmt.Errorf("%s renders duplicate path %q: %w", label, path, errs.ErrUsage)
		}

		seen[path] = true
	}

	return nil
}

func renderImageEvidenceTemplate(tpl string, platform imageEvidencePlatform) string {
	path := strings.ReplaceAll(tpl, "{platform}", strings.ReplaceAll(platform.Raw, "/", "-"))
	path = strings.ReplaceAll(path, "{os}", platform.OS)
	path = strings.ReplaceAll(path, "{arch}", platform.Arch)

	return path
}

func scanImageEvidenceLayout(ctx context.Context, deps imageEvidenceDeps, layout, trivyOutput, sbomOutput string, scan imageEvidenceScan) error { //nolint:cyclop // staging, scanner status, JSON validation and publication have independent failure boundaries.
	trivyStage, err := pathsafe.NewArtifactStaging(filepath.Dir(trivyOutput))
	if err != nil {
		return err
	}
	defer func() { _ = trivyStage.Close() }()

	trivyPath := filepath.Join(trivyStage.Root().Name(), filepath.Base(trivyOutput))

	var sbomStage *pathsafe.ArtifactStaging

	if sbomOutput != "" {
		if deps.syft == nil {
			return fmt.Errorf("SBOM output requires Syft: %w", errs.ErrUsage)
		}

		sbomStage, err = pathsafe.NewArtifactStaging(filepath.Dir(sbomOutput))
		if err != nil {
			return err
		}

		defer func() { _ = sbomStage.Close() }()
	}

	if err := retryImageEvidenceTrivy(ctx, deps, layout, trivyPath, scan); err != nil {
		return err
	}

	if err := validateImageEvidenceTrivyOutput(trivyPath); err != nil {
		return err
	}

	if sbomOutput != "" {
		sbomPath := filepath.Join(sbomStage.Root().Name(), filepath.Base(sbomOutput))
		if err := deps.syft.Generate(ctx, "oci-dir:"+layout, map[string]string{"cyclonedx-json": sbomPath}, deps.stderr); err != nil {
			return fmt.Errorf("syft oci-dir:%s: %w", layout, err)
		}

		body, err := readImageEvidenceReport(sbomPath)
		if err != nil {
			return err
		}

		var bom struct {
			Format      string `json:"bomFormat"`
			SpecVersion string `json:"specVersion"`
			Version     int    `json:"version"`
		}
		if json.Unmarshal(body, &bom) != nil || bom.Format != "CycloneDX" || bom.SpecVersion == "" || bom.Version < 1 {
			return fmt.Errorf("SBOM output must be a CycloneDX document with specVersion and positive version: %w", errs.ErrMalformedInput)
		}
	}

	if err := trivyStage.Install(); err != nil {
		return err
	}

	if sbomStage != nil {
		return sbomStage.Install()
	}

	return nil
}

// retryImageEvidenceTrivy runs the scan until it exits zero or the attempts
// are used up. Only a scan the network stopped is run again: a scanner that
// cannot be started and a failure of its own repeat. Each attempt starts
// without the report a failed one may have left.
func retryImageEvidenceTrivy(ctx context.Context, deps imageEvidenceDeps, layout, trivyOutput string, scan imageEvidenceScan) error {
	return retry.Run(ctx, nil, imageEvidenceTrivyAttempts, scan.delay, func() error {
		if err := os.Remove(trivyOutput); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return retry.Permanent(fmt.Errorf("remove previous trivy output %s: %w", trivyOutput, err))
		}

		err := runImageEvidenceTrivy(ctx, deps.trivy, deps.out, deps.stderr, layout, trivyOutput, scan.platform)
		if err != nil && !errs.Retriable(err) {
			return retry.Permanent(err)
		}

		return err
	}, retry.OnRetry(func(attempt, total int, wait time.Duration, _ error) {
		_, _ = fmt.Fprintf(deps.out, "Trivy image scan failed (attempt %d/%d) for %s, retrying in %s...\n", attempt, total, layout, wait)
	}))
}

// runImageEvidenceTrivy scans layout into trivyOutput, the image of platform
// when one is named. Trivy runs outside the caller's directory, so both
// paths are made absolute.
func runImageEvidenceTrivy(ctx context.Context, trivy ImageEvidenceTrivy, out, stderr io.Writer, layout, trivyOutput, platform string) error {
	input, err := filepath.Abs(layout)
	if err != nil {
		return retry.Permanent(fmt.Errorf("resolve %s: %w", layout, err))
	}

	output, err := filepath.Abs(trivyOutput)
	if err != nil {
		return retry.Permanent(fmt.Errorf("resolve %s: %w", trivyOutput, err))
	}

	args := []string{
		"image",
		"--input", input,
		"--scanners", "vuln",
		"--skip-version-check",
		"--timeout", imageEvidenceTrivyTimeout,
		"--format", "json",
		"--output", output,
	}
	args = append(args, security.TrivyDBSourceArgs()...)

	if platform != "" {
		args = append(args, "--platform", platform)
	}

	var said bytes.Buffer

	code, err := trivy.RunInherit(ctx, out, io.MultiWriter(stderr, &said), args...)
	if err != nil {
		return retry.Permanent(fmt.Errorf("trivy image --input %s: %w", layout, err))
	}

	if code != 0 {
		return fmt.Errorf("trivy image --input %s exited with status %d: %w", layout, code, errs.ToolFailure(errs.TrivyReport(said.String())))
	}

	return nil
}

func validateImageEvidenceTrivyOutput(path string) error {
	body, err := readImageEvidenceReport(path)
	if err != nil {
		return fmt.Errorf("read trivy output %s: %w", path, err)
	}

	if _, err := security.ParseTrivyReport(body); err != nil {
		return fmt.Errorf("parse trivy output %s: %w", path, err)
	}

	return nil
}

func readImageEvidenceReport(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("scanner did not create a regular report: %w", errs.ErrMalformedInput)
	}

	return cliio.ReadFile(path)
}

func requireImageEvidenceDir(path, label string) error {
	info, err := os.Stat(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("%s not found: %s: %w", label, path, errs.ErrMissingInput)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory: %s: %w", label, path, errs.ErrUsage)
	}

	return nil
}
