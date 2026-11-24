// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/security"
	"github.com/stretchr/testify/require"
)

type fakeImageEvidenceCopy struct {
	sourceLayout string
	destLayout   string
	osName       string
	arch         string
}

type fakeImageEvidenceCopier struct {
	localCopies []fakeImageEvidenceCopy
	fail        bool
}

func (f *fakeImageEvidenceCopier) CopyOCILayoutToOCILayout(_ context.Context, sourceLayout, destLayout, osName, arch string) error {
	f.localCopies = append(f.localCopies, fakeImageEvidenceCopy{sourceLayout: sourceLayout, destLayout: destLayout, osName: osName, arch: arch})
	if f.fail {
		return errors.New("copy failed")
	}

	return os.WriteFile(filepath.Join(destLayout, "index.json"), []byte("{}"), 0o600)
}

type fakeImageEvidenceTrivy struct {
	args     []string
	calls    [][]string
	jsonBody string
	code     int
	noOutput bool
	// codes, when set, gives the exit code of each call in turn.
	codes []int
	// said is what a call that exits non-zero writes to stderr.
	said string
}

// trivyOffline is how Trivy ends when it cannot reach its database.
const trivyOffline = "FATAL\tFatal error\trun error: init error: DB error: failed to download vulnerability DB: Get \"https://mirror.example/v2/\": dial tcp: lookup mirror.example: no such host\n"

func (f *fakeImageEvidenceTrivy) RunInherit(_ context.Context, _, stderr io.Writer, args ...string) (int, error) {
	f.args = append([]string{}, args...)

	f.calls = append(f.calls, append([]string{}, args...))

	code := f.code
	if index := len(f.calls) - 1; index < len(f.codes) {
		code = f.codes[index]
	}

	if code != 0 {
		_, _ = io.WriteString(stderr, f.said)
	}

	if f.noOutput {
		return code, nil
	}

	for index, arg := range args {
		if arg == "--output" && index+1 < len(args) {
			body := f.jsonBody
			if body == "" {
				body = `{"Results":[]}`
			}

			if err := os.WriteFile(args[index+1], []byte(body), 0o600); err != nil {
				return 1, err
			}
		}
	}

	return code, nil
}

type fakeImageEvidenceSyft struct {
	target     string // the last target
	targets    []string
	outputs    map[string]string
	noOutput   bool
	jsonBody   string
	jsonBodies map[string]string
}

func (f *fakeImageEvidenceSyft) Generate(_ context.Context, target string, outputs map[string]string, _ io.Writer) error {
	f.target = target
	f.targets = append(f.targets, target)

	f.outputs = outputs
	if f.noOutput {
		return nil
	}

	for _, path := range outputs {
		body := f.jsonBodies[target]
		if body == "" {
			body = f.jsonBody
		}

		if body == "" {
			body = `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1}`
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return err
		}
	}

	return nil
}

func TestImageEvidence_OCILayoutScansAndWritesOptionalSBOM(t *testing.T) {
	t.Parallel()
	work := t.TempDir()

	layout := filepath.Join(work, "layout")
	if err := os.Mkdir(layout, 0o755); err != nil {
		t.Fatal(err)
	}

	trivyOut := filepath.Join(work, "trivy.json")
	sbomOut := filepath.Join(work, "sbom.json")
	trivy := &fakeImageEvidenceTrivy{}
	syft := &fakeImageEvidenceSyft{}

	if err := appcontainer.ImageEvidence(context.Background(), &fakeImageEvidenceCopier{}, trivy, syft, io.Discard, io.Discard, appcontainer.ImageEvidenceInput{
		OCILayout:   layout,
		TrivyOutput: trivyOut,
		SBOMOutput:  sbomOut,
	}); err != nil {
		t.Fatal(err)
	}

	stagedTrivy := trivyArg(t, trivy.args, "--output")
	assertPrivateEvidenceOutput(t, stagedTrivy, trivyOut)

	wantArgs := slices.Concat(
		[]string{"image", "--input", layout, "--scanners", "vuln", "--skip-version-check", "--timeout", "30m", "--format", "json", "--output", stagedTrivy},
		security.TrivyDBSourceArgs(),
	)

	if !slices.Equal(trivy.args, wantArgs) {
		t.Fatalf("trivy args = %#v, want %#v", trivy.args, wantArgs)
	}

	assertPrivateEvidenceOutput(t, syft.outputs["cyclonedx-json"], sbomOut)

	if syft.target != "oci-dir:"+layout {
		t.Fatalf("syft target=%q outputs=%v", syft.target, syft.outputs)
	}
}

