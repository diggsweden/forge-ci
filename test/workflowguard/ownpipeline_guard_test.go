// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package workflowguard

import (
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"github.com/diggsweden/forge-ci/internal/testutil/reporoot"
)

// GitHub calls only a workflow_call workflow, so that trigger is what makes a
// file part of the layer, and its name must say the same: self-*.yml for the
// repository's own pipelines, any other name for what consumers call. The
// guards that skip own pipelines read the name; this keeps it honest.
func TestOwnPipelineNamesMatchTheirTriggers(t *testing.T) {
	t.Parallel()

	for _, path := range yamlFiles(t, ".github/workflows") {
		if mismatch := validate.OwnPipelineMismatch(path, reporoot.ReadFile(t, path)); mismatch != "" {
			t.Errorf("%s: %s", path, mismatch)
		}
	}
}
