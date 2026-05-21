package awsdiscover

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/labels"
)

// res builds a minimal discovered resource for the resolver tests. The
// resolver only reads Identity.{Type,Address,ImportID,NativeIDs}.
func res(typ, addr, importID string, native map[string]string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:     "aws",
			Type:      typ,
			Address:   addr,
			ImportID:  importID,
			NativeIDs: native,
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
}

// parentAddrByAddress indexes the resolved ParentAddress of every
// resource by its own Address, so a table test can assert the join
// outcome without depending on slice order.
func parentAddrByAddress(rs []imported.ImportedResource) map[string]string {
	out := make(map[string]string, len(rs))
	for _, r := range rs {
		out[r.Identity.Address] = r.Identity.ParentAddress
	}
	return out
}

func TestResolveParentAddresses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// in is the discovery set; resolveParentAddresses mutates it.
		in []imported.ImportedResource
		// want maps a resource Address to its expected ParentAddress
		// after resolution. An entry of "" pins "deliberately unlinked".
		want map[string]string
	}{
		{
			name: "forward edge — S3 sub-config links to its bucket",
			in: []imported.ImportedResource{
				res("aws_s3_bucket", "aws_s3_bucket.logs", "my-logs-bucket", map[string]string{"name": "my-logs-bucket"}),
				res("aws_s3_bucket_versioning", "aws_s3_bucket_versioning.logs", "my-logs-bucket", map[string]string{"bucket": "my-logs-bucket"}),
				res("aws_s3_bucket_public_access_block", "aws_s3_bucket_public_access_block.logs", "my-logs-bucket", map[string]string{"bucket": "my-logs-bucket"}),
			},
			want: map[string]string{
				"aws_s3_bucket.logs":                     "",
				"aws_s3_bucket_versioning.logs":          "aws_s3_bucket.logs",
				"aws_s3_bucket_public_access_block.logs": "aws_s3_bucket.logs",
			},
		},
		{
			name: "forward edge — multiple buckets, each child joins its own",
			in: []imported.ImportedResource{
				res("aws_s3_bucket", "aws_s3_bucket.a", "bucket-a", map[string]string{"name": "bucket-a"}),
				res("aws_s3_bucket", "aws_s3_bucket.b", "bucket-b", map[string]string{"name": "bucket-b"}),
				res("aws_s3_bucket_versioning", "aws_s3_bucket_versioning.a", "bucket-a", map[string]string{"bucket": "bucket-a"}),
				res("aws_s3_bucket_versioning", "aws_s3_bucket_versioning.b", "bucket-b", map[string]string{"bucket": "bucket-b"}),
			},
			want: map[string]string{
				"aws_s3_bucket_versioning.a": "aws_s3_bucket.a",
				"aws_s3_bucket_versioning.b": "aws_s3_bucket.b",
			},
		},
		{
			name: "forward edge — VPC child resolves via NativeIDs vpc_id",
			in: []imported.ImportedResource{
				res("aws_vpc", "aws_vpc.main", "vpc-0abc", map[string]string{"name": "vpc-0abc"}),
				res("aws_subnet", "aws_subnet.web", "subnet-01", map[string]string{"name": "subnet-01", "vpc_id": "vpc-0abc"}),
				res("aws_route_table", "aws_route_table.rt", "rtb-09", map[string]string{"name": "rtb-09", "vpc_id": "vpc-0abc"}),
			},
			want: map[string]string{
				"aws_subnet.web":     "aws_vpc.main",
				"aws_route_table.rt": "aws_vpc.main",
			},
		},
		{
			name: "forward edge — KMS alias matches key by ARN-form target_key_id",
			in: []imported.ImportedResource{
				res("aws_kms_key", "aws_kms_key.k", "1234abcd-key-id", map[string]string{
					"name": "1234abcd-key-id",
					"arn":  "arn:aws:kms:us-east-1:111:key/1234abcd-key-id",
				}),
				// alias points at the key by ARN, not the bare key id.
				res("aws_kms_alias", "aws_kms_alias.a", "alias/app", map[string]string{
					"name":          "alias/app",
					"target_key_id": "arn:aws:kms:us-east-1:111:key/1234abcd-key-id",
				}),
			},
			want: map[string]string{
				"aws_kms_alias.a": "aws_kms_key.k",
			},
		},
		{
			name: "missing parent — child whose bucket was not discovered stays unlinked",
			in: []imported.ImportedResource{
				res("aws_s3_bucket_versioning", "aws_s3_bucket_versioning.orphan", "gone-bucket", map[string]string{"bucket": "gone-bucket"}),
			},
			want: map[string]string{
				"aws_s3_bucket_versioning.orphan": "",
			},
		},
		{
			name: "ambiguous forward — two parents share the identifier, child unlinked",
			in: []imported.ImportedResource{
				res("aws_iam_role", "aws_iam_role.one", "shared-role", map[string]string{"name": "shared-role"}),
				res("aws_iam_role", "aws_iam_role.two", "shared-role", map[string]string{"name": "shared-role"}),
				res("aws_iam_role_policy", "aws_iam_role_policy.p", "shared-role:inline", map[string]string{"role_name": "shared-role"}),
			},
			want: map[string]string{
				"aws_iam_role_policy.p": "",
			},
		},
		{
			name: "reverse edge — parameter group links to its sole instance",
			in: []imported.ImportedResource{
				res("aws_db_instance", "aws_db_instance.prod", "prod-db", map[string]string{
					"name":               "prod-db",
					"arn":                "arn:aws:rds:us-east-1:111:db:prod-db",
					"db_parameter_group": "prod-pg",
				}),
				res("aws_db_parameter_group", "aws_db_parameter_group.pg", "prod-pg", map[string]string{"name": "prod-pg"}),
			},
			want: map[string]string{
				"aws_db_parameter_group.pg": "aws_db_instance.prod",
			},
		},
		{
			name: "reverse edge — ElastiCache parameter group links to its replication group",
			in: []imported.ImportedResource{
				res("aws_elasticache_replication_group", "aws_elasticache_replication_group.redis", "redis-rg", map[string]string{
					"name":                  "redis-rg",
					"cache_parameter_group": "redis-pg",
				}),
				res("aws_elasticache_parameter_group", "aws_elasticache_parameter_group.pg", "redis-pg", map[string]string{"name": "redis-pg"}),
			},
			want: map[string]string{
				"aws_elasticache_parameter_group.pg": "aws_elasticache_replication_group.redis",
			},
		},
		{
			name: "reverse edge — parameter group shared by two instances stays unlinked",
			in: []imported.ImportedResource{
				res("aws_db_instance", "aws_db_instance.a", "db-a", map[string]string{"name": "db-a", "db_parameter_group": "shared-pg"}),
				res("aws_db_instance", "aws_db_instance.b", "db-b", map[string]string{"name": "db-b", "db_parameter_group": "shared-pg"}),
				res("aws_db_parameter_group", "aws_db_parameter_group.pg", "shared-pg", map[string]string{"name": "shared-pg"}),
			},
			want: map[string]string{
				"aws_db_parameter_group.pg": "",
			},
		},
		{
			name: "reverse edge — parameter group with no referencing instance stays unlinked",
			in: []imported.ImportedResource{
				res("aws_db_instance", "aws_db_instance.a", "db-a", map[string]string{"name": "db-a", "db_parameter_group": "other-pg"}),
				res("aws_db_parameter_group", "aws_db_parameter_group.pg", "lonely-pg", map[string]string{"name": "lonely-pg"}),
			},
			want: map[string]string{
				"aws_db_parameter_group.pg": "",
			},
		},
		{
			name: "unresolvable type — internet gateway has no FK rule, stays unlinked",
			in: []imported.ImportedResource{
				res("aws_vpc", "aws_vpc.main", "vpc-0abc", map[string]string{"name": "vpc-0abc"}),
				res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-01", map[string]string{"name": "igw-01"}),
				res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-01", map[string]string{"name": "dopt-01"}),
			},
			want: map[string]string{
				"aws_internet_gateway.igw": "",
				"aws_vpc_dhcp_options.d":   "",
			},
		},
		{
			name: "non-child resource — a plain parent type carries no ParentAddress",
			in: []imported.ImportedResource{
				res("aws_vpc", "aws_vpc.main", "vpc-0abc", map[string]string{"name": "vpc-0abc"}),
				res("aws_sqs_queue", "aws_sqs_queue.q", "https://sqs/q", map[string]string{"name": "q"}),
			},
			want: map[string]string{
				"aws_vpc.main":    "",
				"aws_sqs_queue.q": "",
			},
		},
		{
			name: "child with empty FK value stays unlinked",
			in: []imported.ImportedResource{
				res("aws_s3_bucket", "aws_s3_bucket.b", "b", map[string]string{"name": "b"}),
				// versioning row whose discoverer failed to capture the bucket.
				res("aws_s3_bucket_versioning", "aws_s3_bucket_versioning.b", "b", map[string]string{"name": "b"}),
			},
			want: map[string]string{
				"aws_s3_bucket_versioning.b": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolveParentAddresses(tt.in)
			got := parentAddrByAddress(tt.in)
			for addr, wantParent := range tt.want {
				assert.Equal(t, wantParent, got[addr], "ParentAddress of %s", addr)
			}
		})
	}
}

