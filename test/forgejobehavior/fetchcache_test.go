// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// fetchBinary is what the fixture forge serves as the layer's binary.
const fetchBinary = "#!/usr/bin/env bash\nprintf 'fixture engine\\n'\n"

// runnerCache stands in for a Forgejo runner's cache service: the lookup,
// download, reserve, upload and commit calls, held in memory.
type runnerCache struct {
	mu       sync.Mutex
	server   *httptest.Server
	entries  map[int][]byte
	complete map[string]int
	reserved map[int]string
	requests int
}

func newRunnerCache(t *testing.T) *runnerCache {
	t.Helper()

	cache := &runnerCache{entries: map[int][]byte{}, complete: map[string]int{}, reserved: map[int]string{}}
	cache.server = httptest.NewServer(http.HandlerFunc(cache.serve))
	t.Cleanup(cache.server.Close)

	return cache
}

// address is the job's ACTIONS_CACHE_URL: the service under a run's own path.
func (c *runnerCache) address() string { return c.server.URL + "/run-1/" }

// hold makes the cache answer a lookup of key with data.
func (c *runnerCache) hold(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := len(c.entries) + 1
	c.entries[id] = data
	c.complete[key] = id
}

// held returns what the cache holds under key, and whether it holds it.
func (c *runnerCache) held(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id, ok := c.complete[key]

	return c.entries[id], ok
}

func (c *runnerCache) asked() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.requests
}

func (c *runnerCache) serve(writer http.ResponseWriter, request *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.requests++

	// A job's token is no business of the cache: the fetch reads no credential.
	if request.Header.Get("Authorization") != "" {
		http.Error(writer, "the fetch sent a credential", http.StatusTeapot)

		return
	}

	path, found := strings.CutPrefix(request.URL.Path, "/run-1/_apis/artifactcache/")
	if !found {
		http.NotFound(writer, request)

		return
	}

	id, _ := strconv.Atoi(path[strings.LastIndex(path, "/")+1:])

	switch {
	case request.Method == http.MethodGet && path == "cache":
		entry, ok := c.complete[request.URL.Query().Get("keys")+"@"+request.URL.Query().Get("version")]
		if !ok {
			writer.WriteHeader(http.StatusNoContent)

			return
		}

		location := c.server.URL + "/run-1/_apis/artifactcache/artifacts/" + strconv.Itoa(entry)
		writeCacheJSON(writer, map[string]string{"result": "hit", "archiveLocation": location})
	case request.Method == http.MethodGet && strings.HasPrefix(path, "artifacts/"):
		_, _ = writer.Write(c.entries[id])
	case request.Method == http.MethodPost && path == "caches":
		var reserve struct {
			Key     string `json:"key"`
			Version string `json:"version"`
		}

		_ = json.NewDecoder(request.Body).Decode(&reserve)
		id = len(c.entries) + 1
		c.entries[id] = nil
		c.reserved[id] = reserve.Key + "@" + reserve.Version
		writeCacheJSON(writer, map[string]int{"cacheId": id})
	case request.Method == http.MethodPatch:
		data, _ := io.ReadAll(request.Body)
		// As the runner does, refuse a range that is not the whole upload.
		if request.Header.Get("Content-Range") != "bytes 0-"+strconv.Itoa(len(data)-1)+"/*" {
			http.Error(writer, "malformed Content-Range", http.StatusBadRequest)

			return
		}

		c.entries[id] = data
	case request.Method == http.MethodPost:
		c.complete[c.reserved[id]] = id
		_, _ = writer.Write([]byte("{}"))
	default:
		http.NotFound(writer, request)
	}
}

// fetchFixture is a job that has the layer and not its binary: the real
// resolver, the recorded checksum of fetchBinary, and a forge that serves the
// binary over HTTPS and counts the downloads.
type fetchFixture struct {
	t         *testing.T
	box       *sandbox
	digest    string
	key       string
	env       []string
	mu        sync.Mutex
	downloads int
}

func newFetchFixture(t *testing.T) *fetchFixture {
	t.Helper()

	sum := sha256.Sum256([]byte(fetchBinary))
	fixture := &fetchFixture{t: t, box: newSandbox(t, "setup-forge-ci"), digest: hex.EncodeToString(sum[:])}
	fixture.key = "forge-ci-linux-" + runtime.GOARCH + "-" + fixture.digest + "@" + fixture.digest
	fixture.box.write(".engine/bin/lib-forge-ci.sh", string(reporoot.ReadFile(t, ".engine/bin/lib-forge-ci.sh")), 0o600)
	fixture.box.write(trackedChecksum(runtime.GOARCH), fixture.digest+"  bin/forge-ci\n", 0o600)

	forge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/packages/owner/generic/forge-ci/"+fixture.digest+"/forge-ci-linux-"+runtime.GOARCH {
			http.NotFound(w, r)

			return
		}

		fixture.mu.Lock()
		fixture.downloads++
		fixture.mu.Unlock()

		_, _ = w.Write([]byte(fetchBinary))
	}))
	t.Cleanup(forge.Close)

	authority := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: forge.Certificate().Raw})
	fixture.env = []string{
		"GITHUB_SERVER_URL=" + forge.URL, "GITHUB_ACTION_REPOSITORY=owner/forge-ci",
		"CURL_CA_BUNDLE=" + fixture.box.write("forge-ca.pem", string(authority), 0o600),
		"ACTIONS_RUNTIME_TOKEN=a-job-token",
	}

	return fixture
}

