// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package publish_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apppublish "github.com/diggsweden/forge-ci/internal/app/publish"
	apprelease "github.com/diggsweden/forge-ci/internal/app/release"
)

func releaseMavenFixture(t *testing.T) apprelease.FilesInput {
	t.Helper()
	t.Chdir(t.TempDir())
	const repo = "dist/library-maven-publications/com/example/library/1.2.3/library-1.2.3"
	payloads := map[string]string{
		repo + ".pom": `<project><modelVersion>4.0.0</modelVersion><groupId>com.example</groupId><artifactId>library</artifactId><version>1.2.3</version><packaging>aar</packaging></project>`,
		repo + ".aar": "prebuilt AAR bytes",
	}
	manifest := apprelease.FileManifest{Version: 1}
	write := func(name, body string) apprelease.FileEntry {
		require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o700))
		require.NoError(t, os.WriteFile(name, []byte(body), 0o600))
		entry := apprelease.FileEntry{Path: name, Name: filepath.Base(name), Source: "declared"}
		manifest.Assets = append(manifest.Assets, entry)
		return entry
	}
	var checksums strings.Builder
	for name, body := range payloads {
		write(name, body)
		write(name+".bundle", "test-only signer bundle")
		fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256([]byte(body)), filepath.Base(name))
	}
	manifest.Checksums = []apprelease.FileEntry{write("dist/checksums.sha256", checksums.String())}
	write("dist/checksums.sha256.bundle", "test-only signer bundle")
	manifest.Provenance = []apprelease.FileEntry{write("dist/slsa-provenance.intoto.json", "test-only provenance")}
	write("dist/slsa-provenance.intoto.json.bundle", "test-only signer bundle")
	body, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile("dist/release-files.json", body, 0o600))
	return apprelease.FilesInput{DistDir: "dist", ManifestFile: "dist/release-files.json", ManifestExplicit: true}
}

func TestStageReleaseMavenPublication(t *testing.T) {
	files := releaseMavenFixture(t)
	require.NoError(t, apppublish.StageReleaseMavenPublication(t.Context(), files, "dist/library-maven-publications", "staged", "1.2.3"))
	set, err := apppublish.InspectMavenPublication("staged", "1.2.3", false)
	require.NoError(t, err)
	require.Len(t, set.Files, 2)
	body, err := os.ReadFile("staged/com/example/library/1.2.3/library-1.2.3.aar")
	require.NoError(t, err)
	require.Equal(t, "prebuilt AAR bytes", string(body))
}

func TestStageReleaseMavenPublicationLeavesGPGSignaturesAsReleaseEvidence(t *testing.T) {
	files := releaseMavenFixture(t)
	require.NoError(t, filepath.WalkDir("dist", func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(name, ".bundle") {
			return os.Rename(name, strings.TrimSuffix(name, ".bundle")+".asc")
		}
		return nil
	}))
	body, err := os.ReadFile(files.ManifestFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(files.ManifestFile, []byte(strings.ReplaceAll(string(body), ".bundle", ".asc")), 0o600))
	require.NoError(t, apppublish.StageReleaseMavenPublication(t.Context(), files, "dist/library-maven-publications", "staged", "1.2.3"))
	set, err := apppublish.InspectMavenPublication("staged", "1.2.3", false)
	require.NoError(t, err)
	require.Len(t, set.Files, 2)
}

func TestStageReleaseMavenPublicationPublishesNoUndeclaredFiles(t *testing.T) {
	files := releaseMavenFixture(t)
	require.NoError(t, os.WriteFile("dist/library-maven-publications/credentials.txt", []byte("not declared"), 0o600))
	require.NoError(t, apppublish.StageReleaseMavenPublication(t.Context(), files, "dist/library-maven-publications", "staged", "1.2.3"))
	_, err := os.Stat("staged/credentials.txt")
	require.True(t, os.IsNotExist(err))
}

func TestAdmitReleaseMavenPublicationAcceptsOnlyCompleteUnsignedBytes(t *testing.T) {
	files := releaseMavenFixture(t)
	body, err := os.ReadFile(files.ManifestFile)
	require.NoError(t, err)
	var manifest apprelease.FileManifest
	require.NoError(t, json.Unmarshal(body, &manifest))
	var unsigned []apprelease.FileEntry
	for _, asset := range manifest.Assets {
		if strings.HasSuffix(asset.Path, ".bundle") || strings.Contains(asset.Path, "slsa-provenance") {
			require.NoError(t, os.Remove(asset.Path))
			continue
		}
		unsigned = append(unsigned, asset)
	}
	manifest.Assets, manifest.Provenance = unsigned, nil
	body, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(files.ManifestFile, body, 0600))
	require.NoError(t, apppublish.AdmitReleaseMavenPublication(t.Context(), files, "dist/library-maven-publications", "admitted", "1.2.3"))
	require.Error(t, apppublish.StageReleaseMavenPublication(t.Context(), files, "dist/library-maven-publications", "published", "1.2.3"), "unsigned admission must not enable publication")
}

func TestStageReleaseMavenPublicationRefusesInvalidHandoffs(t *testing.T) {
	for _, testCase := range []string{"outside", "dist root", "missing", "wrong version", "changed payload", "unsigned", "symlink"} {
		t.Run(testCase, func(t *testing.T) {
			files := releaseMavenFixture(t)
			directory, version := "dist/library-maven-publications", "1.2.3"
			payload := directory + "/com/example/library/1.2.3/library-1.2.3.aar"
			switch testCase {
			case "outside":
				directory = "../other"
			case "dist root":
				directory = "dist"
			case "missing":
				directory = "dist/absent"
			case "wrong version":
				version = "9.9.9"
			case "changed payload":
				require.NoError(t, os.WriteFile(payload, []byte("modified"), 0o600))
			case "unsigned":
				require.NoError(t, os.Remove(payload+".bundle"))
			case "symlink":
				require.NoError(t, os.Rename(payload, "outside.aar"))
				require.NoError(t, os.Symlink("../../../../../../outside.aar", payload))
			}
			require.Error(t, apppublish.StageReleaseMavenPublication(t.Context(), files, directory, "staged", version))
			_, err := os.Stat("staged/com/example/library/1.2.3/library-1.2.3.aar")
			require.True(t, os.IsNotExist(err), "no payload installed after refusal: %v", err)
		})
	}
}
