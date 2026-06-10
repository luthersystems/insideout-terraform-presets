package reverseimport

import (
	"reflect"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestMergeClosure_ScopedMatchesSweep is the #739 scoping-parity lock. The
// discoverer now scopes child enumeration to the selected parents instead of
// sweeping the whole account. Because mergeClosureResources ALREADY filters
// discovered children back to the selected parents, feeding it the scoped
// (selected-parent-only) discovery result must produce byte-identical merged
// resources + dependency edges as feeding it the full account sweep. This test
// pins that invariant so a future change to either the scoping or the merge
// filter that drifts them apart fails here.
func TestMergeClosure_ScopedMatchesSweep(t *testing.T) {
	selectedBucket := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.selected",
			ImportID: "io-selected",
			Region:   "us-east-1",
		},
	}
	selectedChild := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:         "aws",
			Type:          "aws_s3_bucket_versioning",
			Address:       "aws_s3_bucket_versioning.selected",
			ImportID:      "io-selected",
			Region:        "us-east-1",
			ParentAddress: "aws_s3_bucket.selected",
			NativeIDs:     map[string]string{"bucket": "io-selected"},
		},
	}
	// Resources only the account-wide sweep would surface: an unselected
	// bucket and its child. mergeClosureResources must drop these because
	// they don't match a selected parent.
	otherBucket := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.other",
			ImportID: "io-other",
			Region:   "us-east-1",
		},
	}
	otherChild := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:         "aws",
			Type:          "aws_s3_bucket_versioning",
			Address:       "aws_s3_bucket_versioning.other",
			ImportID:      "io-other",
			Region:        "us-east-1",
			ParentAddress: "aws_s3_bucket.other",
			NativeIDs:     map[string]string{"bucket": "io-other"},
		},
	}

	in := mergeClosureInput{
		current:         []imported.ImportedResource{selectedBucket},
		selectedParents: []imported.ImportedResource{selectedBucket},
		parentTypes:     []string{"aws_s3_bucket"},
		childTypes:      []string{"aws_s3_bucket_versioning"},
	}

	scopedIn := in
	scopedIn.discovered = []imported.ImportedResource{selectedBucket, selectedChild}
	scopedMerged, scopedDeps, _ := mergeClosureResources(scopedIn)

	sweptIn := in
	sweptIn.discovered = []imported.ImportedResource{selectedBucket, selectedChild, otherBucket, otherChild}
	sweptMerged, sweptDeps, _ := mergeClosureResources(sweptIn)

	if !reflect.DeepEqual(scopedMerged, sweptMerged) {
		t.Errorf("merged resources differ between scoped and swept inputs:\nscoped=%#v\nswept=%#v", scopedMerged, sweptMerged)
	}
	if !reflect.DeepEqual(scopedDeps, sweptDeps) {
		t.Errorf("dependency edges differ between scoped and swept inputs:\nscoped=%#v\nswept=%#v", scopedDeps, sweptDeps)
	}
	// Sanity: the selected child IS pulled in, the unselected one is NOT.
	var sawSelected, sawOther bool
	for _, r := range scopedMerged {
		switch r.Identity.Address {
		case "aws_s3_bucket_versioning.selected":
			sawSelected = true
		case "aws_s3_bucket_versioning.other":
			sawOther = true
		}
	}
	if !sawSelected {
		t.Error("selected parent's child was not pulled into the closure")
	}
	if sawOther {
		t.Error("unselected parent's child leaked into the closure")
	}
}

// TestMergeClosure_SkipsAWSManagedKMSAlias is the #cust3 item-2
// regression. The selection closure re-discovers EVERY child of each
// selected parent — including the AWS-managed alias/aws/* KMS aliases
// (e.g. alias/aws/rds, alias/aws/acm) that point at AWS-managed default
// keys when a customer key in the same region is selected. The primary
// discovery already routes those into unsupported.json via
// partitionUnimportable; re-adding them to the closure feeds genconfig a
// body-less import that drops as a generic no_generated_config orphan.
// mergeClosureResources must apply the SAME imported.UnimportableReason
// classifier so the closure and the primary discovery agree on exactly
// which instances are importable.
func TestMergeClosure_SkipsAWSManagedKMSAlias(t *testing.T) {
	selectedKey := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_kms_key",
			Address:  "aws_kms_key.selected",
			ImportID: "11111111-2222-3333-4444-555555555555",
			Region:   "us-west-2",
		},
	}
	awsManagedAlias := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:         "aws",
			Type:          "aws_kms_alias",
			Address:       "aws_kms_alias.alias_aws_acm",
			ImportID:      "alias/aws/acm",
			Region:        "us-west-2",
			ParentAddress: "aws_kms_key.selected",
			NativeIDs:     map[string]string{"name": "alias/aws/acm"},
		},
	}
	customerAlias := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:         "aws",
			Type:          "aws_kms_alias",
			Address:       "aws_kms_alias.customer",
			ImportID:      "alias/customer-tfstate",
			Region:        "us-west-2",
			ParentAddress: "aws_kms_key.selected",
			NativeIDs:     map[string]string{"name": "alias/customer-tfstate"},
		},
	}

	merged, deps, _ := mergeClosureResources(mergeClosureInput{
		current:         []imported.ImportedResource{selectedKey},
		selectedParents: []imported.ImportedResource{selectedKey},
		parentTypes:     []string{"aws_kms_key"},
		childTypes:      []string{"aws_kms_alias"},
		discovered:      []imported.ImportedResource{awsManagedAlias, customerAlias},
	})

	var sawAWSManaged, sawCustomer bool
	for _, r := range merged {
		switch r.Identity.Address {
		case "aws_kms_alias.alias_aws_acm":
			sawAWSManaged = true
		case "aws_kms_alias.customer":
			sawCustomer = true
		}
	}
	if sawAWSManaged {
		t.Error("closure pulled in AWS-managed alias/aws/acm — must be skipped as un-importable")
	}
	if !sawCustomer {
		t.Error("closure dropped the importable customer alias — only AWS-managed aliases should be skipped")
	}
	// The dependency edge for the AWS-managed alias must NOT be recorded.
	for _, d := range deps["aws_kms_key.selected"] {
		if d.Address == "aws_kms_alias.alias_aws_acm" {
			t.Error("AWS-managed alias recorded as a closure dependency")
		}
	}
}
