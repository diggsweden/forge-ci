// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/diggsweden/forge-ci/internal/domain/ci"
	domaincontainer "github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/retry"
)

// PushedManifestDigestInput drives `container manifest digest`.
type PushedManifestDigestInput struct {
	Ref           string
	DigestFile    string
	RetryAttempts int
	RetryDelay    time.Duration
}

// PushedManifestDigestOutput is the verified registry digest and digest-pinned ref.
type PushedManifestDigestOutput struct {
	Digest string
	Ref    string
}

// ResolvePushedManifestDigest resolves the registry-served raw manifest digest
// for a ref and, when a digest file is provided, verifies it matches the
// registry. This is the generic digestfile-vs-registry check used after Buildah
// image or manifest pushes.
func ResolvePushedManifestDigest(ctx context.Context, registry RawManifestRegistry, sink ci.OutputSink, out io.Writer, in PushedManifestDigestInput) (*PushedManifestDigestOutput, error) {
	if err := validatePushedManifestDigestInput(registry, in); err != nil {
		return nil, err
	}

	expectedDigest := ""

	if in.DigestFile != "" {
		// A named file that cannot be read, or holds no digest, is no reason
		// to skip the check it was named for.
		body, err := os.ReadFile(in.DigestFile)
		if err != nil {
			return nil, fmt.Errorf("read the pushed manifest digest file: %w: %w", err, errs.ErrMissingInput)
		}

		expectedDigest = strings.TrimSpace(string(body))
		if !domaincontainer.ValidDigest(expectedDigest) {
			return nil, fmt.Errorf("the pushed manifest digest file %s holds no digest: %w", in.DigestFile, errs.ErrMalformedInput)
		}
	}

	digest, err := resolveVerifiedManifestDigest(ctx, registry, out, in.Ref, expectedDigest, in.RetryAttempts, in.RetryDelay)
	if err != nil {
		return nil, err
	}

	result := &PushedManifestDigestOutput{Digest: digest, Ref: domaincontainer.StripDigest(in.Ref) + "@" + digest}
	if sink != nil {
		if err := sink.Set(ctx, outputKeyDigest, result.Digest); err != nil {
			return nil, err
		}

		if err := sink.Set(ctx, "ref", result.Ref); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func validatePushedManifestDigestInput(registry RawManifestRegistry, in PushedManifestDigestInput) error {
	if registry == nil {
		return fmt.Errorf("container manifest digest: registry adapter is required: %w", errs.ErrUsage)
	}

	if strings.TrimSpace(in.Ref) == "" || strings.ContainsAny(in.Ref, " \t\n\r") {
		return fmt.Errorf("container manifest digest: ref is empty or unsafe: %w", errs.ErrUsage)
	}

	return nil
}

// A publisher's content digest is mandatory. The optional legacy digest-file
// fallback belongs only to the standalone manifest-digest command.
func verifyPublishedLayout(ctx context.Context, registry RawManifestRegistry, out io.Writer, ref, expected string, attempts int, delay time.Duration) (string, error) {
	if !domaincontainer.ValidDigest(expected) {
		return "", fmt.Errorf("layout publisher returned an invalid digest: %w", errs.ErrInvalidConfig)
	}

	return resolveVerifiedManifestDigest(ctx, registry, out, ref, expected, attempts, delay)
}

// An empty expectedDigest means the caller named none to compare with.
func resolveVerifiedManifestDigest(ctx context.Context, registry RawManifestRegistry, out io.Writer, ref, expectedDigest string, retryAttempts int, retryDelay time.Duration) (string, error) {
	// Retries cover a registry that has not yet served a just-pushed
	// manifest. A refused credential does not change between attempts, so it
	// stops at once instead of waiting out every retry.
	registryDigest, err := retry.Do(ctx, out, retry.Attempts(retryAttempts, defaultBuildPushRetryAttempts), retry.Delay(retryDelay, defaultBuildPushRetryDelay), func() (string, error) {
		digest, err := registryManifestDigest(ctx, registry, ref)
		if errors.Is(err, errs.ErrPermissionDenied) {
			return "", retry.Permanent(err)
		}

		return digest, err
	})
	if err != nil {
		return "", err
	}

	registryDigest = strings.TrimSpace(registryDigest)
	if !domaincontainer.ValidDigest(registryDigest) {
		return "", fmt.Errorf("registry returned an invalid manifest digest for %s: %q: %w", ref, registryDigest, errs.ErrInvalidConfig)
	}

	if expectedDigest != "" && expectedDigest != registryDigest {
		return "", fmt.Errorf("pushed manifest digest file does not match registry digest for %s\n  digest file: %s\n  registry:    %s: %w", ref, expectedDigest, registryDigest, errs.ErrValidation)
	}

	return registryDigest, nil
}

func registryManifestDigest(ctx context.Context, registry RawManifestRegistry, ref string) (string, error) {
	raw, err := registry.Manifest(ctx, ref)
	if err != nil {
		return "", err
	}

	if len(raw) == 0 {
		return "", fmt.Errorf("registry returned an empty manifest for %s: %w", ref, errs.ErrDependencyUnavailable)
	}

	sum := sha256.Sum256(raw)

	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