func TestImageEvidence_ValidatesReportBeforePublication(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"empty-object", `{}`, false},
		{"unknown-only", `{"unexpected":true}`, false},
		{"unnamed-null", `{"Results":null}`, false},
		{"blank-name", `{"ArtifactName":" \t"}`, false},
		{"typed-field", `{"Results":[{"Vulnerabilities":"invalid"}]}`, false},
		{"syntax", `{"Results":[`, false},
		{"empty-array", `{"Results":[],"Future":true}`, true},
		{"named-omitted", `{"ArtifactName":"image"}`, true},
		{"named-null", `{"ArtifactName":"image","Results":null}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root, layout := t.TempDir(), t.TempDir()

			trivyPath, sbomPath := filepath.Join(root, "trivy.json"), filepath.Join(root, "sbom.json")
			for _, path := range []string{trivyPath, sbomPath} {
				require.NoError(t, os.WriteFile(path, []byte("OLD-CANARY"), 0o600))
			}

			before := snapshotContainerTree(t, root)
			trivy, syft := &fakeImageEvidenceTrivy{jsonBody: tc.body}, &fakeImageEvidenceSyft{}

			var out bytes.Buffer

			err := appcontainer.ImageEvidence(t.Context(), nil, trivy, syft, &out, &out, appcontainer.ImageEvidenceInput{OCILayout: layout, TrivyOutput: trivyPath, SBOMOutput: sbomPath})
			require.Len(t, trivy.calls, 1)
			staged := trivyArg(t, trivy.args, "--output")
			require.NotEqual(t, trivyPath, staged)
			_, statErr := os.Stat(filepath.Dir(staged))
			require.ErrorIs(t, statErr, os.ErrNotExist)

			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, []string{"oci-dir:" + layout}, syft.targets)

				body, readErr := os.ReadFile(trivyPath)
				require.NoError(t, readErr)
				require.Equal(t, tc.body, string(body))
				body, readErr = os.ReadFile(sbomPath)
				require.NoError(t, readErr)
				require.JSONEq(t, `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1}`, string(body))

				entries, readErr := os.ReadDir(root)
				require.NoError(t, readErr)
				require.Len(t, entries, 2, "only published reports should remain")
			} else {
				require.ErrorIs(t, err, errs.ErrMalformedInput)
				require.Empty(t, syft.targets)
				require.Equal(t, before, snapshotContainerTree(t, root))
			}

			if tc.name == "typed-field" {
				var cause *json.UnmarshalTypeError
				require.ErrorAs(t, err, &cause)
			}

			if tc.name == "syntax" {
				var cause *json.SyntaxError
				require.ErrorAs(t, err, &cause)
			}
		})
	}
}

// assertNoEvidenceToolsRan requires that a refused input reached none of the
// three tools, so an input refused only after a copy, a scan or an SBOM would
// fail.
func assertNoEvidenceToolsRan(t *testing.T, copier *fakeImageEvidenceCopier, trivy *fakeImageEvidenceTrivy, syft *fakeImageEvidenceSyft) {
	t.Helper()

	if len(copier.localCopies) != 0 {
		t.Errorf("copier copied before validation: %+v", copier.localCopies)
	}

	if len(trivy.calls) != 0 {
		t.Errorf("trivy scanned before validation: %v", trivy.calls)
	}

	if len(syft.targets) != 0 {
		t.Errorf("syft generated an SBOM before validation: %v", syft.targets)
	}
}

func TestImageEvidence_RejectsInvalidInputsBeforeTools(t *testing.T) {
	t.Parallel()
	work := t.TempDir()

	layout := filepath.Join(work, "layout")
	if err := os.Mkdir(layout, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		in   appcontainer.ImageEvidenceInput
		want error
	}{
		{name: "no source", in: appcontainer.ImageEvidenceInput{TrivyOutput: filepath.Join(work, "trivy.json")}, want: errs.ErrUsage},
		{name: "no trivy output", in: appcontainer.ImageEvidenceInput{OCILayout: layout, SBOMOutput: filepath.Join(work, "sbom.json")}, want: errs.ErrUsage},
		{name: "missing layout", in: appcontainer.ImageEvidenceInput{OCILayout: filepath.Join(work, "missing"), TrivyOutput: filepath.Join(work, "trivy.json")}, want: errs.ErrMissingInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			copier := &fakeImageEvidenceCopier{}
			trivy := &fakeImageEvidenceTrivy{}
			syft := &fakeImageEvidenceSyft{}

			err := appcontainer.ImageEvidence(context.Background(), copier, trivy, syft, io.Discard, io.Discard, tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}

			assertNoEvidenceToolsRan(t, copier, trivy, syft)
		})
	}
}

// TestImageEvidence_ScansEachPlatformFromItsOwnLayout covers a multi-arch run
// from a scan layout: the layout is split into a layout per platform, and each
// platform is scanned from its own layout.
//
// That last part is the claim worth having. Per-platform output files prove
// only that each scan was told where to write; scanning one platform's layout
// twice writes both files just as happily. Every scan is now tied back to the
// layout copier produced for that architecture.
func TestImageEvidence_ScansEachPlatformFromItsOwnLayout(t *testing.T) {
	t.Parallel()
	work := t.TempDir()

	scanLayout := filepath.Join(work, "scan-layout")
	if err := os.Mkdir(scanLayout, 0o755); err != nil {
		t.Fatal(err)
	}

	copier := &fakeImageEvidenceCopier{}
	trivy := &fakeImageEvidenceTrivy{}

	err := appcontainer.ImageEvidence(context.Background(), copier, trivy, nil, io.Discard, io.Discard, appcontainer.ImageEvidenceInput{
		ScanLayout:          scanLayout,
		Platforms:           []string{"linux/amd64", "linux/arm64"},
		TrivyOutputTemplate: filepath.Join(work, "trivy-{arch}.json"),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("splits the scan layout into one layout per platform", func(t *testing.T) {
		t.Parallel()

		assertPlatformLayouts(t, scanLayout, copier.localCopies)
	})

	t.Run("scans each platform from the layout made for it", func(t *testing.T) {
		t.Parallel()

		assertPerPlatformScans(t, trivy, copier.localCopies, work)
	})

	t.Run("writes the evidence, removes every temporary layout and keeps the caller's", func(t *testing.T) {
		t.Parallel()

		assertEvidenceWrittenAndCleanedUp(t, copier.localCopies, work)

		if _, err := os.Stat(scanLayout); err != nil {
			t.Errorf("caller-owned scan layout was removed: %v", err)
		}
	})
}

// assertPlatformLayouts requires one layout per platform, each split from the
// caller's scan layout.
func assertPlatformLayouts(t *testing.T, scanLayout string, copies []fakeImageEvidenceCopy) {
	t.Helper()

	if len(copies) != 2 {
		t.Fatalf("local copies = %d, want one per platform", len(copies))
	}

	for index, arch := range []string{"amd64", "arm64"} {
		if copies[index].sourceLayout != scanLayout {
			t.Errorf("copy[%d] source = %q, want the scan layout %q", index, copies[index].sourceLayout, scanLayout)
		}

		if copies[index].osName != "linux" || copies[index].arch != arch {
			t.Errorf("copy[%d] platform = %s/%s, want linux/%s", index, copies[index].osName, copies[index].arch, arch)
		}
	}
}

// assertPerPlatformScans ties every scan back to the layout built for that
// architecture. Per-platform output files prove only that each scan was told
// where to write; scanning one platform twice writes both files just as well.
func assertPerPlatformScans(t *testing.T, trivy *fakeImageEvidenceTrivy, copies []fakeImageEvidenceCopy, work string) {
	t.Helper()

	if len(trivy.calls) != 2 || len(copies) != 2 {
		t.Fatalf("trivy calls = %d, copies = %d, want 2 of each", len(trivy.calls), len(copies))
	}

	for index, arch := range []string{"amd64", "arm64"} {
		layout := copies[index].destLayout

		// The layout holds one platform; --platform makes Trivy read that
		// platform's image and no other, as the scan gate does.
		if got := trivyArg(t, trivy.calls[index], "--platform"); got != "linux/"+arch {
			t.Errorf("trivy scanned platform %q for %s, want linux/%s", got, arch, arch)
		}

		if got := trivyArg(t, trivy.calls[index], "--input"); got != layout {
			t.Errorf("trivy scanned %q for %s, want that platform's layout %q", got, arch, layout)
		}

		assertPrivateEvidenceOutput(t, trivyArg(t, trivy.calls[index], "--output"), filepath.Join(work, "trivy-"+arch+".json"))
	}
}

// assertEvidenceWrittenAndCleanedUp checks the evidence files exist and that no
// temporary layout survives. The layouts are working copies of a whole image;
// leaving them behind fills the runner's disk one release at a time.
func assertEvidenceWrittenAndCleanedUp(t *testing.T, copies []fakeImageEvidenceCopy, work string) {
	t.Helper()

	for _, name := range []string{"trivy-amd64.json", "trivy-arm64.json"} {
		if _, err := os.Stat(filepath.Join(work, name)); err != nil {
			t.Errorf("evidence %s missing: %v", name, err)
		}
	}

	assertTemporaryLayoutsRemoved(t, copies)
}

func assertTemporaryLayoutsRemoved(t *testing.T, copies []fakeImageEvidenceCopy) {
	t.Helper()

	for _, copied := range copies {
		if _, err := os.Stat(copied.destLayout); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("temporary layout %s was not cleaned up: %v", copied.destLayout, err)
		}
	}
}

// trivyArg returns the value following flag in a recorded trivy invocation.
func trivyArg(t *testing.T, args []string, flag string) string {
	t.Helper()

	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}

	t.Fatalf("trivy called without %s: %v", flag, args)

	return ""
}

func TestImageEvidence_RejectsInvalidMultiArchInputsBeforeTools(t *testing.T) {
	t.Parallel()
	work := t.TempDir()

	layout := filepath.Join(work, "layout")
	if err := os.Mkdir(layout, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		in   appcontainer.ImageEvidenceInput
		want error
	}{
		{name: "no scan layout", in: appcontainer.ImageEvidenceInput{Platforms: []string{"linux/amd64"}, TrivyOutputTemplate: filepath.Join(work, "trivy-{arch}.json")}, want: errs.ErrUsage},
		{name: "no platforms", in: appcontainer.ImageEvidenceInput{ScanLayout: layout, TrivyOutputTemplate: filepath.Join(work, "trivy-{arch}.json")}, want: errs.ErrUsage},
		{name: "no template", in: appcontainer.ImageEvidenceInput{ScanLayout: layout, Platforms: []string{"linux/amd64"}}, want: errs.ErrUsage},
		{name: "constant template", in: appcontainer.ImageEvidenceInput{ScanLayout: layout, Platforms: []string{"linux/amd64"}, TrivyOutputTemplate: filepath.Join(work, "trivy.json")}, want: errs.ErrUsage},
		{name: "duplicate template", in: appcontainer.ImageEvidenceInput{ScanLayout: layout, Platforms: []string{"linux/amd64", "windows/amd64"}, TrivyOutputTemplate: filepath.Join(work, "trivy-{arch}.json")}, want: errs.ErrUsage},
		{name: "colliding template", in: appcontainer.ImageEvidenceInput{ScanLayout: layout, Platforms: []string{"linux/amd64", "windows/amd64"}, TrivyOutputTemplate: filepath.Join(work, "{platform}") + "/../trivy-{arch}.json"}, want: errs.ErrUsage},
		{name: "invalid platform", in: appcontainer.ImageEvidenceInput{ScanLayout: layout, Platforms: []string{"amd64"}, TrivyOutputTemplate: filepath.Join(work, "trivy-{arch}.json")}, want: errs.ErrUsage},
		{name: "mixed single and multi flags", in: appcontainer.ImageEvidenceInput{OCILayout: layout, ScanLayout: layout, Platforms: []string{"linux/amd64"}, TrivyOutputTemplate: filepath.Join(work, "trivy-{arch}.json")}, want: errs.ErrUsage},
		{name: "missing scan layout", in: appcontainer.ImageEvidenceInput{ScanLayout: filepath.Join(work, "missing"), Platforms: []string{"linux/amd64"}, TrivyOutputTemplate: filepath.Join(work, "trivy-{arch}.json")}, want: errs.ErrMissingInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			copier := &fakeImageEvidenceCopier{}
			trivy := &fakeImageEvidenceTrivy{}
			syft := &fakeImageEvidenceSyft{}

			err := appcontainer.ImageEvidence(context.Background(), copier, trivy, syft, io.Discard, io.Discard, tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}

			assertNoEvidenceToolsRan(t, copier, trivy, syft)
		})
	}
}

// TestImageEvidence_OnePlatformFailingLeavesItsPeersAndNoLayouts covers a
// multi-arch run in which scanning fails. A failed scan of one platform does
// not stop the next: every platform is scanned, the first failure is the one
// returned, only the platform that succeeded publishes evidence, and no
// temporary layout survives either outcome. Failing to extract a platform
// layout is different: it aborts the run before any scan, and still cleans up.
func TestImageEvidence_OnePlatformFailingLeavesItsPeersAndNoLayouts(t *testing.T) {
	t.Parallel()

	input := func(work string) appcontainer.ImageEvidenceInput {
		return appcontainer.ImageEvidenceInput{
			ScanLayout:          filepath.Join(work, "scan-layout"),
			Platforms:           []string{"linux/amd64", "linux/arm64", "linux/s390x"},
			TrivyOutputTemplate: filepath.Join(work, "evidence", "trivy-{arch}.json"),
		}
	}

	prepare := func(t *testing.T) string {
		t.Helper()

		work := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(work, "evidence"), 0o750))
		require.NoError(t, os.MkdirAll(filepath.Join(work, "scan-layout"), 0o750))

		return work
	}

	t.Run("scan failures", func(t *testing.T) {
		t.Parallel()

		work := prepare(t)
		copier := &fakeImageEvidenceCopier{}
		// The failures say nothing of the network, so none is retried: this
		// test is about a scan that stays failed, not the retry around it.
		trivy := &fakeImageEvidenceTrivy{codes: []int{3, 0, 5}}

		err := appcontainer.ImageEvidence(context.Background(), copier, trivy, nil, io.Discard, io.Discard, input(work))
		require.ErrorIs(t, err, errs.ErrValidation)
		require.Contains(t, err.Error(), "exited with status 3")
		require.NotContains(t, err.Error(), "status 5")

		require.Len(t, trivy.calls, 3, "every platform is scanned")

		entries, readErr := os.ReadDir(filepath.Join(work, "evidence"))
		require.NoError(t, readErr)

		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}

		require.ElementsMatch(t, []string{"trivy-arm64.json"}, names)
		require.Len(t, copier.localCopies, 3)
		assertTemporaryLayoutsRemoved(t, copier.localCopies)
	})

	t.Run("layout extraction failure", func(t *testing.T) {
		t.Parallel()

		work := prepare(t)
		copier := &fakeImageEvidenceCopier{fail: true}
		trivy := &fakeImageEvidenceTrivy{}

		err := appcontainer.ImageEvidence(context.Background(), copier, trivy, nil, io.Discard, io.Discard, input(work))
		require.Error(t, err)
		require.Len(t, copier.localCopies, 1, "extraction failure aborts before the next platform")
		require.Empty(t, trivy.calls)
		assertTemporaryLayoutsRemoved(t, copier.localCopies)
	})
}

// Trivy's database download fails transiently, so a scan the network stopped
// is run again, three times in all: a later success publishes the report of
// the attempt that passed, an exhausted bound returns the last failure as an
// unavailable dependency and publishes nothing, and a report left by a failed
// attempt is gone before the next one starts. A scan Trivy fails for a reason
// of its own is returned from the first attempt as Trivy's failure.
func TestImageEvidence_RetriesAFailedScanWithinTheAttemptBound(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		codes     []int
		wantCalls int
		wantErr   string
		said      string
		wantClass error
	}{
		{name: "passes on the third attempt", codes: []int{1, 2, 0}, wantCalls: 3, said: trivyOffline},
		{name: "bound exhausted", codes: []int{1, 2, 3}, wantCalls: 3, wantErr: "exited with status 3", said: trivyOffline, wantClass: errs.ErrDependencyUnavailable},
		{name: "a failure that is not the network", codes: []int{1, 0}, wantCalls: 1, wantErr: "exited with status 1", said: "FATAL\tFatal error\trun error: image scan error: unable to initialize the scanner\n", wantClass: errs.ErrValidation},
		{name: "a failure that says nothing", codes: []int{1, 0}, wantCalls: 1, wantErr: "exited with status 1", wantClass: errs.ErrValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			layout := filepath.Join(root, "layout")
			require.NoError(t, os.Mkdir(layout, 0o750))

			trivyPath := filepath.Join(root, "trivy.json")
			trivy := &fakeImageEvidenceTrivy{codes: tc.codes, said: tc.said}

			var out bytes.Buffer

			err := appcontainer.ImageEvidence(t.Context(), nil, trivy, nil, &out, io.Discard, appcontainer.ImageEvidenceInput{
				OCILayout: layout, TrivyOutput: trivyPath, TrivyRetryDelay: time.Millisecond,
			})
			require.Len(t, trivy.calls, tc.wantCalls)
			require.Equal(t, tc.wantCalls-1, strings.Count(out.String(), "retrying in"), out.String())

			if tc.wantErr != "" {
				require.ErrorIs(t, err, tc.wantClass)
				require.Equal(t, errors.Is(tc.wantClass, errs.ErrDependencyUnavailable), errors.Is(err, errs.ErrDependencyUnavailable))
				require.Contains(t, err.Error(), tc.wantErr)
				require.NoFileExists(t, trivyPath)

				return
			}

			require.NoError(t, err)
			require.FileExists(t, trivyPath)
		})
	}
}

type unstartableImageEvidenceTrivy struct{ calls int }

func (u *unstartableImageEvidenceTrivy) RunInherit(context.Context, io.Writer, io.Writer, ...string) (int, error) {
	u.calls++

	return -1, errs.ErrDependencyUnavailable
}

// A scanner that cannot be started is not a transient failure.
func TestImageEvidence_DoesNotRetryWhatRetryingCannotFix(t *testing.T) {
	t.Parallel()

	layout := t.TempDir()
	trivyPath := filepath.Join(t.TempDir(), "trivy.json")

	missing := &unstartableImageEvidenceTrivy{}
	err := appcontainer.ImageEvidence(t.Context(), nil, missing, nil, io.Discard, io.Discard, appcontainer.ImageEvidenceInput{
		OCILayout: layout, TrivyOutput: trivyPath, TrivyRetryDelay: time.Millisecond,
	})
	require.ErrorIs(t, err, errs.ErrDependencyUnavailable)
	require.Equal(t, 1, missing.calls)
}
