// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func assertPrivateEvidenceOutput(t *testing.T, staged, published string) {
	t.Helper()
	require.NotEqual(t, published, staged)
	require.Equal(t, filepath.Base(published), filepath.Base(staged))
	parent, err := filepath.EvalSymlinks(filepath.Dir(published))
	require.NoError(t, err)
	require.Equal(t, parent, filepath.Dir(filepath.Dir(staged)))
	_, err = os.Stat(staged)
	require.ErrorIs(t, err, os.ErrNotExist)
	body, err := os.ReadFile(published)
	require.NoError(t, err)
	require.NotEmpty(t, body)
}

func TestEvidenceBoundary_RefusesCollisionsBeforeTools(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	copier := &fakeImageEvidenceCopier{}
	trivy, syft := &fakeImageEvidenceTrivy{}, &fakeImageEvidenceSyft{}

	in := appcontainer.ImageEvidenceInput{OCILayout: root, TrivyOutput: filepath.Join(root, "report.json"), SBOMOutput: root + "/./report.json"}

	err := appcontainer.ImageEvidence(t.Context(), copier, trivy, syft, io.Discard, io.Discard, in)
	require.ErrorIs(t, err, errs.ErrUsage)
	require.Empty(t, copier.localCopies)
	require.Empty(t, trivy.calls)
	require.Empty(t, syft.targets)
}

func TestEvidenceBoundary_RequiresFreshCompleteReports(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"missing-trivy", "missing-sbom", "malformed-sbom", "success"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			layout := filepath.Join(root, "layout")
			require.NoError(t, os.Mkdir(layout, 0o700))

			trivyPath, sbomPath := filepath.Join(root, "trivy.json"), filepath.Join(root, "sbom.json")
			require.NoError(t, os.WriteFile(trivyPath, []byte(`{"Results":[],"old":true}`), 0o600))
			require.NoError(t, os.WriteFile(sbomPath, []byte(`{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,"old":true}`), 0o600))
			before := snapshotContainerTree(t, root)
			trivy := &fakeImageEvidenceTrivy{noOutput: kind == "missing-trivy"}

			syft := &fakeImageEvidenceSyft{noOutput: kind == "missing-sbom"}
			if kind == "malformed-sbom" {
				syft.jsonBody = `{"bomFormat":"SPDX","specVersion":"1.6","version":1}`
			}

			err := appcontainer.ImageEvidence(t.Context(), nil, trivy, syft, io.Discard, io.Discard, appcontainer.ImageEvidenceInput{OCILayout: layout, TrivyOutput: trivyPath, SBOMOutput: sbomPath})
			if kind != "success" {
				require.ErrorIs(t, err, errs.ErrMalformedInput)
				require.Equal(t, before, snapshotContainerTree(t, root))
			} else {
				require.NoError(t, err)
				body, err := os.ReadFile(sbomPath)
				require.NoError(t, err)
				require.JSONEq(t, `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1}`, string(body))
				assertPrivateEvidenceOutput(t, trivyArg(t, trivy.args, "--output"), trivyPath)
			}
		})
	}
}