func (f *fetchFixture) downloaded() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.downloads
}

// fetch runs fetch_forge_ci as a new job would, with an empty temporary
// directory, and returns the step's result and the binary it left there.
func (f *fetchFixture) fetch(env ...string) (result, string) {
	f.t.Helper()

	temporary := f.t.TempDir()
	got := f.box.run("set -euo pipefail\nsource .engine/bin/lib-forge-ci.sh\nfetch_forge_ci\n",
		append(append([]string{"RUNNER_TEMP=" + temporary}, f.env...), env...)...)

	binary, err := os.ReadFile(filepath.Join(temporary, "forge-ci-"+f.digest, "forge-ci"))
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}

	return got, string(binary)
}

// A job takes the layer's binary from the runner's cache when the cache holds
// the recorded bytes, and from the forge otherwise. The first job to download
// offers it to the cache. Nothing the cache does fails a job or changes which
// bytes it runs.
func TestFetchForgeCIUsesTheRunnerCache(t *testing.T) {
	t.Parallel()

	t.Run("the first job downloads and saves, the next downloads nothing", func(t *testing.T) {
		t.Parallel()

		fixture, cache := newFetchFixture(t), newRunnerCache(t)

		got, binary := fixture.fetch("ACTIONS_CACHE_URL=" + cache.address())
		if got.status != 0 || binary != fetchBinary || fixture.downloaded() != 1 {
			t.Fatalf("first job: status %d, %d downloads, binary %q\n%s", got.status, fixture.downloaded(), binary, got.stderr)
		}

		if saved, ok := cache.held(fixture.key); !ok || string(saved) != fetchBinary {
			t.Fatalf("the first job did not save the binary: held %t, %q", ok, saved)
		}

		got, binary = fixture.fetch("ACTIONS_CACHE_URL=" + cache.address())
		if got.status != 0 || binary != fetchBinary || fixture.downloaded() != 1 {
			t.Fatalf("second job: status %d, %d downloads, binary %q\n%s", got.status, fixture.downloaded(), binary, got.stderr)
		}

		if !strings.Contains(got.stderr, "from the runner's cache") {
			t.Fatalf("the second job does not say where the binary came from: %q", got.stderr)
		}
	})

	t.Run("an entry that is not the recorded binary is not used", func(t *testing.T) {
		t.Parallel()

		fixture, cache := newFetchFixture(t), newRunnerCache(t)
		cache.hold(fixture.key, []byte("#!/usr/bin/env bash\nprintf 'another binary\\n'\n"))

		got, binary := fixture.fetch("ACTIONS_CACHE_URL=" + cache.address())
		if got.status != 0 || binary != fetchBinary || fixture.downloaded() != 1 {
			t.Fatalf("status %d, %d downloads, binary %q\n%s", got.status, fixture.downloaded(), binary, got.stderr)
		}
	})

	// Not parallel: a sibling's cache must not take the port this one frees.
	t.Run("a cache that does not answer costs a download, not the job", func(t *testing.T) {
		fixture, cache := newFetchFixture(t), newRunnerCache(t)
		address := cache.address()
		cache.server.Close()

		got, binary := fixture.fetch("ACTIONS_CACHE_URL=" + address)
		if got.status != 0 || binary != fetchBinary || fixture.downloaded() != 1 {
			t.Fatalf("status %d, %d downloads, binary %q\n%s", got.status, fixture.downloaded(), binary, got.stderr)
		}
	})

	t.Run("a cache of the newer service is left alone", func(t *testing.T) {
		t.Parallel()

		fixture, cache := newFetchFixture(t), newRunnerCache(t)

		got, binary := fixture.fetch("ACTIONS_CACHE_URL="+cache.address(), "ACTIONS_CACHE_SERVICE_V2=True")
		if got.status != 0 || binary != fetchBinary || fixture.downloaded() != 1 || cache.asked() != 0 {
			t.Fatalf("status %d, %d downloads, %d cache requests\n%s", got.status, fixture.downloaded(), cache.asked(), got.stderr)
		}
	})
}

func writeCacheJSON(writer http.ResponseWriter, value any) {
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
	}
}
