// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package consumerinit_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appinit "github.com/diggsweden/forge-ci/internal/app/consumerinit"
)

type recordingTagCheckout struct {
	fakeCheckout

	resolved string
}

func (r *recordingTagCheckout) Commit(ctx context.Context, ref string) (string, error) {
	r.resolved = ref

	return r.fakeCheckout.Commit(ctx, ref)
}

func TestRunReleaseTagSelectionPreservesExactRef(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, ref, want string
		tags            []string
	}{
		{name: "catalog", tags: []string{"0.90.0"}, want: "0.90.0"},
		{name: "mixed", tags: []string{"v0.90.0", "0.91.0"}, want: "0.91.0"},
		{name: "tie", tags: []string{"0.90.0", "v0.90.0"}, want: "v0.90.0"},
		{name: "explicit_catalog", ref: "0.90.0", tags: []string{"v0.91.0"}, want: "0.90.0"},
		{name: "explicit_prefixed", ref: "v0.90.0", tags: []string{"0.91.0"}, want: "v0.90.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			remote := &recordingTagCheckout{tags: append(tc.tags, tc.ref), defaults: "forge: gitlab\n"}
			dir := t.TempDir()

			result, err := appinit.Run(t.Context(), appinit.Input{Checkout: remote, URL: "https://gitlab.example.com/team/forge-ci", Ref: tc.ref, Dir: dir, Example: "go-cli"})
			if err != nil {
				t.Fatal(err)
			}

			if result.Source.Tag != tc.want || remote.resolved != tc.want {
				t.Fatalf("tag spelling changed: source=%q resolved=%q want=%q", result.Source.Tag, remote.resolved, tc.want)
			}

			body, err := os.ReadFile(filepath.Join(dir, ".gitlab-ci.yml"))
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(body), "@"+strings.TrimPrefix(tc.want, "v")) {
				t.Fatal("generated Catalog component does not use the selected version")
			}
		})
	}
}
