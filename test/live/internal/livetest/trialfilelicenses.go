// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import "strings"

// Metadata travels with injected files, rather than changing the consumer's
// REUSE policy or attributing its own files to the harness. SSH allowed-signers
// accepts comment lines, so the public trust file remains usable by ssh-keygen.
const trialFileLicense = "# SPDX-FileCopyrightText" + ": 2026 Digg - Agency for Digital Government\n" +
	"# SPDX-License-Identifier" + ": CC0-1.0\n\n"

func licensedTrialSigners(signers string) string {
	if strings.HasPrefix(signers, trialFileLicense) {
		return signers
	}
	return trialFileLicense + signers
}

// NextReleaseRequestFiles is the harness-owned change requesting a second
// release in a retained demo. Its metadata must survive into the final PR.
func NextReleaseRequestFiles() map[string]string {
	return map[string]string{"next-release-request.txt": trialFileLicense + "request the next release\n"}
}
