// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	apprelease "github.com/diggsweden/forge-ci/internal/app/release"
)

// GitLabReleaseBundleEvidence is the verified checksum pair used by the live
// tamper tests. Successful pipeline status alone is not release acceptance.
type GitLabReleaseBundleEvidence struct {
	ChecksumName string
	Checksum     []byte
	Bundle       []byte
	Verified     int
}

// VerifyGitLabReleaseEvidence uses the native file-contract CLI in private
// workspaces, never changing the suite's working directory or reimplementing
// manifest/checksum policy. The caller binds both archives to the same native
// child pipeline and uses its authenticated release-asset reader for download.
func VerifyGitLabReleaseEvidence(ctx context.Context, binary string, producerZIP, signerZIP []byte, names []string,
	download func(string) ([]byte, error), verify func([]byte, []byte) error,
) (GitLabReleaseBundleEvidence, error) {
	var result GitLabReleaseBundleEvidence
	root, err := os.MkdirTemp("", "gitlab-release-evidence-")
	if err != nil {
		return result, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	producer, signer, published := filepath.Join(root, "producer"), filepath.Join(root, "signer"), filepath.Join(root, "published")
	if err := stageReleaseArchive(producerZIP, "_release-build/", producer); err != nil {
		return result, err
	}
	if err := stageReleaseArchive(signerZIP, "_release-sign/", signer); err != nil {
		return result, err
	}
	for _, dir := range []string{producer, signer} {
		digest, err := os.ReadFile(filepath.Join(dir, "digest"))
		if err != nil {
			return result, err
		}
		if err := apprelease.VerifyDistWithManifestRoot(filepath.Join(dir, "dist"), strings.TrimSpace(string(digest)), "."); err != nil {
			return result, err
		}
	}
	run := func(dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, binary, append([]string{"release", "files"}, args...)...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "TMPDIR=" + root, "NO_COLOR=1"}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("native release files %v: %w: %s", args, err, stderr.String())
		}
		return out, nil
	}
	flags := []string{"--dist-dir", "dist", "--manifest", "dist/release-files.json"}
	if _, err := run(producer, append([]string{"validate", "--unsigned"}, flags...)...); err != nil {
		return result, err
	}
	if _, err := run(producer, append([]string{"validate-checksums"}, flags...)...); err != nil {
		return result, err
	}
	original, err := readReleaseEvidenceManifest(producer, "dist/release-files.json")
	if err != nil {
		return result, err
	}
	// Recreate producer classification through the owning constructors. A
	// non-empty but fabricated Source/section is not an accepted declaration.
	args := []string{"import-goreleaser", "--dist-dir", "dist", "--manifest", "acceptance-expected.json"}
	if _, err := os.Stat(filepath.Join(producer, "dist/artifacts.json")); os.IsNotExist(err) {
		args = []string{"declare", "--dist-dir", "dist", "--manifest", "acceptance-expected.json", "--checksums-file", original.Checksums[0].Path}
		for _, a := range original.Assets {
			if slices.ContainsFunc(original.Checksums, func(e apprelease.FileEntry) bool { return e.Path == a.Path }) ||
				slices.ContainsFunc(original.Evidence, func(e apprelease.FileEntry) bool { return e.Path == a.Path }) ||
				slices.ContainsFunc(original.SBOMs, func(e apprelease.FileEntry) bool { return e.Path == a.Path }) {
				continue
			}
			args = append(args, "--asset", a.Path)
		}
		for _, a := range original.SBOMs {
			args = append(args, "--sbom", a.Path)
		}
		// Only the validated checksum file in this owned copy is replaced by
		// the constructor; the retained signer/published bytes remain untouched.
		if err := os.Remove(filepath.Join(producer, original.Checksums[0].Path)); err != nil {
			return result, err
		}
	} else if err != nil {
		return result, err
	}
	for _, a := range original.Evidence {
		args = append(args, "--evidence-file", a.Path)
	}
	if _, err := run(producer, args...); err != nil {
		return result, err
	}
	expectedProducer, err := readReleaseEvidenceManifest(producer, "acceptance-expected.json")
	if err != nil {
		return result, err
	}
	if !sameReleaseManifest(original, expectedProducer) {
		return result, fmt.Errorf("producer manifest disagrees with native producer classification")
	}
	for _, a := range original.Assets {
		before, err := os.ReadFile(filepath.Join(producer, a.Path))
		if err != nil {
			return result, err
		}
		after, err := os.ReadFile(filepath.Join(signer, a.Path))
		if err != nil {
			return result, err
		}
		if !bytes.Equal(before, after) {
			return result, fmt.Errorf("signer changed producer subject %s", a.Name)
		}
	}
	if _, err := run(signer, append([]string{"validate"}, flags...)...); err != nil {
		return result, err
	}
	finalized, err := readReleaseEvidenceManifest(signer, "dist/release-files.json")
	if err != nil {
		return result, err
	}
	// Finalize the actual producer declaration against retained signer files;
	// do not trust roles asserted only by the finalized manifest.
	body, err := json.Marshal(original)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(signer, "producer-manifest.json"), body, 0o600); err != nil {
		return result, err
	}
	if _, err := run(signer, "manifest", "--dist-dir", "dist", "--manifest", "producer-manifest.json", "--output", "acceptance-expected.json"); err != nil {
		return result, err
	}
	expectedSigner, err := readReleaseEvidenceManifest(signer, "acceptance-expected.json")
	if err != nil {
		return result, err
	}
	if !sameReleaseManifest(finalized, expectedSigner) {
		return result, fmt.Errorf("signer manifest disagrees with native finalization of producer declaration")
	}
	checksumPath, err := run(signer, append([]string{"checksum-file"}, flags...)...)
	if err != nil {
		return result, err
	}
	checksum := strings.TrimSpace(string(checksumPath))
	result.ChecksumName = filepath.Base(checksum)
	wanted := make([]string, 0, len(finalized.Assets))
	for _, a := range finalized.Assets {
		wanted = append(wanted, a.Name)
	}
	actual := slices.Clone(names)
	slices.Sort(wanted)
	slices.Sort(actual)
	if !slices.Equal(wanted, actual) {
		return result, fmt.Errorf("published release asset set differs from finalized signer declaration")
	}
	for _, required := range []string{result.ChecksumName, apprelease.ProvenanceStatementName} {
		if !slices.Contains(wanted, required) || !slices.Contains(wanted, required+".bundle") {
			return result, fmt.Errorf("release lacks mandatory signed subject %s", required)
		}
	}
	if err := os.MkdirAll(filepath.Join(published, "dist"), 0o700); err != nil {
		return result, err
	}
	manifestBody, err := os.ReadFile(filepath.Join(signer, "dist/release-files.json"))
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(published, "dist/release-files.json"), manifestBody, 0o600); err != nil {
		return result, err
	}
	var remaining int64 = 1 << 30
	for _, a := range finalized.Assets {
		payload, err := download(a.Name)
		if err != nil {
			return result, fmt.Errorf("published asset %s: %w", a.Name, err)
		}
		remaining -= int64(len(payload))
		if remaining < 0 {
			return result, fmt.Errorf("published release exceeds evidence byte budget")
		}
		p := filepath.Join(published, a.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return result, err
		}
		if err := os.WriteFile(p, payload, 0o600); err != nil {
			return result, err
		}
	}
	retained, err := os.ReadFile(filepath.Join(signer, checksum))
	if err != nil {
		return result, err
	}
	result.Checksum, err = os.ReadFile(filepath.Join(published, checksum))
	if err != nil {
		return result, err
	}
	if !bytes.Equal(retained, result.Checksum) {
		return result, fmt.Errorf("published checksum bytes differ from retained signer subject")
	}
	// Exact coverage and hashes use the native owner, including its exclusions
	// for checksum-self, evidence, provenance and post-signing sidecars.
	if _, err := run(published, append([]string{"validate-checksums"}, flags...)...); err != nil {
		return result, err
	}
	for _, a := range finalized.Assets {
		plain, bundled := strings.CutSuffix(a.Path, ".bundle")
		if !bundled {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(published, plain))
		if err != nil {
			return result, err
		}
		retainedSubject, err := os.ReadFile(filepath.Join(signer, plain))
		if err != nil {
			return result, err
		}
		if !bytes.Equal(blob, retainedSubject) {
			return result, fmt.Errorf("published signed subject %s differs from retained signer bytes", plain)
		}
		bundle, err := os.ReadFile(filepath.Join(published, a.Path))
		if err != nil {
			return result, err
		}
		if err := verify(blob, bundle); err != nil {
			return result, fmt.Errorf("published signature %s: %w", a.Name, err)
		}
		result.Verified++
		if plain == checksum {
			result.Bundle = bundle
		}
	}
	return result, nil
}

