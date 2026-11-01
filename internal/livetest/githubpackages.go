// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/container"
)

var (
	errRuntimePackageCoordinates     = errors.New("runtime packages require canonical GHCR coordinates under the engine owner")
	errPackageIdentityInput          = errors.New("package identity requires a canonical name and acknowledged repository ID")
	errPackageRepositoryMismatch     = errors.New("container package source repository identity mismatch; refusing adoption")
	errPackageDeletionUnacknowledged = errors.New("exact package deletion requires an acknowledged organization and package ID")
	errPackageIdentityDrift          = errors.New("container package identity drift; refusing deletion")
	errPackageDeletionUnconfirmed    = errors.New("exact container package deletion not confirmed")
)

// GitHubPackageIdentity is an exact, explicitly acknowledged cleanup resource.
// Capture it only for packages this batch created or the operator acknowledged.
// The JSON names are the field names that retained receipts already carry.
type GitHubPackageIdentity struct {
	Organization string `json:"Organization"`
	Name         string `json:"Name"` // raw package name, including nested path; never URL-escaped
	ID           int64  `json:"ID"`
	RepositoryID int64  `json:"RepositoryID"`
}

// RuntimePackageNames lists exact runtime and cache coordinates without listing
// an organization or granting deletion authority. Prepared engines are retained.
func (engine GitHubEngine) RuntimePackageNames() ([]string, error) {
	prefix := "ghcr.io/" + strings.ToLower(engine.Owner) + "/"
	if !container.ValidRepositoryName(engine.Registry()+"/forge-ci-runtime") || !strings.HasPrefix(engine.Registry()+"/", prefix) || !container.ValidRepositoryName(prefix+strings.ToLower(engine.Repository)) {
		return nil, errRuntimePackageCoordinates
	}

	base := strings.TrimPrefix(engine.Registry()+"/", prefix)

	var names []string
	for _, image := range []string{"forge-ci-runtime-base", "forge-ci-runtime", "forge-ci-runtime-rust-stable", "forge-ci-runtime-go-1.27", "forge-ci-runtime-node-24", "forge-ci-runtime-java-25", "forge-ci-runtime-android-35"} {
		names = append(names, base+image)
	}

	return append(names, strings.ToLower(engine.Repository)+"-buildcache"), nil
}

// ReadContainerPackageIdentity checks the exact source repository binding.
// Missing packages return os.ErrNotExist. Nil repository ownership is refused.
func (trial *GitHubTrial) ReadContainerPackageIdentity(ctx context.Context, name string, repositoryID int64) (GitHubPackageIdentity, error) {
	identity := GitHubPackageIdentity{Organization: trial.org, Name: name, RepositoryID: repositoryID}
	if repositoryID <= 0 || !container.ValidRepositoryName("ghcr.io/"+trial.org+"/"+name) {
		return identity, errPackageIdentityInput
	}
	// go-github v91 applies url.PathEscape once; pass the raw nested name.
	pkg, response, err := trial.api.Organizations.GetPackage(ctx, trial.org, "container", name)
	if response != nil && response.StatusCode == http.StatusNotFound {
		return identity, os.ErrNotExist
	}

	if err != nil {
		return identity, fmt.Errorf("read exact container package: %w", err)
	}

	if pkg.GetID() <= 0 || pkg.GetName() != name || pkg.GetRepository() == nil || pkg.GetRepository().GetID() != repositoryID {
		return identity, errPackageRepositoryMismatch
	}

	identity.ID = pkg.GetID()

	return identity, nil
}

// DeleteContainerPackage deletes only an explicitly acknowledged identity after
// rereading both package and source repository IDs, then confirms absence.
func (trial *GitHubTrial) DeleteContainerPackage(ctx context.Context, acknowledged GitHubPackageIdentity) error {
	if acknowledged.Organization != trial.org || acknowledged.ID <= 0 {
		return errPackageDeletionUnacknowledged
	}

	current, err := trial.ReadContainerPackageIdentity(ctx, acknowledged.Name, acknowledged.RepositoryID)
	if err != nil {
		return err
	}

	if current != acknowledged {
		return errPackageIdentityDrift
	}

	if _, err := trial.api.Organizations.DeletePackage(ctx, trial.org, "container", acknowledged.Name); err != nil {
		return fmt.Errorf("delete exact container package: %w", err)
	}

	_, response, err := trial.api.Organizations.GetPackage(ctx, trial.org, "container", acknowledged.Name)
	if err == nil || response == nil || response.StatusCode != http.StatusNotFound {
		return errPackageDeletionUnconfirmed
	}

	return nil
}
