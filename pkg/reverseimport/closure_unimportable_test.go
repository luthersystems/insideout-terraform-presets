package reverseimport

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

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
