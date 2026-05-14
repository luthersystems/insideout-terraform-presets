package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_secret_manager_secret_iam_binding
// (Bundle G1, #470).
//
// Walks the google_secret_manager_secret rows discovered during the
// CAI phase and calls secretmanager.googleapis.com/v1
// Projects.Secrets.GetIamPolicy per secret, emitting one row per
// (secret × role). Members are collapsed into NativeIDs["members"]
// — the binding row's identity is parent+role.
//
// Per-secret failures soft-fail via the progress emitter.
//
// Terraform import ID: "projects/<project>/secrets/<secret_id> <role>"
// per provider docs:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/secret_manager_secret_iam#google_secret_manager_secret_iam_binding

const (
	secretManagerSecretIAMBindingTFType    = "google_secret_manager_secret_iam_binding"
	secretManagerSecretIAMBindingAssetType = "secretmanager.googleapis.com/IamPolicy" // descriptive only
)

type secretManagerSecretIAMBindingDiscoverer struct {
	lister gcpIAMPolicyLister
}

func newSecretManagerSecretIAMBindingDiscoverer(lister gcpIAMPolicyLister) Discoverer {
	return &secretManagerSecretIAMBindingDiscoverer{lister: lister}
}

func (secretManagerSecretIAMBindingDiscoverer) ResourceType() string {
	return secretManagerSecretIAMBindingTFType
}
func (secretManagerSecretIAMBindingDiscoverer) AssetType() string {
	return secretManagerSecretIAMBindingAssetType
}
func (secretManagerSecretIAMBindingDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (secretManagerSecretIAMBindingDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (secretManagerSecretIAMBindingDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("secret_manager_secret_iam_binding: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

func (d *secretManagerSecretIAMBindingDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != secretManagerSecretTFType {
			continue
		}
		// prior.Identity.ImportID is already "projects/<p>/secrets/<s>".
		secretFullName := prior.Identity.ImportID
		secretShort := prior.Identity.NameHint
		bindings, err := d.lister.GetSecretIAMPolicy(ctx, secretFullName)
		if err != nil {
			msg := fmt.Sprintf("secret_manager_secret_iam_binding: get IAM policy failed for secret %q (continuing): %v", secretFullName, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		for _, b := range bindings {
			importID := secretManagerSecretIAMBindingImportID(secretFullName, b.Role)
			name := secretShort + "-" + iamRoleSuffix(b.Role)
			out = append(out, makeImportedResource(book, secretManagerSecretIAMBindingTFType, name, importID, projectID, "", map[string]string{
				"secret_id": secretFullName,
				"role":      b.Role,
				"members":   strings.Join(b.Members, ","),
			}, nil))
		}
	}
	return out, nil
}

// secretManagerSecretIAMBindingImportID composes the Terraform
// import-ID per provider docs: "projects/<p>/secrets/<id> <role>".
// secretFullName is already the "projects/<p>/secrets/<id>" form
// produced by the parent google_secret_manager_secret discoverer.
func secretManagerSecretIAMBindingImportID(secretFullName, role string) string {
	return secretFullName + " " + role
}
