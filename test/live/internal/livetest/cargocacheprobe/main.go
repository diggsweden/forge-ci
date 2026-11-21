// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

// cargocacheprobe seeds deliberately invalid entries or competing saves in a
// disposable runner cache. It is a live-test helper, never a product command.
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/diggsweden/forge-ci/internal/adapters/actionscache"
	"github.com/diggsweden/forge-ci/internal/adapters/cargocache"
)

var (
	errProbeUsage    = errors.New("expected corrupt or concurrent")
	errProbeRestore  = errors.New("seed helper cannot restore")
	errCompetingSave = errors.New("competing save failed")
	errSaveOutcome   = errors.New("expected completed uploads or a reservation conflict")
	errMissingCrate  = errors.New("missing crate")
)

func main() {
	if err := run(); err != nil {
		// Never print transport data or runner credentials.
		fmt.Fprintln(os.Stderr, "Cargo cache probe failed")
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 || (os.Args[1] != "corrupt" && os.Args[1] != "concurrent") {
		return errProbeUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	transport := probeTransport{mode: os.Args[1], client: actionscache.Client{URL: os.Getenv("ACTIONS_CACHE_URL"), Token: os.Getenv("ACTIONS_RUNTIME_TOKEN")}}

	cache, err := cargocache.New("Cargo.lock", os.Getenv("CARGO_HOME"), transport)
	if err != nil {
		return err
	}

	return cache.Save(ctx)
}

type probeTransport struct {
	mode   string
	client actionscache.Client
}

func (p probeTransport) Restore(context.Context, string, string) (bool, error) {
	return false, errProbeRestore
}

func (p probeTransport) Save(ctx context.Context, key, source string) error {
	if p.mode == "corrupt" {
		if err := corruptArchive(source); err != nil {
			return err
		}

		if err := p.client.Save(ctx, key, source); err != nil {
			return err
		}

		fmt.Println("Cargo cache probe: corrupt entry seeded")

		return nil
	}

	var (
		logs        [2]bytes.Buffer
		results     [2]error
		ready, done sync.WaitGroup
	)
	ready.Add(2)
	done.Add(2)

	start := make(chan struct{})

	for i := range 2 {
		go func() {
			defer done.Done()

			ready.Done()
			<-start

			client := p.client
			client.Log = &logs[i]
			results[i] = client.Save(ctx, key, source)
		}()
	}

	ready.Wait()
	close(start)
	done.Wait()

	log := logs[0].String() + logs[1].String()
	fmt.Print(log)

	if results[0] != nil || results[1] != nil {
		fmt.Printf("Cargo cache probe: save results: %v; %v\n", results[0], results[1])

		return errCompetingSave
	}
	// Act-derived caches can reserve both entries; other v1 services return a
	// conflict to one caller. Both must finish cleanly, with at least one upload.
	// The workflow then restores through the product into a fresh Cargo home.
	uploads := strings.Count(log, "cache uploaded:")

	conflicts := strings.Count(log, "key already reserved")
	if uploads < 1 || uploads+conflicts != 2 {
		return errSaveOutcome
	}

	fmt.Println("Cargo cache probe: competing saves verified")

	return nil
}

func corruptArchive(source string) error {
	data, err := os.ReadFile(source) //nolint:gosec // source is the archive the cache just wrote for this disposable helper.
	if err != nil {
		return err
	}

	reader := tar.NewReader(bytes.NewReader(data))

	header, err := reader.Next()
	if err != nil {
		return err
	}

	crate, err := io.ReadAll(reader)
	if err != nil || len(crate) == 0 {
		return errMissingCrate
	}

	crate[0] ^= 0xff

	var output bytes.Buffer

	writer := tar.NewWriter(&output)
	if err := writer.WriteHeader(header); err != nil {
		return err
	}

	if _, err := writer.Write(crate); err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	return os.WriteFile(source, output.Bytes(), 0o600)
}
