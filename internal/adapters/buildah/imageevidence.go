// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package buildah

import (
	"context"
	"fmt"
	"io"
)

// ExportLocalImageToLayout exports an image from buildah's local storage to an OCI layout.
func (a *Adapter) ExportLocalImageToLayout(ctx context.Context, imageRef, layoutDir string, out io.Writer) error {
	return a.run(ctx, out, a.global("push", imageRef, "oci:"+layoutDir+":scan")...)
}

// ExportManifestToLayout includes all platforms; remote children, if needed,
// use the same auth file and TLS policy as the subsequent registry publication.
func (a *Adapter) ExportManifestToLayout(ctx context.Context, authFile string, tlsVerify bool, manifest, layoutDir string, out io.Writer) error {
	args := append(a.global("manifest", "push", "--all"), exportAuthArgs(authFile, tlsVerify)...)
	args = append(args, manifest, "oci:"+layoutDir+":scan")

	return a.runWithEnv(ctx, out, registryAuthEnv(authFile), args...)
}

func exportAuthArgs(authFile string, tlsVerify bool) []string {
	args := []string{fmt.Sprintf("--tls-verify=%t", tlsVerify)}
	if authFile != "" {
		args = append(args, "--authfile", authFile)
	}

	return args
}
