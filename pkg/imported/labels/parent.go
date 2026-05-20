// parent.go is the declarative parent/child registry for imported
// Terraform resource types.
//
// Why this exists: luthersystems/reliable's reverse-Terraform `/import`
// wizard (reliable#1617) discovers existing cloud resources and renders
// them as importable tiles grouped by category. Many discovered types
// are *child / sub-configuration* resources that have no independent
// lifecycle — they only make sense imported together with their parent.
// `aws_s3_bucket_versioning` is meaningless without the
// `aws_s3_bucket` it configures; `aws_route_table` is scoped to an
// `aws_vpc`; `aws_kms_alias` points at an `aws_kms_key`. The product
// decision is that children must NOT appear as standalone import tiles —
// the parent is the importable unit and importing it pulls the children
// along.
//
// The relationship knowledge belongs upstream here, not duplicated in
// reliable, for the same reason the display-label registry does: this
// repo already owns the discovery machinery (the awsdiscover
// `ParentLister` / `ImportIDFromParent` parent-scoped discoverers). Those
// tell the discoverer how to *enumerate* children via parent-scoped AWS
// APIs but never emit a declarative `childTfType → parentTfType` map —
// they key the parent by CloudFormation type, not Terraform type, and
// only cover the SDK-only sub-resource families. This registry
// fills that gap with a single Terraform-typed map, and the parent field
// rides along in the same generated labels artifact reliable already
// consumes (see cmd/imported-codegen/emit_labels.go) so the UI gets it
// for free with no second data path.
//
// Authoring rule: only add an entry for a child that genuinely requires
// a *named parent of a specific type* to be addressable. A resource that
// has a parent-ish relationship but is still independently importable
// (e.g. `aws_lb_target_group` — usable without a load balancer) does NOT
// belong here. A type with no parent is simply absent from the map;
// ParentTfType returns ("", false) for it.
package labels

import "sort"

// parentTfTypes is the declarative childTfType → parentTfType registry.
//
// Each key is a Terraform resource type that the discovery pipeline can
// surface, and the value is the Terraform type of the parent resource it
// is scoped to. Every key and value here is a member of
// pkg/insideout-import/registry's KnownTypes set — the
// TestParentTfTypes_AllTypesAreKnown test enforces that so a typo or a
// renamed-upstream type fails loudly instead of silently producing a
// dangling edge.
//
// Grouped by parent family for readability; map iteration order is
// irrelevant — every consumer (the accessor, the codegen emitter) reads
// it by key.
var parentTfTypes = map[string]string{
	// S3 bucket sub-configuration family. Each of these is a distinct
	// Terraform resource type that AWS models as inline bucket
	// properties — they are addressed solely by the parent bucket name
	// and have no lifecycle of their own. Mirrors awsdiscover's
	// sdkOnlySubresourceTypeConfigs entries whose ParentCFNType is
	// "AWS::S3::Bucket".
	"aws_s3_bucket_versioning":                           "aws_s3_bucket",
	"aws_s3_bucket_lifecycle_configuration":              "aws_s3_bucket",
	"aws_s3_bucket_ownership_controls":                   "aws_s3_bucket",
	"aws_s3_bucket_public_access_block":                  "aws_s3_bucket",
	"aws_s3_bucket_server_side_encryption_configuration": "aws_s3_bucket",
	"aws_s3_bucket_policy":                               "aws_s3_bucket",

	// VPC children. A route table, internet gateway, subnet, and the
	// modern split security-group rule resources are all scoped to a
	// specific VPC and are not meaningful to import on their own.
	"aws_route_table":      "aws_vpc",
	"aws_internet_gateway": "aws_vpc",
	"aws_subnet":           "aws_vpc",
	"aws_vpc_dhcp_options": "aws_vpc",

	// Split security-group rule resources (the aws_vpc_security_group_*
	// resources that replaced the legacy aws_security_group_rule). Each
	// rule is addressed by, and only exists within, its security group.
	"aws_vpc_security_group_ingress_rule": "aws_security_group",
	"aws_vpc_security_group_egress_rule":  "aws_security_group",

	// CloudWatch Logs. A log stream is created inside a log group and is
	// addressed as "<log-group>:<stream>"; it has no standalone tile.
	"aws_cloudwatch_log_stream": "aws_cloudwatch_log_group",

	// KMS. An alias is a pointer to a key; importing the key is the unit
	// of work, the alias rides along.
	"aws_kms_alias": "aws_kms_key",

	// IAM. A role-policy attachment is the edge between a role and a
	// managed policy — its import ID is "<role>/<policy-arn>", so it is
	// scoped to the role. (Reliable groups it under the role tile.)
	"aws_iam_role_policy_attachment": "aws_iam_role",
	"aws_iam_role_policy":            "aws_iam_role",

	// Database parameter groups. A DB parameter group is attached to and
	// configures a specific database instance; an ElastiCache parameter
	// group likewise configures a replication group. Neither is a
	// standalone importable unit in the wizard.
	"aws_db_parameter_group":          "aws_db_instance",
	"aws_elasticache_parameter_group": "aws_elasticache_replication_group",
}

// ParentTfType returns the Terraform type of the parent resource that
// childTfType is scoped to, and true, when childTfType is a registered
// child. For any type with no parent — every importable, standalone
// resource — it returns ("", false).
//
// Consumers (e.g. reliable's import wizard) use this to decide which
// discovered types deserve a standalone tile: a type for which this
// returns true is folded into its parent's tile rather than shown on its
// own.
func ParentTfType(childTfType string) (string, bool) {
	parent, ok := parentTfTypes[childTfType]
	return parent, ok
}

// HasParent reports whether childTfType is a registered child type. It
// is the boolean-only convenience form of ParentTfType for call sites
// that only need the predicate.
func HasParent(childTfType string) bool {
	_, ok := parentTfTypes[childTfType]
	return ok
}

// ChildTfTypes returns the sorted list of every registered child type —
// the keys of the parentTfType registry. It exists so consumers in other
// packages (notably the awsdiscover parent-instance resolver) can pin a
// cross-package consistency invariant: every type-level child edge must
// have a corresponding instance-level foreign-key resolution rule, or be
// explicitly exempted. Returns a fresh slice; callers may mutate it.
func ChildTfTypes() []string {
	out := make([]string, 0, len(parentTfTypes))
	for child := range parentTfTypes {
		out = append(out, child)
	}
	sort.Strings(out)
	return out
}
