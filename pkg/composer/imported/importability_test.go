package imported

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestReasonCodes_WireStable pins the literal reason-code strings. These are
// cross-repo wire identifiers (carried in unsupported.json's `reason` field and
// consumed by reliable#1967); renaming one is a wire-format break that must
// surface as a deliberate test diff per the "bump the reliable consumer in
// lockstep" contract, not slip through because every comparison reads the const
// symbol on both sides.
func TestReasonCodes_WireStable(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_managed_kms_alias", ReasonAWSManagedKMSAlias)
	assert.Equal(t, "aws_managed_kms_key", ReasonAWSManagedKMSKey)
	assert.Equal(t, "service_managed_eni", ReasonServiceManagedENI)
	assert.Equal(t, "service_managed", ReasonServiceManaged)
	assert.Equal(t, "ephemeral_log_stream", ReasonEphemeralLogStream)
	assert.Equal(t, "insideout_imported", ReasonInsideOutImported)
}

func TestIsAWSManagedKMSAliasName(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"alias/aws/rds":      true,
		"alias/aws/ebs":      true,
		"alias/aws/dynamodb": true,
		"alias/my-app-key":   false,
		"alias/aws":          false, // no trailing slash — not the reserved namespace
		"":                   false,
		"aws/rds":            false,
	}
	for name, want := range cases {
		assert.Equalf(t, want, IsAWSManagedKMSAliasName(name), "IsAWSManagedKMSAliasName(%q)", name)
	}
}

func TestIsAWSManagedKMSKeyManager(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"AWS":      true,
		"CUSTOMER": false,
		"":         false, // not surfaced by the discoverer → importable
		"aws":      false, // case-sensitive: KMS reports the literal "AWS"
	}
	for km, want := range cases {
		assert.Equalf(t, want, IsAWSManagedKMSKeyManager(km), "IsAWSManagedKMSKeyManager(%q)", km)
	}
}

func TestUnimportableReason_KMSKey(t *testing.T) {
	t.Parallel()
	t.Run("AWS-managed key (KeyManager=AWS) is un-importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:      "aws_kms_key",
			NativeIDs: map[string]string{"key_manager": "AWS"},
		}}
		assert.Equal(t, ReasonAWSManagedKMSKey, UnimportableReason(ir))
	})
	t.Run("customer-managed key (KeyManager=CUSTOMER) is importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:      "aws_kms_key",
			NativeIDs: map[string]string{"key_manager": "CUSTOMER"},
		}}
		assert.Equal(t, "", UnimportableReason(ir))
	})
	t.Run("absent key_manager is importable (discriminator not surfaced; genconfig backstop covers it)", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:      "aws_kms_key",
			NativeIDs: map[string]string{"arn": "arn:aws:kms:us-east-1:111111111111:key/1234abcd"},
		}}
		assert.Equal(t, "", UnimportableReason(ir))
	})
}

func TestUnimportableReason_ServiceManaged(t *testing.T) {
	t.Parallel()
	t.Run("EventBridge rule with ManagedBy is un-importable", func(t *testing.T) {
		// AutoScalingManagedRule: AWS stamps ManagedBy; tag/PutRule/DeleteRule
		// are all rejected (ManagedRuleException), so the rule cannot be managed
		// by Terraform at all (#785).
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:             "aws_cloudwatch_event_rule",
			NameHint:         "AutoScalingManagedRule",
			ServiceManagedBy: "autoscaling.amazonaws.com",
		}}
		assert.Equal(t, ReasonServiceManaged, UnimportableReason(ir))
	})
	t.Run("rule without ManagedBy is importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:     "aws_cloudwatch_event_rule",
			NameHint: "my-app-rule",
		}}
		assert.Equal(t, "", UnimportableReason(ir))
	})
	t.Run("marker is type-agnostic — any type with ServiceManagedBy is un-importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:             "aws_some_future_type",
			ServiceManagedBy: "service.amazonaws.com",
		}}
		assert.Equal(t, ReasonServiceManaged, UnimportableReason(ir))
	})
}

