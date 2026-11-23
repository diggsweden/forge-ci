// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package gitlabbehavior

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// Execute the templates' literal readers with artifact-controlled values.
// Command substitutions, spaces and equals signs must survive as data, and
// unrelated output names must never enter the job environment.
func TestPublishOutputsAreDataAndOnlyKnownNamesAreImported(t *testing.T) {
	t.Parallel()

	for _, file := range []string{"publish-apple-appstore.yml", "publish-forge-packages.yml", "publish-maven-central.yml"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			output := filepath.Join(dir, "output.env")

			const payload = "artifact with spaces $(touch EXPLOITED) `touch EXPLOITED` =value= .ipa"

			contents := "IPA_FILE=" + payload + "\nREQUEST_ID=" + payload + "\nVERSION=" + payload + "\nFINGERPRINT=" + payload + "\nIS_SNAPSHOT=false\nUNRELATED_OUTPUT=must-not-be-exported\n"
			if err := os.WriteFile(output, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			scripts := publisherScripts(t, file)

			var readers strings.Builder

			for _, script := range scripts {
				if strings.Contains(script, "while IFS= read -r line") {
					readers.WriteString(script + "\n")
				}
			}

			if readers.Len() == 0 {
				t.Fatal("the component has no literal output reader")
			}

			readers.WriteString("test -z \"${UNRELATED_OUTPUT:-}\"\n")

			if file == "publish-maven-central.yml" {
				readers.WriteString("test \"$GPG_FINGERPRINT\" = \"$EXPECTED\"\n")
			}

			if file == "publish-apple-appstore.yml" {
				readers.WriteString("test \"$IPA_FILE\" = \"$EXPECTED\" && test \"$REQUEST_ID\" = \"$EXPECTED\"\n")
			} else {
				readers.WriteString("test \"$VERSION\" = \"$EXPECTED\" && test \"$IS_SNAPSHOT\" = false\n")
			}

			ctx := commandContext(t)

			command := exec.CommandContext(ctx, componentBash(t), "--noprofile", "--norc", "-eu", "-c", readers.String())
			command.Dir = dir

			command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "CI_OUTPUT=" + output, "EXPECTED=" + payload}
			if result, err := command.CombinedOutput(); err != nil {
				t.Fatalf("output values did not survive literally: %v\n%s", err, result)
			}

			if _, err := os.Stat(filepath.Join(dir, "EXPLOITED")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("output data executed a shell command")
			}

			// The job creates its output file away from artifact extraction, so
			// old artifact contents cannot be appended to and then imported.
			initScript := scripts[0] + "\ntest \"$CI_OUTPUT\" != \"$OLD_OUTPUT\" && test ! -s \"$CI_OUTPUT\"\n"
			init := exec.CommandContext(ctx, componentBash(t), "--noprofile", "--norc", "-eu", "-c", initScript)
			init.Dir = dir

			init.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir, "CI_OUTPUT=" + output, "OLD_OUTPUT=" + output}
			if result, err := init.CombinedOutput(); err != nil {
				t.Fatalf("output initialization retained artifact data: %v\n%s", err, result)
			}
		})
	}
}

func publisherScripts(t *testing.T, file string) []string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(reporoot.Path(t), "templates", file))
	if err != nil {
		t.Fatal(err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(body))

	var header any
	if err := decoder.Decode(&header); err != nil {
		t.Fatal(err)
	}

	var jobs map[string]struct {
		Script []string `yaml:"script"`
	}
	if err := decoder.Decode(&jobs); err != nil {
		t.Fatal(err)
	}

	return jobs["$[[ inputs.job-name ]]"].Script
}

func TestSignedGradlePublisherValidatesHandoffBeforeDeploy(t *testing.T) {
	t.Parallel()

	var body string

	for _, script := range publisherScripts(t, "publish-forge-packages.yml") {
		if strings.Contains(script, `if [ -n "$RC_RELEASE_MANIFEST" ]`) {
			body = script
		}
	}

	if body == "" {
		t.Fatal("signed-release deploy step missing")
	}

	for _, mode := range []string{"valid", "changed digest", "missing digest"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			stub := `forge-ci() {
  if [ "$1 $2" = 'release validate-dist' ]; then
    printf 'validated\n'
    [ "$FAIL_VALIDATE" != true ]
  elif [ "$1 $2 $3" = 'publish forge-packages deploy' ]; then
    printf 'deployed\n'
    [ "$4" = --project-type ] && [ "$5" = gradle-android ] &&
      [ "$6" = --working-dir ] && [ "$7" = 'dist/library with spaces' ] &&
      [ "$8" = --release-manifest ] && [ "$9" = dist/release-files.json ]
  else
    printf 'unexpected project/toolchain command\n' >&2
    return 79
  fi
}
`

			digest, fail := "signer-digest", "false"
			if mode == "changed digest" {
				fail = "true"
			}

			if mode == "missing digest" {
				digest = ""
			}

			command := exec.CommandContext(commandContext(t), componentBash(t), "--noprofile", "--norc", "-eu", "-c", stub+body)
			command.Env = []string{"PATH=/usr/bin:/bin", "PACKAGE_TYPE=gradle-android", "RC_PROJECT_DIRECTORY=dist/library with spaces", "RC_RELEASE_MANIFEST=dist/release-files.json", "RC_RELEASE_DIST=dist", "RC_RELEASE_DIGEST=" + digest, "FAIL_VALIDATE=" + fail}

			out, err := command.CombinedOutput()
			if mode == "valid" {
				if err != nil || string(out) != "validated\ndeployed\n" {
					t.Fatalf("valid handoff: %v\n%s", err, out)
				}
			} else if err == nil || strings.Contains(string(out), "deployed") {
				t.Fatalf("invalid handoff reached deploy: %v\n%s", err, out)
			}
		})
	}
}
