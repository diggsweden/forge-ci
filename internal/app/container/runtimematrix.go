// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package container

import (
	"context"
	"encoding/json"

	"github.com/diggsweden/forge-ci/internal/cliio"
	"github.com/diggsweden/forge-ci/internal/domain/ci"
	domaincontainer "github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/pipeline"
)

// PlanRuntimeMatrix is read-only roster selection for verification and publication
// workflows. Unlike publication planning, it is also safe on a pull request.
func PlanRuntimeMatrix(ctx context.Context, sink ci.OutputSink, roster, declared, selected string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	platforms, err := splitPlatforms(declared)
	if err != nil {
		return err
	}

	platforms, err = domaincontainer.SelectPlatforms(platforms, selected)
	if err != nil {
		return err
	}

	body, err := cliio.ReadRegularFile(roster)
	if err != nil {
		return err
	}

	var images []domaincontainer.RuntimeImage
	if err := decodeRuntimeJSON(body, &images); err != nil {
		return err
	}

	archs := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		archs = append(archs, PlatformSuffix(platform))
	}

	plan, err := domaincontainer.PlanRuntimeImages(images, archs)
	if err != nil {
		return err
	}

	type buildRow struct {
		Target string `json:"target"`
		Name   string `json:"name"`
		Arch   string `json:"arch"`
	}

	builds := make([]buildRow, 0, len(plan.Builds))
	for _, leg := range plan.Builds {
		builds = append(builds, buildRow{leg.Target, leg.Name, leg.Arch})
	}

	buildJSON, err := json.Marshal(pipeline.Matrix[buildRow]{Include: builds})
	if err != nil {
		return err
	}

	imageJSON, err := json.Marshal(pipeline.Matrix[domaincontainer.RuntimeImage]{Include: plan.Images})
	if err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := sink.Set(ctx, "build-matrix", string(buildJSON)); err != nil {
		return err
	}

	return sink.Set(ctx, "image-matrix", string(imageJSON))
}
