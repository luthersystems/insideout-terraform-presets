package awsdiscover

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/dependencies"
)

// fk_native_ids_test.go pins the presets#733 change: the Cloud Control
// extractors lift cross-reference foreign-key fields into
// Identity.NativeIDs at discover time, under the dependencies.FieldRefs()
// key, so the picker's free-form dependency closure (lambda→iam_role,
// *→kms_key, …) is derivable with NO EnrichAttributes call.

// TestFKLift_PerType pins that each source type's NativeIDsFromProperties
// extractor lifts the expected FK field, given a representative Cloud
// Control GetResource properties payload. The CFN property names are the
// authoritative schema names (verified against the public CloudFormation
// type schemas) — a rename in AWS's schema that we don't track here would
// fail these pins.
func TestFKLift_PerType(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123456789012:role/exec"
	kmsARN := "arn:aws:kms:us-east-1:123456789012:key/abcd-1234"

	cases := []struct {
		tfType     string
		identifier string
		props      map[string]any
		wantKey    string
		wantVal    string
	}{
		{
			tfType: "aws_lambda_function", identifier: "fn",
			props:   map[string]any{"Arn": "arn:aws:lambda:us-east-1:1:function:fn", "Role": roleARN},
			wantKey: "role_arn", wantVal: roleARN,
		},
		{
			tfType: "aws_lambda_function", identifier: "fn",
			props:   map[string]any{"Arn": "arn:aws:lambda:us-east-1:1:function:fn", "KmsKeyArn": kmsARN},
			wantKey: "kms_key_arn", wantVal: kmsARN,
		},
		{
			tfType: "aws_sqs_queue", identifier: "https://sqs/q",
			props:   map[string]any{"Arn": "arn:aws:sqs:us-east-1:1:q", "KmsMasterKeyId": "alias/aws/sqs"},
			wantKey: "kms_master_key_id", wantVal: "alias/aws/sqs",
		},
		{
			tfType: "aws_sns_topic", identifier: "arn:aws:sns:us-east-1:1:t",
			props:   map[string]any{"KmsMasterKeyId": kmsARN},
			wantKey: "kms_master_key_id", wantVal: kmsARN,
		},
		{
			tfType: "aws_dynamodb_table", identifier: "t",
			props:   map[string]any{"Arn": "arn:aws:dynamodb:us-east-1:1:table/t", "SSESpecification": map[string]any{"KMSMasterKeyId": kmsARN}},
			wantKey: "kms_key_arn", wantVal: kmsARN,
		},
		{
			tfType: "aws_db_instance", identifier: "db",
			props:   map[string]any{"DBInstanceArn": "arn:aws:rds:us-east-1:1:db:db", "KmsKeyId": kmsARN},
			wantKey: "kms_key_id", wantVal: kmsARN,
		},
		{
			tfType: "aws_secretsmanager_secret", identifier: "arn:aws:secretsmanager:us-east-1:1:secret:s",
			props:   map[string]any{"KmsKeyId": kmsARN},
			wantKey: "kms_key_id", wantVal: kmsARN,
		},
		{
			tfType: "aws_cloudwatch_log_group", identifier: "/app",
			props:   map[string]any{"Arn": "arn:aws:logs:us-east-1:1:log-group:/app", "KmsKeyId": kmsARN},
			wantKey: "kms_key_id", wantVal: kmsARN,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.tfType+"/"+tc.wantKey, func(t *testing.T) {
			t.Parallel()
			cfg := configByTFType(t, tc.tfType)
			if cfg.NativeIDsFromProperties == nil {
				t.Fatalf("%s has no NativeIDsFromProperties extractor", tc.tfType)
			}
			got := cfg.NativeIDsFromProperties(tc.identifier, tc.props)
			if got[tc.wantKey] != tc.wantVal {
				t.Fatalf("%s NativeIDs[%q] = %q, want %q (got map %v)", tc.tfType, tc.wantKey, got[tc.wantKey], tc.wantVal, got)
			}
			// The lifted key must be a real FieldRefs() cross-ref field —
			// otherwise the closure resolver would never read it.
			if _, ok := dependencies.Lookup(tc.wantKey); !ok {
				t.Fatalf("lifted NativeIDs key %q is not a dependencies.FieldRefs() field", tc.wantKey)
			}
		})
	}
}

// TestFKLift_AbsentSafe pins that the FK lift never breaks the base
// extractor when the FK property is absent: the canonical identifiers are
// still produced and no empty FK key is stamped.
func TestFKLift_AbsentSafe(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_lambda_function")
	got := cfg.NativeIDsFromProperties("fn", map[string]any{"Arn": "arn:aws:lambda:us-east-1:1:function:fn"})
	if got["arn"] != "arn:aws:lambda:us-east-1:1:function:fn" {
		t.Fatalf("base arn lost: %v", got)
	}
	if _, ok := got["role_arn"]; ok {
		t.Fatalf("role_arn stamped despite absent Role property: %v", got)
	}
	if _, ok := got["kms_key_arn"]; ok {
		t.Fatalf("kms_key_arn stamped despite absent KmsKeyArn property: %v", got)
	}
}

