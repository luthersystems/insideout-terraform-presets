package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_cloudfunctions2_function_iam_member
// (Bundle G1, #470).
//
// Walks the google_cloudfunctions2_function rows discovered during
// the CAI phase and calls cloudfunctions.googleapis.com/v2
// Functions.GetIamPolicy per function, emitting one row per
// (function × role × member).
//
// Per-function failures soft-fail via the progress emitter.
//
// Terraform import ID:
//
//	"projects/<project>/locations/<location>/functions/<function> <role> <member>"
//
// Per provider docs:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/cloudfunctions2_function_iam#google_cloudfunctions2_function_iam_member

const (
	cloudFunctions2FunctionIAMMemberTFType    = "google_cloudfunctions2_function_iam_member"
	cloudFunctions2FunctionIAMMemberAssetType = "cloudfunctions.googleapis.com/IamPolicy" // descriptive only
)

type cloudFunctions2FunctionIAMMemberDiscoverer struct {
	lister gcpIAMPolicyLister
}

func newCloudFunctions2FunctionIAMMemberDiscoverer(lister gcpIAMPolicyLister) Discoverer {
	return &cloudFunctions2FunctionIAMMemberDiscoverer{lister: lister}
}

func (cloudFunctions2FunctionIAMMemberDiscoverer) ResourceType() string {
	return cloudFunctions2FunctionIAMMemberTFType
}
func (cloudFunctions2FunctionIAMMemberDiscoverer) AssetType() string {
	return cloudFunctions2FunctionIAMMemberAssetType
}
func (cloudFunctions2FunctionIAMMemberDiscoverer) ScopeStyle() ScopeStyle {
	return ScopeStyleNonCAI
}

func (cloudFunctions2FunctionIAMMemberDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (cloudFunctions2FunctionIAMMemberDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("cloudfunctions2_function_iam_member: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

func (d *cloudFunctions2FunctionIAMMemberDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != cloudFunctions2FunctionTFType {
			continue
		}
		fnFullName := prior.Identity.ImportID
		fnShort := prior.Identity.NameHint
		loc := prior.Identity.Location
		bindings, err := d.lister.GetCloudFunctions2FunctionIAMPolicy(ctx, fnFullName)
		if err != nil {
			msg := fmt.Sprintf("cloudfunctions2_function_iam_member: get IAM policy failed for function %q (continuing): %v", fnFullName, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		for _, b := range bindings {
			for _, m := range b.Members {
				importID := cloudFunctions2FunctionIAMMemberImportID(fnFullName, b.Role, m)
				name := fnShort + "-" + iamRoleSuffix(b.Role) + "-" + iamMemberSuffix(m)
				out = append(out, makeImportedResource(book, cloudFunctions2FunctionIAMMemberTFType, name, importID, projectID, loc, map[string]string{
					"function_id": fnFullName,
					"role":        b.Role,
					"member":      m,
				}, nil))
			}
		}
	}
	return out, nil
}

// cloudFunctions2FunctionIAMMemberImportID composes the Terraform
// import-ID per provider docs:
//
//	"projects/<p>/locations/<l>/functions/<n> <role> <member>"
func cloudFunctions2FunctionIAMMemberImportID(fnFullName, role, member string) string {
	return fnFullName + " " + role + " " + member
}