// TestResolveParentAddresses_NoPanicOnTrivialInput pins that the
// resolver tolerates empty and single-element sets (the < 2 short
// circuit) without panicking.
func TestResolveParentAddresses_NoPanicOnTrivialInput(t *testing.T) {
	t.Parallel()
	resolveParentAddresses(nil)
	resolveParentAddresses([]imported.ImportedResource{})
	one := []imported.ImportedResource{res("aws_s3_bucket_versioning", "aws_s3_bucket_versioning.x", "x", map[string]string{"bucket": "x"})}
	resolveParentAddresses(one)
	assert.Equal(t, "", one[0].Identity.ParentAddress, "a lone child cannot resolve a parent")
}

// TestParentFK_AgreesWithLabelsRegistry pins that every instance-level
// foreign-key edge names the same parent type as the type-level
// pkg/imported/labels parentTfType registry. Without this the two halves
// of the parent/child model could silently disagree.
func TestParentFK_AgreesWithLabelsRegistry(t *testing.T) {
	t.Parallel()
	for child, fk := range parentFKByChildType {
		parent, ok := labels.ParentTfType(child)
		require.Truef(t, ok, "%q has an FK rule but is not in the labels parentTfType registry", child)
		assert.Equalf(t, parent, fk.parentType,
			"%q FK rule names parent %q; labels registry says %q", child, fk.parentType, parent)
		// Exactly one join direction must be set.
		hasChildKey := fk.childKey != ""
		hasParentKey := fk.parentKey != ""
		assert.Truef(t, hasChildKey != hasParentKey,
			"%q must set exactly one of childKey/parentKey (got childKey=%q parentKey=%q)", child, fk.childKey, fk.parentKey)
	}
}

