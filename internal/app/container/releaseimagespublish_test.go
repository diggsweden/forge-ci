// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container_test

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/adapters/ociregistry"
	appcontainer "github.com/diggsweden/forge-ci/internal/app/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/imageledger"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
)

type releaseImagesPublishFixture struct {
	in     appcontainer.PublishReleaseImagesInput
	writes *atomic.Int32
	gated  *[]string
	digest map[string]string // <name>-<arch> → staged digest
}

// releaseImagesPublishFixture stages what the build jobs leave for the
// publish job: app for amd64 and arm64, worker for amd64, in a layouts
// directory apart from the job's working directory.
func newReleaseImagesPublishFixture(t *testing.T) releaseImagesPublishFixture {
	t.Helper()
	t.Chdir(t.TempDir())

	var writes atomic.Int32

	handler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writes.Add(1)
		}

		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	host := strings.TrimPrefix(server.URL, "http://")
	layouts := filepath.Join(t.TempDir(), "layouts")
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"auths":{}}`), 0o600))

	plan := pipeline.ReleaseImagesPlan{Images: []pipeline.ReleaseImage{
		{Name: "app", ImageRepository: host + "/org/app", CandidateTag: "staging-v1.2.3", FinalTagName: "v1.2.3", Archs: "amd64\narm64", Scan: true},
		{Name: "worker", ImageRepository: host + "/org/worker", CandidateTag: "staging-v1.2.3", FinalTagName: "v1.2.3", Archs: "amd64", Scan: true},
	}}

	digests := map[string]string{}

	for _, image := range plan.Images {
		for arch := range strings.FieldsSeq(image.Archs) {
			dir := filepath.Join(layouts, image.Name+"-arch-"+arch)
			tag := image.ImageRepository + ":staging-v1.2.3-" + arch
			_, staged := stagedLayout(t, t.Context(), filepath.Join(dir, "layout"), tag, arch)
			digest, err := staged.Digest()
			require.NoError(t, err)

			body, err := json.Marshal(appcontainer.SignerImageArchMetadata{Arch: arch, Tag: tag, Digest: digest.String(), Ref: image.ImageRepository + "@" + digest.String()})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, image.Name+"-"+arch+".json"), body, 0o600))

			digests[image.Name+"-"+arch] = digest.String()
		}
	}

	gated := []string{}

	return releaseImagesPublishFixture{
		in: appcontainer.PublishReleaseImagesInput{
			Plan: plan, LayoutsDir: layouts, OutputDir: filepath.Join(t.TempDir(), "images"), AuthFile: auth,
			ReleaseTag: "v1.2.3", ReleaseSHA: strings.Repeat("b", 40), ServerURL: server.URL, Repository: "org/app", RetryAttempts: 1,
			Trivy: &fakeImageEvidenceTrivy{},
			Gate: func(_ context.Context, _, platform string) error {
				gated = append(gated, platform)

				return nil
			},
		},
		writes: &writes,
		gated:  &gated,
		digest: digests,
	}
}

func (f releaseImagesPublishFixture) publish(t *testing.T) error {
	t.Helper()

	return appcontainer.PublishReleaseImages(t.Context(), ociregistry.WithAuthFile(f.in.AuthFile), nil, io.Discard, f.in)
}

// Every planned image is scanned, gated and pushed, and the one ledger the
// job writes records each of them by the digest of the index it pushed.
func TestPublishReleaseImagesRecordsEveryImageInOneLedger(t *testing.T) {
	fixture := newReleaseImagesPublishFixture(t)

	require.NoError(t, fixture.publish(t))
	require.Equal(t, []string{"linux/amd64", "linux/arm64", "linux/amd64"}, *fixture.gated)

	for _, name := range []string{"trivy-results-app-amd64.json", "trivy-results-app-arm64.json", "trivy-results-worker-amd64.json", "index-app.json", "index-worker.json"} {
		require.FileExists(t, filepath.Join(fixture.in.OutputDir, name))
	}

	body, err := os.ReadFile(filepath.Join(fixture.in.OutputDir, appcontainer.ReleaseImagesLedgerName))
	require.NoError(t, err)
	entries, err := imageledger.Parse(body)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	for i, image := range fixture.in.Plan.Images {
		entry := entries[i]
		pushed, err := ociregistry.NewAnonymous().ResolveDigest(t.Context(), image.ImageRepository+":staging-v1.2.3")
		require.NoError(t, err)
		require.Equal(t, image.Name, entry.Role)
		require.Equal(t, imageledger.ImageKindRelease, entry.ImageKind)
		require.Equal(t, pushed, entry.Digest)
		require.Equal(t, image.ImageRepository+"@"+pushed, entry.Ref)
		require.Equal(t, image.ImageRepository+":v1.2.3", entry.FinalTag)
		require.Equal(t, image.ImageRepository+":staging-v1.2.3", entry.CandidateTag)
	}

	// The job's working directory keeps nothing of the run.
	left, err := os.ReadDir(".")
	require.NoError(t, err)
	require.Empty(t, left)
}

// The gate decides before the first upload: a blocked layout publishes
// nothing and leaves no ledger.
func TestPublishReleaseImagesGatesBeforeAnyUpload(t *testing.T) {
	fixture := newReleaseImagesPublishFixture(t)
	fixture.in.Gate = func(context.Context, string, string) error { return errs.ErrValidation }

	require.ErrorIs(t, fixture.publish(t), errs.ErrValidation)
	require.Zero(t, fixture.writes.Load())
	require.NoFileExists(t, filepath.Join(fixture.in.OutputDir, appcontainer.ReleaseImagesLedgerName))
}

// The layouts come from jobs that ran consumer code. Anything but the planned
// layouts is refused before a scan or an upload: a ledger planted to be
// appended to, an extra file, a link, a missing layout.
func TestPublishReleaseImagesAdmitsOnlyThePlannedLayouts(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, layouts string){
		"pre-seeded ledger": func(t *testing.T, layouts string) {
			t.Helper()
			require.NoError(t, os.MkdirAll(filepath.Join(layouts, "app-ledger"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(layouts, "app-ledger", "release-images.json"), []byte(`{"version":1,"images":[]}`), 0o600))
		},
		"file inside a layout directory": func(t *testing.T, layouts string) {
			t.Helper()
			require.NoError(t, os.WriteFile(filepath.Join(layouts, "app-arch-amd64", "release-images.json"), nil, 0o600))
		},
		"linked layout directory": func(t *testing.T, layouts string) {
			t.Helper()
			moved := filepath.Join(t.TempDir(), "elsewhere")
			require.NoError(t, os.Rename(filepath.Join(layouts, "worker-arch-amd64"), moved))
			require.NoError(t, os.Symlink(moved, filepath.Join(layouts, "worker-arch-amd64")))
		},
		"linked metadata file": func(t *testing.T, layouts string) {
			t.Helper()

			file := filepath.Join(layouts, "app-arch-arm64", "app-arm64.json")
			moved := filepath.Join(t.TempDir(), "app-arm64.json")
			require.NoError(t, os.Rename(file, moved))
			require.NoError(t, os.Symlink(moved, file))
		},
		"missing layout": func(t *testing.T, layouts string) {
			t.Helper()
			require.NoError(t, os.RemoveAll(filepath.Join(layouts, "app-arch-arm64")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newReleaseImagesPublishFixture(t)
			plant(t, fixture.in.LayoutsDir)

			require.ErrorIs(t, fixture.publish(t), errs.ErrValidation)
			require.Empty(t, *fixture.gated)
			require.Zero(t, fixture.writes.Load())
			require.NoDirExists(t, fixture.in.OutputDir)
		})
	}
}
