package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_secret_manager_secret_iam_member
// (Bundle G1, #470).
//
// Walks the google_secret_manager_secret rows discovered during the
// CAI phase and calls secretmanager.googleapis.com/v1
// Projects.Secrets.GetIamPolicy per secret, emitting one row per
// (secret × role × member). Compare to the _iam_binding sibling which
// collapses members.
//
// Per-secret failures soft-fail via the progress emitter.
//
// Terraform import ID:
//
//	"projects/<project>/secrets/<secret_id> <role> <member>"
//
// (Three space-separated tokens; the secret path uses slashes.)
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/secret_manager_secret_iam#google_secret_manager_secret_iam_member

const (
	secretManagerSecretIAMMemberTFType    = "google_secret_manager_secret_iam_member"
	secretManagerSecretIAMMemberAssetType = "secretmanager.googleapis.com/IamPolicy" // descriptive only
)

type secretManagerSecretIAMMemberDiscoverer struct {
	lister gcpIAMPolicyLister
}

func newSecretManagerSecretIAMMemberDiscoverer(lister gcpIAMPolicyLister) Discoverer {
	return &secretManagerSecretIAMMemberDiscoverer{lister: lister}
}

func (secretManagerSecretIAMMemberDiscoverer) ResourceType() string {
	return secretManagerSecretIAMMemberTFType
}
func (secretManagerSecretIAMMemberDiscoverer) AssetType() string {
	return secretManagerSecretIAMMemberAssetType
}
func (secretManagerSecretIAMMemberDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (secretManagerSecretIAMMemberDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (secretManagerSecretIAMMemberDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("secret_manager_secret_iam_member: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

func (d *secretManagerSecretIAMMemberDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != secretManagerSecretTFType {
			continue
		}
		secretFullName := prior.Identity.ImportID
		secretShort := prior.Identity.NameHint
		bindings, err := d.lister.GetSecretIAMPolicy(ctx, secretFullName)
		if err != nil {
			msg := fmt.Sprintf("secret_manager_secret_iam_member: get IAM policy failed for secret %q (continuing): %v", secretFullName, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		for _, b := range bindings {
			for _, m := range b.Members {
				importID := secretManagerSecretIAMMemberImportID(secretFullName, b.Role, m)
				name := secretShort + "-" + iamRoleSuffix(b.Role) + "-" + iamMemberSuffix(m)
				out = append(out, makeImportedResource(book, secretManagerSecretIAMMemberTFType, name, importID, projectID, "", map[string]string{
					"secret_id": secretFullName,
					"role":      b.Role,
					"member":    m,
				}, nil))
			}
		}
	}
	return out, nil
}

// secretManagerSecretIAMMemberImportID composes the Terraform import-
// ID per provider docs: "projects/<p>/secrets/<id> <role> <member>".
func secretManagerSecretIAMMemberImportID(secretFullName, role, member string) string {
	return secretFullName + " " + role + " " + member
}