// fkContractFixture is a representative discovery payload for one
// resource type, used to drive the production Cloud Control config
// closure in TestParentFK_DiscovererEmitsForeignKey.
type fkContractFixture struct {
	identifier string
	props      map[string]any
}

// fkContractFixtures supplies, per Cloud Control resource type the #650
// resolver depends on, an identifier + properties payload shaped like
// what the AWS API returns. The contract test feeds these through the
// production NativeIDsFromProperties closure and asserts the
// foreign-key NativeIDs entry comes out — so a config refactor that
// drops a parent-reference extractor fails CI here instead of silently
// emptying ParentAddress at customer deploy time.
var fkContractFixtures = map[string]fkContractFixture{
	// Forward-edge children: the child's own config carries the FK.
	"aws_route_table":                     {identifier: "rtb-1", props: map[string]any{"VpcId": "vpc-1"}},
	"aws_subnet":                          {identifier: "subnet-1", props: map[string]any{"VpcId": "vpc-1"}},
	"aws_vpc_security_group_ingress_rule": {identifier: "sgr-1", props: map[string]any{"GroupId": "sg-1"}},
	"aws_vpc_security_group_egress_rule":  {identifier: "sgr-2", props: map[string]any{"GroupId": "sg-1"}},
	"aws_kms_alias":                       {identifier: "alias/app", props: map[string]any{"TargetKeyId": "key-1"}},
	"aws_cloudwatch_log_stream":           {identifier: "log-group|log-stream", props: nil},
	"aws_iam_role_policy":                 {identifier: "policy-name|role-name", props: nil},
	"aws_s3_bucket_policy":                {identifier: "my-bucket", props: nil},
	// Reverse-edge parents: the parent's config carries the FK that
	// points back at the parameter-group child.
	"aws_db_instance":                   {identifier: "prod-db", props: map[string]any{"DBParameterGroupName": "prod-pg"}},
	"aws_elasticache_replication_group": {identifier: "redis-rg", props: map[string]any{"CacheParameterGroupName": "redis-pg"}},
}

