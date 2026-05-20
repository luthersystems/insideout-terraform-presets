package labels

import (
	"testing"

	typeregistry "github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// TestParentTfType_ChildResolvesToParent pins the core contract: every
// known child type resolves to exactly the expected parent. The cases
// are exact-match and mutation-fragile — a change to any value in the
// parentTfTypes map (or an accidental deletion of a key) fails a
// specific row here rather than slipping through a presence-only check.
func TestParentTfType_ChildResolvesToParent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		child      string
		wantParent string
	}{
		// S3 bucket sub-configuration family.
		{"aws_s3_bucket_versioning", "aws_s3_bucket"},
		{"aws_s3_bucket_lifecycle_configuration", "aws_s3_bucket"},
		{"aws_s3_bucket_ownership_controls", "aws_s3_bucket"},
		{"aws_s3_bucket_public_access_block", "aws_s3_bucket"},
		{"aws_s3_bucket_server_side_encryption_configuration", "aws_s3_bucket"},
		{"aws_s3_bucket_policy", "aws_s3_bucket"},
		// VPC children.
		{"aws_route_table", "aws_vpc"},
		{"aws_internet_gateway", "aws_vpc"},
		{"aws_subnet", "aws_vpc"},
		{"aws_vpc_dhcp_options", "aws_vpc"},
		// Split security-group rules.
		{"aws_vpc_security_group_ingress_rule", "aws_security_group"},
		{"aws_vpc_security_group_egress_rule", "aws_security_group"},
		// CloudWatch Logs.
		{"aws_cloudwatch_log_stream", "aws_cloudwatch_log_group"},
		// KMS.
		{"aws_kms_alias", "aws_kms_key"},
		// IAM.
		{"aws_iam_role_policy_attachment", "aws_iam_role"},
		{"aws_iam_role_policy", "aws_iam_role"},
		// Database parameter groups.
		{"aws_db_parameter_group", "aws_db_instance"},
		{"aws_elasticache_parameter_group", "aws_elasticache_replication_group"},
	}
	for _, tc := range cases {
		t.Run(tc.child, func(t *testing.T) {
			t.Parallel()
			gotParent, ok := ParentTfType(tc.child)
			if !ok {
				t.Fatalf("ParentTfType(%q): ok = false, want true", tc.child)
			}
			if gotParent != tc.wantParent {
				t.Errorf("ParentTfType(%q) = %q, want %q", tc.child, gotParent, tc.wantParent)
			}
		})
	}
}

// TestParentTfType_StandaloneTypesHaveNoParent pins the negative half of
// the contract: a resource type that is independently importable — a
// parent itself, or a plain standalone resource — must NOT be in the
// registry. ParentTfType returns ("", false) so reliable's wizard keeps
// rendering it as its own tile.
func TestParentTfType_StandaloneTypesHaveNoParent(t *testing.T) {
	t.Parallel()
	standalone := []string{
		// Parents must not themselves be children.
		"aws_s3_bucket",
		"aws_vpc",
		"aws_security_group",
		"aws_cloudwatch_log_group",
		"aws_kms_key",
		"aws_iam_role",
		"aws_db_instance",
		"aws_elasticache_replication_group",
		// Plain standalone resources.
		"aws_dynamodb_table",
		"aws_sqs_queue",
		"aws_lambda_function",
		"aws_lb_target_group",
		"google_storage_bucket",
		// Unknown type — defensive.
		"not_a_real_type",
	}
	for _, tt := range standalone {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()
			gotParent, ok := ParentTfType(tt)
			if ok {
				t.Errorf("ParentTfType(%q): ok = true (parent=%q), want false", tt, gotParent)
			}
			if gotParent != "" {
				t.Errorf("ParentTfType(%q) = %q, want \"\"", tt, gotParent)
			}
		})
	}
}

// TestHasParent pins the boolean convenience accessor against the same
// child / standalone split, so a divergence between HasParent and
// ParentTfType fails loudly.
func TestHasParent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tfType string
		want   bool
	}{
		{"aws_s3_bucket_versioning", true},
		{"aws_kms_alias", true},
		{"aws_vpc_security_group_egress_rule", true},
		{"aws_s3_bucket", false},
		{"aws_kms_key", false},
		{"not_a_real_type", false},
	}
	for _, tc := range cases {
		t.Run(tc.tfType, func(t *testing.T) {
			t.Parallel()
			if got := HasParent(tc.tfType); got != tc.want {
				t.Errorf("HasParent(%q) = %v, want %v", tc.tfType, got, tc.want)
			}
		})
	}
}

// TestParentTfTypes_AllTypesAreKnown enforces that every key and every
// value in the registry is a member of registry.KnownTypes. A dangling
// edge — a child or parent that no longer exists upstream because a type
// was renamed or removed — is a release-blocking bug: reliable's wizard
// would either fold a tile into a parent that has no tile, or hide a
// child of a type the discoverer never emits.
func TestParentTfTypes_AllTypesAreKnown(t *testing.T) {
	t.Parallel()
	known := knownTypeSet(t)
	for child, parent := range parentTfTypes {
		if _, ok := known[child]; !ok {
			t.Errorf("child %q is not in registry.KnownTypes — typo or removed-upstream type", child)
		}
		if _, ok := known[parent]; !ok {
			t.Errorf("parent %q (of child %q) is not in registry.KnownTypes — typo or removed-upstream type", parent, child)
		}
	}
}

// TestParentTfTypes_NoSelfReference pins that no type is its own parent —
// a self-edge would make the wizard fold a tile into itself and hide it
// entirely.
func TestParentTfTypes_NoSelfReference(t *testing.T) {
	t.Parallel()
	for child, parent := range parentTfTypes {
		if child == parent {
			t.Errorf("type %q is registered as its own parent", child)
		}
	}
}

// TestParentTfTypes_ParentsAreNotChildren pins that the registry is a
// flat one-level tree: a registered parent must not itself appear as a
// child key. The wizard folds children one level into their parent's
// tile; a parent that is also a child would need recursive folding,
// which the consumer does not implement.
func TestParentTfTypes_ParentsAreNotChildren(t *testing.T) {
	t.Parallel()
	for child, parent := range parentTfTypes {
		if grandparent, ok := parentTfTypes[parent]; ok {
			t.Errorf("parent %q (of child %q) is itself a child of %q — registry must be a flat one-level tree", parent, child, grandparent)
		}
	}
}

// knownTypeSet returns registry.KnownTypes as a set for O(1) membership
// checks.
func knownTypeSet(t *testing.T) map[string]struct{} {
	t.Helper()
	types := typeregistry.KnownTypes()
	if len(types) == 0 {
		t.Fatal("typeregistry.KnownTypes() returned nothing — cannot validate the parent registry")
	}
	set := make(map[string]struct{}, len(types))
	for _, ty := range types {
		set[ty] = struct{}{}
	}
	return set
}
