// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// MergeAuth adds (or replaces) the credential for registry in a docker/OCI auth
// config document — the {"auths": {"<registry>": {"auth": "<b64>"}}} format read
// by docker, podman/buildah, skopeo, and cosign alike, so one written file
// authenticates every tool on both ghcr and Forgejo. existing may be
// empty (a fresh config) or a prior config, whose other registries and fields
// are preserved, including exact numeric values. Non-object roots are rejected;
// a missing or non-object auths/target entry is replaced with an object.
//
// The credential is stored as base64(username:password) — the standard,
// tool-neutral encoding. Note this is encoding, NOT encryption: the file is
// only as protected as its permissions, so callers must write it 0600.
func MergeAuth(existing []byte, registry, username, password string) ([]byte, error) {
	if registry == "" || username == "" || password == "" {
		return nil, fmt.Errorf("registry, username, and password are all required: %w", errs.ErrUsage)
	}

	root, err := parseAuthConfig(existing)
	if err != nil {
		return nil, err
	}

	auths, _ := root["auths"].(map[string]any)
	if auths == nil {
		auths = map[string]any{}
	}

	entry, _ := auths[registry].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}

	// Docker-compatible clients prefer identitytoken over auth. Retaining a
	// token from an earlier login would silently ignore the new credential.
	delete(entry, "identitytoken")
	entry["auth"] = base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	auths[registry] = entry
	root["auths"] = auths

	return json.MarshalIndent(root, "", "  ")
}

func parseAuthConfig(existing []byte) (map[string]any, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(existing)) == 0 {
		return root, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(existing))
	decoder.UseNumber()
	// Unlike Unmarshal, Decode accepts trailing documents; require only JSON whitespace.
	if err := decoder.Decode(&root); err != nil || root == nil || len(bytes.Trim(existing[decoder.InputOffset():], " \t\r\n")) != 0 {
		return nil, fmt.Errorf("parse existing auth config: %w", errs.ErrMalformedInput)
	}

	return root, nil
}
