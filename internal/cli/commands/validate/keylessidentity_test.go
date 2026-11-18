// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/cli"
	validatecmd "github.com/diggsweden/forge-ci/internal/cli/commands/validate"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/testutil/mockbinary"
	"github.com/diggsweden/forge-ci/internal/testutil/testenv"
)

// Both signature validators hand cosign the repository whose GitHub run must
// have signed: by default the running repository, or the one the operator
// names. A certificate names the workflow file that ran, so for a release
// that a reusable workflow signed, the repository claim is the only pin on
// whose run it was. Where GitHub's issuer is trusted and no repository is
// named or derivable, as off a GitHub runner, verification is refused as a
// usage error before cosign runs.
func TestSignatureValidators_PinTheGitHubWorkflowRepository(t *testing.T) {
	image := "ghcr.io/acme/app@sha256:" + strings.Repeat("0", 64)
	offRunner := []string{"--cert-identity-regexp", `^https://github\.com/`, "--cert-oidc-issuer", "https://token.actions.githubusercontent.com"}

	for name, tc := range map[string]struct {
		argv       []string
		local      bool
		repository string
	}{
		"artifact default":    {argv: []string{"validate", "artifact-signature", "--method", "sigstore"}, repository: "acme/app"},
		"artifact explicit":   {argv: []string{"validate", "artifact-signature", "--method", "sigstore", "--cert-github-workflow-repository", "other/app"}, repository: "other/app"},
		"artifact off runner": {argv: append([]string{"validate", "artifact-signature", "--method", "sigstore"}, offRunner...), local: true},
		"image default":       {argv: []string{"validate", "container-signature", image, "--method", "sigstore"}, repository: "acme/app"},
		"image explicit":      {argv: []string{"validate", "container-signature", image, "--method", "sigstore", "--cert-github-workflow-repository", "other/app"}, repository: "other/app"},
		"image off runner":    {argv: append([]string{"validate", "container-signature", image, "--method", "sigstore"}, offRunner...), local: true},
	} {
		t.Run(name, func(t *testing.T) {
			testenv.New(t)

			if !tc.local {
				t.Setenv("GITHUB_ACTIONS", "true")
				t.Setenv("GITHUB_REPOSITORY", "acme/app")
				t.Setenv("GITHUB_SERVER_URL", "https://github.com")
			}

			bins := mockbinary.New(t)
			bins.Add("cosign", ":")
			bins.OnPath()

			argv := tc.argv
			if argv[1] == "artifact-signature" {
				artifact := filepath.Join(t.TempDir(), "app.tgz")
				for _, path := range []string{artifact, artifact + ".bundle"} {
					if err := os.WriteFile(path, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}

				argv = append(slices.Clone(argv), "--artifact", artifact)
			}

			err := validatecmd.New().Run(context.Background(), argv)
			calls := bins.Invocations("cosign")

			if tc.repository == "" {
				if got := errs.ExitCodeFromError(cli.ClassifyError(err)); got != errs.ExitCodeUsage || len(calls) != 0 ||
					!strings.Contains(fmt.Sprint(err), "--cert-github-workflow-repository") {
					t.Fatalf("err = %v (exit %d), cosign runs = %d; want a usage refusal naming the flag before cosign runs", err, got, len(calls))
				}

				return
			}

			if err != nil {
				t.Fatalf("%v: %v", argv, err)
			}

			if len(calls) != 1 {
				t.Fatalf("cosign ran %d times, want once", len(calls))
			}

			got := calls[0].Args

			at := slices.Index(got, "--certificate-github-workflow-repository")
			if at < 0 || at+1 >= len(got) || got[at+1] != tc.repository {
				t.Errorf("cosign argv = %q, want --certificate-github-workflow-repository %s", got, tc.repository)
			}
		})
	}
}
