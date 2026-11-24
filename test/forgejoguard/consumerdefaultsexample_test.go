// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package forgejoguard

import (
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/consumerinit"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// A mirror copies the defaults example to .forge-ci/consumer-defaults.yml;
// init parses that file strictly, so the example must parse the same way and
// carry the placeholders init's Forgejo substitutions name.
func TestConsumerDefaultsExampleParses(t *testing.T) {
	t.Parallel()

	defaults, err := consumerinit.ParseDefaults(reporoot.ReadFile(t, "examples/forgejo/.forge-ci/consumer-defaults.yml.example"))
	if err != nil {
		t.Fatal(err)
	}

	if defaults.Forge != "forgejo" {
		t.Errorf("forge = %q, want forgejo", defaults.Forge)
	}

	if !strings.HasSuffix(defaults.SignerImage, "/forge-ci-signer@sha256:PIN_SIGNER_IMAGE_DIGEST") {
		t.Errorf("signer-image = %q, want the self-release example's signer placeholder", defaults.SignerImage)
	}

	if defaults.CosignPublicKeySHA256 != "PIN_COSIGN_PUBLIC_KEY_SHA256" {
		t.Errorf("cosign-public-key-sha256 = %q, want its placeholder", defaults.CosignPublicKeySHA256)
	}
}
