// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build linux

package forgejobehavior

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCompanionUsesFreshBinaryFromTheExactPRCheckout(t *testing.T) {
	t.Parallel()

	file := ".github/workflows/self-pullrequest.yml"
	build := jobStep(t, file, "integration-testsuite", "Build exact PR candidate")

	test := jobStep(t, file, "integration-testsuite", "Run the black-box suite against this checkout")
	if build.Env["EXPECTED_SOURCE_SHA"] != "${{ github.sha }}" ||
		test.Env["FORGE_CI_TEST_BINARY"] != "${{ runner.temp }}/forge-ci-candidate" {
		t.Fatal("companion must use the fresh binary built from the PR's exact source SHA")
	}

	for _, matching := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching source", false: "wrong source"}[matching], func(t *testing.T) {
			t.Parallel()
			box := newSandbox(t, "candidate-build")
			box.write("bin/git", "#!/bin/sh\nset -eu\ntest \"$*\" = 'rev-parse HEAD'\nprintf '%s\\n' \"$ACTUAL_SOURCE_SHA\"\n", 0o700)
			box.write("bin/go", fakeEngine, 0o700)

			sha := strings.Repeat("a", 40)

			actual := sha
			if !matching {
				actual = strings.Repeat("b", 40)
			}

			result := box.run(build.Run,
				"PATH="+filepath.Join(box.root, "bin")+":"+os.Getenv("PATH"),
				"RUNNER_TEMP="+box.root, "EXPECTED_SOURCE_SHA="+sha, "ACTUAL_SOURCE_SHA="+actual)
			if !matching {
				if result.status == 0 || len(box.argv()) != 0 {
					t.Fatal("source mismatch must refuse before compiling any candidate")
				}

				return
			}

			want := []string{"build", "-trimpath", "-buildvcs=false", "-ldflags", "-X main.commit=" + sha,
				"-o", filepath.Join(box.root, "forge-ci-candidate"), "./cmd/forge-ci"}
			if result.status != 0 || !slices.Equal(box.argv(), want) {
				t.Fatalf("fresh candidate build: %v: %s", box.argv(), result.stderr)
			}
		})
	}
}
