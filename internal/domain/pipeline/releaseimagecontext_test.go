// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package pipeline_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
	"github.com/stretchr/testify/require"
)

func TestReleaseImageContextAdmission(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name, artifact, digest string
		valid                  bool
	}{
		{"checkout", "", "", true},
		{"producer pair", "release-context", digest, true},
		{"missing digest", "release-context", "", false},
		{"missing artifact", "", digest, false},
		{"invalid name", "context\nname", digest, false},
		{"short digest", "context", "ab", false},
		{"uppercase", "context", strings.ToUpper(digest), false},
		{"nonhex", "context", strings.Repeat("z", 64), false},
		{"prefixed", "context", "sha256:" + digest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := pipeline.ValidateReleaseImageContext(tc.artifact, tc.digest)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestFetchMatrixIncludesOnlyRegistryPreparation(t *testing.T) {
	t.Parallel()

	checkout := pipeline.ReleaseImageBuild{Name: "checkout", Arch: "amd64"}
	cache := pipeline.ReleaseImageBuild{Name: "cached", Arch: "arm64", CacheTag: "cache"}
	parent := pipeline.ReleaseImageBuild{Name: "child", Arch: "amd64", RegistryInputs: map[string]string{"BASE": "parent"}}

	both := pipeline.ReleaseImageBuild{Name: "both", Arch: "arm64", CacheTag: "cache", RegistryInputs: parent.RegistryInputs}
	for _, tc := range []struct {
		name   string
		builds []pipeline.ReleaseImageBuild
		want   string
	}{
		{"empty", nil, `{"include":[]}`},
		{"checkout only", []pipeline.ReleaseImageBuild{checkout}, `{"include":[]}`},
		{"mixed", []pipeline.ReleaseImageBuild{checkout, cache, parent}, `{"include":[{"name":"cached","arch":"arm64"},{"name":"child","arch":"amd64"}]}`},
		{"all required", []pipeline.ReleaseImageBuild{parent, both}, `{"include":[{"name":"child","arch":"amd64"},{"name":"both","arch":"arm64"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := pipeline.ReleaseImagesPlan{Builds: tc.builds}
			body, err := json.Marshal(plan.FetchMatrix())
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(body))
			require.Len(t, plan.BuildMatrix().Include, len(tc.builds))
		})
	}
}