func TestIsServiceManaged(t *testing.T) {
	t.Parallel()
	assert.True(t, IsServiceManaged(ResourceIdentity{ServiceManagedBy: "autoscaling.amazonaws.com"}))
	assert.True(t, IsServiceManaged(ResourceIdentity{ServiceManagedBy: "  spaces-trimmed  "}))
	assert.False(t, IsServiceManaged(ResourceIdentity{}))
	assert.False(t, IsServiceManaged(ResourceIdentity{ServiceManagedBy: "   "}))
}

func TestIsServiceManagedENIInterfaceType(t *testing.T) {
	t.Parallel()
	// Importable (customer-owned / absent) interface types.
	for _, it := range []string{"", "interface", "efa", "efa-only", "branch", "trunk"} {
		assert.Falsef(t, IsServiceManagedENIInterfaceType(it), "interface_type %q should be importable", it)
	}
	// Service-managed (parent-owned) interface types — un-importable. Includes
	// a fabricated future value to pin the forward-compatible default.
	for _, it := range []string{"nat_gateway", "vpc_endpoint", "network_load_balancer", "lambda", "transit_gateway", "some_future_managed_type"} {
		assert.Truef(t, IsServiceManagedENIInterfaceType(it), "interface_type %q should be service-managed", it)
	}
}

func TestHasInsideOutImportedMarker(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		tags map[string]string
		want bool
	}{
		"aws imported marker present": {
			tags: map[string]string{awsTagKeyImported: "true"},
			want: true,
		},
		"aws imported marker presence is enough": {
			tags: map[string]string{awsTagKeyImported: ""},
			want: true,
		},
		"aws imported marker match is case-insensitive": {
			tags: map[string]string{"insideoutimported": "true"},
			want: true,
		},
		"gcp imported marker present": {
			tags: map[string]string{gcpLabelKeyImported: "true"},
			want: true,
		},
		"bare aws import project account id is not ownership": {
			tags: map[string]string{awsTagKeyImportProject: "123456789012"},
			want: false,
		},
		"bare gcp import project label is not ownership": {
			tags: map[string]string{gcpLabelKeyImportProject: "customer-project"},
			want: false,
		},
		"no tags": {
			tags: map[string]string{},
			want: false,
		},
		"not fetched": {
			tags: nil,
			want: false,
		},
	}

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, HasInsideOutImportedMarker(tc.tags))
		})
	}
}

func TestUnimportableReason_InsideOutImportedMarker(t *testing.T) {
	t.Parallel()
	t.Run("aws marker makes otherwise importable resource un-importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type: "aws_vpc",
			Tags: map[string]string{awsTagKeyImported: "true"},
		}}
		assert.Equal(t, ReasonInsideOutImported, UnimportableReason(ir))
	})
	t.Run("gcp marker makes otherwise importable resource un-importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type: "google_storage_bucket",
			Tags: map[string]string{gcpLabelKeyImported: "true"},
		}}
		assert.Equal(t, ReasonInsideOutImported, UnimportableReason(ir))
	})
	t.Run("bare import project account id stays importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type: "aws_vpc",
			Tags: map[string]string{awsTagKeyImportProject: "123456789012"},
		}}
		assert.Equal(t, "", UnimportableReason(ir))
	})
}

func TestUnimportableReason_KMSAlias(t *testing.T) {
	t.Parallel()
	// AWS-managed alias, name carried via each of the three identity surfaces
	// the discoverer may populate, to prove the fallback order.
	t.Run("via NativeIDs name", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:      "aws_kms_alias",
			NativeIDs: map[string]string{"name": "alias/aws/rds"},
		}}
		assert.Equal(t, ReasonAWSManagedKMSAlias, UnimportableReason(ir))
	})
	t.Run("via ImportID", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:     "aws_kms_alias",
			ImportID: "alias/aws/ebs",
		}}
		assert.Equal(t, ReasonAWSManagedKMSAlias, UnimportableReason(ir))
	})
	t.Run("via NameHint", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:     "aws_kms_alias",
			NameHint: "alias/aws/dynamodb",
		}}
		assert.Equal(t, ReasonAWSManagedKMSAlias, UnimportableReason(ir))
	})
	t.Run("customer alias is importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:      "aws_kms_alias",
			NativeIDs: map[string]string{"name": "alias/my-app"},
			ImportID:  "alias/my-app",
		}}
		assert.Equal(t, "", UnimportableReason(ir))
	})
}