func readReleaseEvidenceManifest(root, name string) (apprelease.FileManifest, error) {
	var m apprelease.FileManifest
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return m, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&m)
	return m, err
}

func sameReleaseManifest(a, b apprelease.FileManifest) bool {
	// Sections are sets, but every native validator has already rejected
	// duplicate/conflicting entries. Preserve all path/name/source fields.
	for _, m := range []*apprelease.FileManifest{&a, &b} {
		for _, section := range []*[]apprelease.FileEntry{&m.Assets, &m.Checksums, &m.SBOMs, &m.Evidence, &m.Provenance} {
			*section = slices.Clone(*section)
			slices.SortFunc(*section, func(x, y apprelease.FileEntry) int { return strings.Compare(x.Path, y.Path) })
			if len(*section) == 0 {
				*section = nil
			}
		}
	}
	return reflect.DeepEqual(a, b)
}

func stageReleaseArchive(body []byte, prefix, root string) error {
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	var remaining int64 = 1 << 30
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			if !cleanZIPEntryName(strings.TrimSuffix(f.Name, "/")) {
				return fmt.Errorf("unsafe archive directory %q", f.Name)
			}
			continue
		}
		if !cleanZIPEntryName(f.Name) || !f.Mode().IsRegular() {
			return fmt.Errorf("unsafe archive entry %q", f.Name)
		}
		name, found := strings.CutPrefix(f.Name, prefix)
		if !found || (name != "digest" && !strings.HasPrefix(name, "dist/")) {
			continue
		}
		b, err := readZIPEntry(f, remaining)
		if err != nil {
			return err
		}
		remaining -= int64(len(b))
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		file, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(b)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// GitLabReleaseArchive retains the public handoff files, not signer credentials,
// through the existing confined evidence writer before provider cleanup.
func GitLabReleaseArchive(tb TB, target Target, address, evidenceName string) []byte {
	tb.Helper()
	body := Download(tb, target, address, 1<<30)
	if root := snapshotEvidenceRoot(); root != "" {
		if err := writeSnapshotEvidence(root, evidenceName, body); err != nil {
			tb.Fatalf("retain native release evidence: %v", err)
		}
	}
	return body
}
