// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package build_test

import (
	"context"
	"errors"
	"io"
	"testing"

	appbuild "github.com/diggsweden/forge-ci/internal/app/build"
	"github.com/stretchr/testify/require"
)

type downloadCache struct {
	hit    bool
	err    error
	events *[]string
}

func (c downloadCache) Restore(context.Context) (bool, error) {
	*c.events = append(*c.events, "restore")

	return c.hit, c.err
}
func (c downloadCache) Save(context.Context) error {
	*c.events = append(*c.events, "save")

	return c.err
}

type cacheFetchTool struct {
	events *[]string
	err    error
}

func (c cacheFetchTool) Run(_ context.Context, in appbuild.CargoRunInput) error {
	*c.events = append(*c.events, "fetch")

	if len(in.Args) != 2 || in.Args[0] != "fetch" || in.Args[1] != "--locked" {
		return errors.New("fetch is not locked")
	}

	return c.err
}

func TestCargoCacheAlwaysUsesLockedFetch(t *testing.T) {
	for _, tc := range []struct {
		name               string
		hit                bool
		cacheErr, fetchErr error
		want               []string
	}{
		{"cold", false, nil, nil, []string{"restore", "fetch", "save"}},
		{"warm", true, nil, nil, []string{"restore", "fetch"}},
		{"unavailable", false, errors.New("unavailable"), nil, []string{"restore", "fetch", "save"}},
		{"fetch_failure", false, nil, errors.New("locked fetch failed"), []string{"restore", "fetch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []string

			err := appbuild.CargoFetchWithRetry(t.Context(), cacheFetchTool{&events, tc.fetchErr}, io.Discard, io.Discard, appbuild.CargoFetchInput{Attempts: 1, Cache: downloadCache{tc.hit, tc.cacheErr, &events}})
			require.ErrorIs(t, err, tc.fetchErr)
			require.Equal(t, tc.want, events)
		})
	}
}
