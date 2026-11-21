// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
//
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package git_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/diggsweden/forge-ci/internal/domain/git"
)

func TestParseRawLogReadsSignatureKindAndTrailers(t *testing.T) {
	t.Parallel()

	const raw = "commit aaa111\ntree t\nparent p\nauthor Ada Lind <ada@example.org> 1 +0000\ncommitter C <c@example.org> 1 +0000\n" +
		"gpgsig -----BEGIN SSH SIGNATURE-----\n U1NIU0lH\n -----END SSH SIGNATURE-----\n\n" +
		"    fix(ci): show the matrix once\n    \n    The body explains.\n    Not: a trailer here\n    \n" +
		"    Signed-off-by: Ada Lind <ada@example.org>\n    Fixes: #42\n    Co-authored-by: Bo Ek\n     <bo@example.org>\n" +
		"\x00commit bbb222\ntree t\nauthor Bo Ek <bo@example.org> 1 +0000\ncommitter C <c@example.org> 1 +0000\n" +
		"gpgsig-sha256 -----BEGIN PGP SIGNATURE-----\n \n abc\n -----END PGP SIGNATURE-----\n\n    docs: one line\n" +
		"\x00commit ccc333\ntree t\nauthor Cy <cy@example.org> 1 +0000\n\n    feat: prose last\n    \n    Ends with prose: not trailers\n    because this line is not one.\n" +
		"\x00commit ddd444\ntree t\nauthor Di <di@example.org> 1 +0000\ngpgsig -----BEGIN SIGNED MESSAGE-----\n MIIx\n -----END SIGNED MESSAGE-----\n\n    chore: x509\n" +
		"\x00commit eee555\ntree t\nauthor Ed <ed@example.org> 1 +0000\ngpgsig -----BEGIN NEW FORMAT-----\n x\n\n    chore: unknown armor\n" +
		"\x00garbage"

	require.Equal(t, []git.Commit{
		{
			SHA: "aaa111", Author: "Ada Lind", Subject: "fix(ci): show the matrix once", Signature: "SSH",
			Trailers: []git.Trailer{
				{Key: "Signed-off-by", Value: "Ada Lind <ada@example.org>"},
				{Key: "Fixes", Value: "#42"},
				{Key: "Co-authored-by", Value: "Bo Ek <bo@example.org>"},
			},
		},
		{SHA: "bbb222", Author: "Bo Ek", Subject: "docs: one line", Signature: "GPG"},
		{SHA: "ccc333", Author: "Cy", Subject: "feat: prose last"},
		{SHA: "ddd444", Author: "Di", Subject: "chore: x509", Signature: "X.509"},
		{SHA: "eee555", Author: "Ed", Subject: "chore: unknown armor", Signature: "other"},
	}, git.ParseRawLog(raw))
}
