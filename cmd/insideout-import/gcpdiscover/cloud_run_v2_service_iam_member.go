package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_cloud_run_v2_service_iam_member
// (Bundle G1, #470).
//
// Walks the google_cloud_run_v2_service rows discovered during the
// CAI phase and calls run.googleapis.com/v2 Services.GetIamPolicy
// per service, emitting one row per (service × role × member).
//
// Per-service failures soft-fail via the progress emitter.
//
// Terraform import ID:
//
//	"projects/<project>/locations/<location>/services/<service> <role> <member>"
//
// Per provider docs:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/cloud_run_v2_service_iam#google_cloud_run_v2_service_iam_member

const (
	cloudRunV2ServiceIAMMemberTFType    = "google_cloud_run_v2_service_iam_member"
	cloudRunV2ServiceIAMMemberAssetType = "run.googleapis.com/IamPolicy" // descriptive only
)

type cloudRunV2ServiceIAMMemberDiscoverer struct {
	lister gcpIAMPolicyLister
}

func newCloudRunV2ServiceIAMMemberDiscoverer(lister gcpIAMPolicyLister) Discoverer {
	return &cloudRunV2ServiceIAMMemberDiscoverer{lister: lister}
}

func (cloudRunV2ServiceIAMMemberDiscoverer) ResourceType() string {
	return cloudRunV2ServiceIAMMemberTFType
}
func (cloudRunV2ServiceIAMMemberDiscoverer) AssetType() string {
	return cloudRunV2ServiceIAMMemberAssetType
}
func (cloudRunV2ServiceIAMMemberDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (cloudRunV2ServiceIAMMemberDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (cloudRunV2ServiceIAMMemberDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("cloud_run_v2_service_iam_member: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

func (d *cloudRunV2ServiceIAMMemberDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != cloudRunV2ServiceTFType {
			continue
		}
		// prior.Identity.ImportID is
		// "projects/<p>/locations/<l>/services/<n>" — the parent
		// resource path Services.GetIamPolicy expects.
		serviceFullName := prior.Identity.ImportID
		serviceShort := prior.Identity.NameHint
		loc := prior.Identity.Location
		bindings, err := d.lister.GetCloudRunV2ServiceIAMPolicy(ctx, serviceFullName)
		if err != nil {
			msg := fmt.Sprintf("cloud_run_v2_service_iam_member: get IAM policy failed for service %q (continuing): %v", serviceFullName, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		for _, b := range bindings {
			for _, m := range b.Members {
				importID := cloudRunV2ServiceIAMMemberImportID(serviceFullName, b.Role, m)
				name := serviceShort + "-" + iamRoleSuffix(b.Role) + "-" + iamMemberSuffix(m)
				out = append(out, makeImportedResource(book, cloudRunV2ServiceIAMMemberTFType, name, importID, projectID, loc, map[string]string{
					"service_id": serviceFullName,
					"role":       b.Role,
					"member":     m,
				}, nil))
			}
		}
	}
	return out, nil
}

// cloudRunV2ServiceIAMMemberImportID composes the Terraform import-
// ID per provider docs:
//
//	"projects/<p>/locations/<l>/services/<n> <role> <member>"
func cloudRunV2ServiceIAMMemberImportID(serviceFullName, role, member string) string {
	return serviceFullName + " " + role + " " + member
}