// sdkOnlyFKChildren are forward-edge child types discovered by the
// SDK-only sub-resource pipeline rather than Cloud Control. Their
// foreign-key NativeIDs entry cannot be driven through a
// cloudControlConfig closure (the fetch funcs need a live/fake SDK
// client), so the discoverer↔resolver contract for these is pinned by
// their own discoverer tests (sdkonly_s3_test.go asserts NativeIDs
// ["bucket"]; the IAM role-policy-attachment test asserts NativeIDs
// ["role"]). They are listed here so TestParentFK_DiscovererEmitsForeignKey
// can account for every forward edge and skip exactly these.
var sdkOnlyFKChildren = map[string]struct{}{
	"aws_s3_bucket_versioning":                           {},
	"aws_s3_bucket_lifecycle_configuration":              {},
	"aws_s3_bucket_ownership_controls":                   {},
	"aws_s3_bucket_public_access_block":                  {},
	"aws_s3_bucket_server_side_encryption_configuration": {},
	"aws_iam_role_policy_attachment":                     {},
}

// TestParentFK_DiscovererEmitsForeignKey pins the discoverer↔resolver
// contract: for every parent/child edge, the resource type that is
// supposed to carry the foreign key actually emits it through its
// production discoverer config. Without this, a refactor that drops a
// NativeIDsFromProperties extractor would leave the FK registry pointing
// at a key the discoverer no longer produces — ParentAddress would go
// silently empty and only the resolver's synthetic-input unit tests
// (which never exercise the real config) would still pass.
func TestParentFK_DiscovererEmitsForeignKey(t *testing.T) {
	t.Parallel()
	for child, fk := range parentFKByChildType {
		// The type whose config carries the FK, and the NativeIDs key
		// it must emit: the child itself for a forward edge, the parent
		// for a reverse edge.
		typ, key := child, fk.childKey
		if fk.parentKey != "" {
			typ, key = fk.parentType, fk.parentKey
		}
		if _, sdkOnly := sdkOnlyFKChildren[child]; sdkOnly {
			// Forward child discovered by the SDK-only pipeline; its
			// FK is pinned by the discoverer's own test (see comment
			// on sdkOnlyFKChildren).
			continue
		}
		t.Run(child, func(t *testing.T) {
			t.Parallel()
			fx, ok := fkContractFixtures[typ]
			require.Truef(t, ok, "no fkContractFixtures entry for %q (needed to verify the %q edge)", typ, child)
			cfg := configByTFType(t, typ)
			require.NotNilf(t, cfg.NativeIDsFromProperties,
				"%q has no NativeIDsFromProperties extractor; the #650 resolver expects NativeIDs[%q]", typ, key)
			native := cfg.NativeIDsFromProperties(fx.identifier, fx.props)
			assert.NotEmptyf(t, native[key],
				"%q discoverer must emit NativeIDs[%q] for the %q parent-instance edge; got %+v", typ, key, child, native)
		})
	}
}

// TestParentFK_CoversLabelsRegistry pins that every child type in the
// labels parentTfType registry is accounted for here — either with a
// resolvable FK rule or as a documented, deliberate omission in
// unresolvableChildTypes. A new labels edge with no entry in either map
// fails this test loudly instead of silently shipping children with no
// parent reference.
func TestParentFK_CoversLabelsRegistry(t *testing.T) {
	t.Parallel()
	for _, child := range labels.ChildTfTypes() {
		_, resolvable := parentFKByChildType[child]
		_, unresolvable := unresolvableChildTypes[child]
		assert.Truef(t, resolvable || unresolvable,
			"labels child %q has neither an FK rule nor an unresolvableChildTypes entry", child)
		assert.Falsef(t, resolvable && unresolvable,
			"labels child %q is in both parentFKByChildType and unresolvableChildTypes", child)
	}
	// Every unresolvable entry must itself be a real labels child.
	for child := range unresolvableChildTypes {
		assert.Truef(t, labels.HasParent(child),
			"unresolvableChildTypes lists %q which is not a labels child type", child)
	}
}