func TestUnimportableReason_ENI(t *testing.T) {
	t.Parallel()
	t.Run("service-managed interface_type", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:      "aws_network_interface",
			NativeIDs: map[string]string{"id": "eni-abc", "interface_type": "nat_gateway"},
		}}
		assert.Equal(t, ReasonServiceManagedENI, UnimportableReason(ir))
	})
	t.Run("standard interface_type is importable", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:      "aws_network_interface",
			NativeIDs: map[string]string{"id": "eni-abc", "interface_type": "interface"},
		}}
		assert.Equal(t, "", UnimportableReason(ir))
	})
	t.Run("absent interface_type is importable (genconfig backstop covers it)", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:      "aws_network_interface",
			NativeIDs: map[string]string{"id": "eni-abc"},
		}}
		assert.Equal(t, "", UnimportableReason(ir))
	})
}

func TestUnimportableReason_LogStream(t *testing.T) {
	t.Parallel()
	t.Run("every log stream is un-importable (type-level)", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{
			Type:     "aws_cloudwatch_log_stream",
			ImportID: "/aws/rds/instance/db/postgresql:db.0",
		}}
		assert.Equal(t, ReasonEphemeralLogStream, UnimportableReason(ir))
	})
	t.Run("reason carries an operator description", func(t *testing.T) {
		assert.NotEmpty(t, ReasonDescription(ReasonEphemeralLogStream))
	})
}

func TestUnimportableReason_OtherTypesImportable(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"aws_vpc", "aws_s3_bucket", "aws_kms_key", "aws_lambda_function"} {
		ir := ImportedResource{Identity: ResourceIdentity{Type: typ, ImportID: "alias/aws/rds"}}
		assert.Equalf(t, "", UnimportableReason(ir), "type %q must never be classified un-importable by this predicate", typ)
	}
}

func TestReasonDescription(t *testing.T) {
	t.Parallel()
	// Pin a distinctive phrase per code (not just non-empty) so a regression
	// that swaps the two case bodies — or truncates a description — fails.
	kms := ReasonDescription(ReasonAWSManagedKMSAlias)
	assert.Truef(t, strings.Contains(kms, "alias/aws/"), "KMS-alias description must mention the reserved prefix, got %q", kms)
	kmsKey := ReasonDescription(ReasonAWSManagedKMSKey)
	assert.Truef(t, strings.Contains(kmsKey, "KeyManager"), "KMS-key description must mention KeyManager, got %q", kmsKey)
	eni := ReasonDescription(ReasonServiceManagedENI)
	assert.Truef(t, strings.Contains(eni, "network interface"), "ENI description must mention network interface, got %q", eni)
	insideOut := ReasonDescription(ReasonInsideOutImported)
	assert.Truef(t, strings.Contains(insideOut, "InsideOut"), "InsideOut-imported description must mention InsideOut, got %q", insideOut)
	svcManaged := ReasonDescription(ReasonServiceManaged)
	assert.Truef(t, strings.Contains(svcManaged, "Service-managed"), "service-managed description must mention Service-managed, got %q", svcManaged)
	assert.NotEqual(t, eni, svcManaged, "the ENI and generic service-managed descriptions must be distinct")
	assert.NotEqual(t, kms, eni, "the two descriptions must be distinct (guards against swapped case bodies)")
	assert.NotEqual(t, kms, kmsKey, "the KMS-alias and KMS-key descriptions must be distinct")
	assert.NotEqual(t, insideOut, kms, "the InsideOut-imported and KMS-alias descriptions must be distinct")
	assert.Equal(t, "", ReasonDescription("unknown_code"))
}
