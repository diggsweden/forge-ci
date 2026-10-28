// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package version

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/diggsweden/forge-ci/internal/cliio"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/projecttype"
	domainversion "github.com/diggsweden/forge-ci/internal/domain/version"
)

func nativeBump(kind projecttype.Type) bool {
	_, supported := domainversion.BumpPatchFiles(kind)

	return supported
}

// The trusted plan assigns each native file to a project; the existing
// ApplyBump verifier then proves each project's version-only substitution.
// Gradle/Xcode changes remain engine-owned text edits, and changelogs come
// from their separate data artifact, never from the native-tool patch.
func applyPlannedNativeBumps(ctx context.Context, git applyBumpOps, inputs []bumpTarget, in BumpPlanInput, out io.Writer) error {
	body, err := cliio.ReadRegularFile(in.PatchPath)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != in.PatchSHA256 {
		return fmt.Errorf("version-bump plan patch differs from its key-free job's digest: %w", errs.ErrValidation)
	}

	version := strings.TrimPrefix(strings.TrimSpace(in.Version), "v")

	sections, err := domainversion.SplitBumpPatch(body, version)
	if err != nil {
		return err
	}

	patches := make([][]byte, len(inputs))

	for _, section := range sections {
		index := plannedPatchTarget(inputs, section.Path)
		if index < 0 {
			return fmt.Errorf("version-bump patch changes an unplanned project file %s: %w", section.Path, errs.ErrValidation)
		}

		patches[index] = append(patches[index], section.Patch...)
	}

	policy := in.ReleasePolicy
	if policy == "" {
		policy = "stable"
	}

	for index, target := range inputs {
		files, supported := domainversion.BumpPatchFiles(target.ProjectType)
		if !supported {
			continue
		}

		if len(patches[index]) == 0 {
			if err := requireProjectVersion(path.Join(target.WorkingDir, files[0]), version); err != nil {
				return err
			}

			continue
		}

		if git == nil {
			return fmt.Errorf("prepared version bump requires a Git adapter: %w", errs.ErrUsage)
		}

		if err := applyProjectPatch(ctx, git, target, policy, version, patches[index], out); err != nil {
			return err
		}
	}

	return nil
}

func plannedPatchTarget(inputs []bumpTarget, file string) int {
	for index, target := range inputs {
		files, supported := domainversion.BumpPatchFiles(target.ProjectType)
		if !supported {
			continue
		}

		dir := path.Clean(target.WorkingDir)
		if slices.Contains(files, path.Base(file)) && (path.Dir(file) == dir ||
			target.ProjectType == projecttype.Maven && (dir == "." || strings.HasPrefix(file, dir+"/"))) {
			return index
		}
	}

	return -1
}

func applyProjectPatch(ctx context.Context, git applyBumpOps, target bumpTarget, policy, version string, patch []byte, out io.Writer) error {
	file, err := os.CreateTemp("", "forge-ci-version-bump-*.patch")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()

	_, writeErr := file.Write(patch)
	closeErr := file.Close()

	if writeErr != nil {
		return writeErr
	}

	if closeErr != nil {
		return closeErr
	}

	paths, err := os.CreateTemp("", "forge-ci-version-paths-*")
	if err != nil {
		return err
	}

	defer func() { _ = os.Remove(paths.Name()) }()

	if err := paths.Close(); err != nil {
		return err
	}

	sum := sha256.Sum256(patch)
	_, err = ApplyBump(ctx, git, out, ApplyBumpInput{ReleasePolicy: policy, PatchPath: file.Name(), SHA256: hex.EncodeToString(sum[:]),
		ProjectType: target.ProjectType, Version: version, WorkingDir: target.WorkingDir, PathsFile: paths.Name()})

	return err
}
