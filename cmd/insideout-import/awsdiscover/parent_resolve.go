package awsdiscover

import (
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// parent_resolve.go is the instance-level half of the parent/child
// discovery model (issue #650). pkg/imported/labels' parentTfType
// registry answers the *type*-level question — "aws_s3_bucket_versioning
// is scoped to aws_s3_bucket". This file answers the *instance*-level
// question — "this specific versioning row belongs to that specific
// bucket row" — by joining each discovered child against the rest of the
// discovery result and stamping the parent's Terraform Address onto
// ResourceIdentity.ParentAddress.
//
// The resolver runs once over the fully assembled cross-discoverer set
// (see AWSDiscoverer.DiscoverTypes) so it can see every parent regardless
// of which discoverer produced it. It is purely a join over identifiers
// the discoverers already capture — no new AWS API calls.

// parentFK describes how a discovered child resource of a given Terraform
// type links to its parent instance. Exactly one of childKey / parentKey
// is set, distinguishing the two join directions:
//
//   - Forward edge (childKey set): the child carries a foreign-key
//     identifier of its parent — e.g. an aws_s3_bucket_versioning row
//     carries NativeIDs["bucket"], the bucket name, which is the parent
//     aws_s3_bucket's ImportID. This is the common case.
//
//   - Reverse edge (parentKey set): the child carries no reference to
//     its parent; the parent carries a reference to the child instead.
//     The two parameter-group types are like this — an
//     aws_db_parameter_group is referenced *by* the aws_db_instance that
//     uses it (the instance's DBParameterGroupName), not the other way
//     round. Reverse edges resolve only on an unambiguous 1:1 match.
type parentFK struct {
	// parentType is the Terraform type of the parent resource. It must
	// agree with pkg/imported/labels' type-level parentTfType registry;
	// TestParentFK_AgreesWithLabelsRegistry pins that invariant.
	parentType string
	// childKey, for a forward edge, is the NativeIDs key on the CHILD
	// whose value identifies the parent (matched against the parent's
	// ImportID or any of its NativeIDs values).
	childKey string
	// parentKey, for a reverse edge, is the NativeIDs key on the PARENT
	// whose value equals this child's ImportID.
	parentKey string
}

// parentFKByChildType is the AWS foreign-key registry: child Terraform
// type → how to find its parent instance. Every key here is a member of
// the labels parentTfType registry; the inverse (a labels child with no
// entry here) is allowed only for the types in unresolvableChildTypes.
// Both invariants are enforced by tests so the type-level and
// instance-level halves cannot silently drift apart.
var parentFKByChildType = map[string]parentFK{
	// S3 bucket sub-configuration. Every S3 sub-resource discoverer
	// (sdkonly_s3.go) and the aws_s3_bucket_policy Cloud Control entry
	// stamp NativeIDs["bucket"] with the bucket name, which is the
	// parent aws_s3_bucket's ImportID.
	"aws_s3_bucket_versioning":                           {parentType: "aws_s3_bucket", childKey: "bucket"},
	"aws_s3_bucket_lifecycle_configuration":              {parentType: "aws_s3_bucket", childKey: "bucket"},
	"aws_s3_bucket_ownership_controls":                   {parentType: "aws_s3_bucket", childKey: "bucket"},
	"aws_s3_bucket_public_access_block":                  {parentType: "aws_s3_bucket", childKey: "bucket"},
	"aws_s3_bucket_server_side_encryption_configuration": {parentType: "aws_s3_bucket", childKey: "bucket"},
	"aws_s3_bucket_policy":                               {parentType: "aws_s3_bucket", childKey: "bucket"},

	// VPC children. The route table and subnet discoverers lift the
	// CloudFormation model's VpcId into NativeIDs["vpc_id"] (see
	// vpcIDNativeIDs); it matches the parent aws_vpc's ImportID
	// (vpc-…). aws_internet_gateway and aws_vpc_dhcp_options carry no
	// VPC reference in their own model — see unresolvableChildTypes.
	"aws_route_table": {parentType: "aws_vpc", childKey: "vpc_id"},
	"aws_subnet":      {parentType: "aws_vpc", childKey: "vpc_id"},

	// Split security-group rules. The ingress/egress Cloud Control
	// entries lift the model's GroupId into NativeIDs["security_group_id"],
	// which matches the parent aws_security_group's ImportID (sg-…).
	"aws_vpc_security_group_ingress_rule": {parentType: "aws_security_group", childKey: "security_group_id"},
	"aws_vpc_security_group_egress_rule":  {parentType: "aws_security_group", childKey: "security_group_id"},

	// CloudWatch Logs. The log-stream discoverer splits the Cloud
	// Control compound id into NativeIDs["log_group_name"], which
	// matches the parent aws_cloudwatch_log_group's ImportID.
	"aws_cloudwatch_log_stream": {parentType: "aws_cloudwatch_log_group", childKey: "log_group_name"},

	// KMS. The alias discoverer lifts TargetKeyId into
	// NativeIDs["target_key_id"]. AWS allows that value to be either a
	// key id or a key ARN; the resolver indexes the parent aws_kms_key
	// by both its ImportID (key id) and NativeIDs (arn), so either form
	// joins.
	"aws_kms_alias": {parentType: "aws_kms_key", childKey: "target_key_id"},

	// IAM. The role-policy-attachment discoverer stamps
	// NativeIDs["role"]; the inline-role-policy discoverer stamps
	// NativeIDs["role_name"]. Both equal the parent aws_iam_role's
	// ImportID (the role name).
	"aws_iam_role_policy_attachment": {parentType: "aws_iam_role", childKey: "role"},
	"aws_iam_role_policy":            {parentType: "aws_iam_role", childKey: "role_name"},

	// Parameter groups — reverse edges. A parameter group's own model
	// has no back-reference to the instance that uses it, so the
	// resolver scans for the parent instead: the aws_db_instance /
	// aws_elasticache_replication_group discoverers carry the parameter
	// group name (see arnAndKey) and the resolver matches it against
	// this child's ImportID. A parameter group shared by several
	// instances has no single parent and stays unlinked.
	"aws_db_parameter_group":          {parentType: "aws_db_instance", parentKey: "db_parameter_group"},
	"aws_elasticache_parameter_group": {parentType: "aws_elasticache_replication_group", parentKey: "cache_parameter_group"},
}

// unresolvableChildTypes are labels-registry child types for which the
// AWS resource model the discoverer reads carries no usable parent
// reference, so no parent-instance link can be emitted without
// discovering an additional association resource (a new AWS API call,
// out of scope for #650 which is "emit what you already know"). They are
// listed explicitly — rather than simply omitted — so
// TestParentFK_CoversLabelsRegistry can tell a deliberate, documented
// omission from an accidental gap. Tracked for follow-up in #651.
var unresolvableChildTypes = map[string]string{
	"aws_internet_gateway": "AWS::EC2::InternetGateway has no VpcId; the IGW↔VPC link is a separate AWS::EC2::VPCGatewayAttachment resource (#651)",
	"aws_vpc_dhcp_options": "AWS::EC2::DHCPOptions has no VpcId; the link is a separate AWS::EC2::VPCDHCPOptionsAssociation resource (#651)",
}

// parentIndexKey identifies a candidate parent by its Terraform type and
// one of the identifier values it exposes.
type parentIndexKey struct {
	typ string
	id  string
}

// resolveParentAddresses populates Identity.ParentAddress on every
// discovered child resource whose parent instance is present in
// resources. It mutates the slice in place and is safe to call on any
// discovery result — resources with no parent, and children whose parent
// was not discovered, are left with an empty ParentAddress (no dangling
// reference).
//
// resources is the fully assembled cross-discoverer set; the resolver is
// a pure in-memory join and issues no AWS calls.
func resolveParentAddresses(resources []imported.ImportedResource) {
	if len(resources) < 2 {
		return
	}

	// Forward index: every identifier a resource exposes — its ImportID
	// and each NativeIDs value — mapped to its Terraform Address, keyed
	// by (type, identifier). A (type, identifier) pair that resolves to
	// more than one distinct address is marked ambiguous and dropped, so
	// a child is never linked to an arbitrarily chosen parent.
	fwd := make(map[parentIndexKey]string)
	ambiguous := make(map[parentIndexKey]struct{})
	index := func(typ, id, addr string) {
		if id == "" || addr == "" {
			return
		}
		k := parentIndexKey{typ, id}
		if _, bad := ambiguous[k]; bad {
			return
		}
		if prev, ok := fwd[k]; ok && prev != addr {
			ambiguous[k] = struct{}{}
			delete(fwd, k)
			return
		}
		fwd[k] = addr
	}
	for i := range resources {
		id := resources[i].Identity
		index(id.Type, id.ImportID, id.Address)
		for _, v := range id.NativeIDs {
			index(id.Type, v, id.Address)
		}
	}

	for i := range resources {
		child := &resources[i]
		fk, ok := parentFKByChildType[child.Identity.Type]
		if !ok {
			continue
		}
		if fk.parentKey == "" {
			// Forward edge: the child carries the parent's identifier.
			fkVal := child.Identity.NativeIDs[fk.childKey]
			if fkVal == "" {
				continue
			}
			k := parentIndexKey{fk.parentType, fkVal}
			if _, bad := ambiguous[k]; bad {
				continue
			}
			if addr := fwd[k]; addr != "" {
				child.Identity.ParentAddress = addr
			}
			continue
		}
		// Reverse edge: the parent carries the child's identifier. Scan
		// for parents of fk.parentType whose NativeIDs[fk.parentKey]
		// equals this child's ImportID. Only an unambiguous 1:1 match
		// yields a link.
		childID := child.Identity.ImportID
		if childID == "" {
			continue
		}
		var match string
		matches := 0
		for j := range resources {
			p := resources[j].Identity
			if p.Type != fk.parentType || p.Address == "" {
				continue
			}
			if p.NativeIDs[fk.parentKey] == childID {
				match = p.Address
				matches++
			}
		}
		if matches == 1 {
			child.Identity.ParentAddress = match
		}
	}
}