// TestFKLift_DoesNotClobberBase pins first-writer-wins: a base extractor's
// canonical id under a key is never overwritten by an FK lift sharing that
// key (defensive — none of the wired pairs collide today).
func TestFKLift_DoesNotClobberBase(t *testing.T) {
	t.Parallel()
	base := func(_ string, _ map[string]any) map[string]string {
		return map[string]string{"kms_key_id": "base-wins"}
	}
	ext := fkNativeIDs(base, fkRef{cfnProp: "KmsKeyId", nativeKey: "kms_key_id"})
	got := ext("x", map[string]any{"KmsKeyId": "fk-loses"})
	if got["kms_key_id"] != "base-wins" {
		t.Fatalf("fk lift clobbered base: %v", got)
	}
}

// TestDiscoverOnlyClosure_NoAttrs is the end-to-end parity proof: an IR
// set with EMPTY Attrs — exactly what discovery fast mode returns — still
// produces the full picker closure. Parent/child edges come from
// resolveParentAddresses (ParentAddress); free-form FK edges come from
// composer/imported.DependencyEdges (NativeIDs). Together they reproduce
// what reliable's Attrs-based path shows today.
func TestDiscoverOnlyClosure_NoAttrs(t *testing.T) {
	t.Parallel()

	roleARN := "arn:aws:iam::1:role/exec"
	kmsARN := "arn:aws:kms:us-east-1:1:key/k1"

	irs := []imported.ImportedResource{
		// Targets. res() builds a discover-only IR (Identity set, Attrs nil).
		res("aws_iam_role", "aws_iam_role.exec", "exec", map[string]string{"arn": roleARN}),
		res("aws_kms_key", "aws_kms_key.k", "k1", map[string]string{"arn": kmsARN}),
		res("aws_vpc", "aws_vpc.main", "vpc-1", map[string]string{"id": "vpc-1"}),
		res("aws_s3_bucket", "aws_s3_bucket.logs", "logs", map[string]string{"name": "logs"}),
		// Free-form FK source: lambda → iam_role + kms_key.
		res("aws_lambda_function", "aws_lambda_function.api", "api",
			map[string]string{"arn": "arn:aws:lambda:us-east-1:1:function:api", "role_arn": roleARN, "kms_key_arn": kmsARN}),
		// Parent/child source: subnet → vpc (vpc_id is BOTH a FieldRefs FK
		// and a parent/child FK).
		res("aws_subnet", "aws_subnet.web", "subnet-1", map[string]string{"id": "subnet-1", "vpc_id": "vpc-1"}),
		// Parent/child source: s3 sub-resource → bucket.
		res("aws_s3_bucket_versioning", "aws_s3_bucket_versioning.logs", "logs", map[string]string{"bucket": "logs"}),
	}

	// All Attrs are nil/empty — assert that before resolving.
	for _, r := range irs {
		if len(r.Attrs) != 0 {
			t.Fatalf("fixture has non-empty Attrs for %s — fast mode is Attrs-free", r.Identity.Address)
		}
	}

	// Parent/child closure (discover-time, no AWS calls).
	resolveParentAddresses(irs)
	parentByAddr := map[string]string{}
	for _, r := range irs {
		parentByAddr[r.Identity.Address] = r.Identity.ParentAddress
	}
	if parentByAddr["aws_subnet.web"] != "aws_vpc.main" {
		t.Errorf("subnet parent = %q, want aws_vpc.main", parentByAddr["aws_subnet.web"])
	}
	if parentByAddr["aws_s3_bucket_versioning.logs"] != "aws_s3_bucket.logs" {
		t.Errorf("versioning parent = %q, want aws_s3_bucket.logs", parentByAddr["aws_s3_bucket_versioning.logs"])
	}

	// Free-form FK closure (discover-time, no Attrs).
	edges := imported.DependencyEdges(irs)
	want := map[string][]string{
		"aws_lambda_function.api": {"aws_iam_role.exec", "aws_kms_key.k"},
		// vpc_id is a FieldRefs FK too, so the subnet→vpc edge surfaces here
		// as well — consistent with reliable's Attrs path (which also reads
		// vpc_id from Attrs). The picker dedupes against the parent edge.
		"aws_subnet.web": {"aws_vpc.main"},
	}
	if !mapsEqual(edges, want) {
		t.Fatalf("DependencyEdges = %v, want %v", edges, want)
	}
}

func mapsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
	}
	return true
}
