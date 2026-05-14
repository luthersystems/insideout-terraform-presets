package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_project_iam_member (Bundle G1, #470).
//
// Cloud Asset Inventory does NOT surface IAM bindings as a separate
// asset type — they live as a field on each parent asset, and
// SearchAllResources never returns them as standalone rows. This
// discoverer follows the non-CAI precedent established by
// google_sql_user (#383) and google_logging_project_sink (#382): it
// calls cloudresourcemanager.googleapis.com/v3 Projects.GetIamPolicy
// directly and emits one ImportedResource per (project × role × member).
//
// Fan-out is degenerate — one project = one IAM policy. The
// orchestrator passes the real GCP project ID through projectID; the
// priorResults slice is unused (no parent-CAI-row dependency).
//
// Terraform import ID: "<project> <role> <member>" (space-separated).
// See:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/google_project_iam#google_project_iam_member

const (
	projectIAMMemberTFType    = "google_project_iam_member"
	projectIAMMemberAssetType = "cloudresourcemanager.googleapis.com/IamPolicy" // descriptive only; CAI rejects this
)

type projectIAMMemberDiscoverer struct {
	lister gcpIAMPolicyLister
}

func newProjectIAMMemberDiscoverer(lister gcpIAMPolicyLister) Discoverer {
	return &projectIAMMemberDiscoverer{lister: lister}
}

func (projectIAMMemberDiscoverer) ResourceType() string   { return projectIAMMemberTFType }
func (projectIAMMemberDiscoverer) AssetType() string      { return projectIAMMemberAssetType }
func (projectIAMMemberDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (projectIAMMemberDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (projectIAMMemberDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("project_iam_member: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI calls cloudresourcemanager Projects.GetIamPolicy on the
// project once and emits one row per (role × member). Per-error soft-
// fail surfaces via emitter.ServiceWarn — a single project not
// returning a policy (e.g. caller lacks resourcemanager.projects.
// getIamPolicy) doesn't propagate as a hard error.
func (d *projectIAMMemberDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, _ []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	bindings, err := d.lister.GetProjectIAMPolicy(ctx, projectID)
	if err != nil {
		msg := fmt.Sprintf("project_iam_member: get IAM policy failed for project %q (continuing): %v", projectID, err)
		emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, b := range bindings {
		for _, m := range b.Members {
			importID := projectIAMMemberImportID(projectID, b.Role, m)
			name := projectID + "-" + iamRoleSuffix(b.Role) + "-" + iamMemberSuffix(m)
			out = append(out, makeImportedResource(book, projectIAMMemberTFType, name, importID, projectID, "", map[string]string{
				"project": projectID,
				"role":    b.Role,
				"member":  m,
			}, nil))
		}
	}
	return out, nil
}

// projectIAMMemberImportID composes the Terraform import-ID per
// provider docs: "<project> <role> <member>" (space-separated). The
// space delimiter is load-bearing — the provider's parser splits on
// it. Anchored to the verified format in the task spec.
func projectIAMMemberImportID(projectID, role, member string) string {
	return projectID + " " + role + " " + member
}
