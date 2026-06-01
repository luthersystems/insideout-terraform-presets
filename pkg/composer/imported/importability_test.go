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
	assert.Equal(t, "service_managed_eni", ReasonServiceManagedENI)
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
	assert.Truef(t, strings.Contains(kms, "alias/aws/"), "KMS description must mention the reserved prefix, got %q", kms)
	eni := ReasonDescription(ReasonServiceManagedENI)
	assert.Truef(t, strings.Contains(eni, "network interface"), "ENI description must mention network interface, got %q", eni)
	assert.NotEqual(t, kms, eni, "the two descriptions must be distinct (guards against swapped case bodies)")
	assert.Equal(t, "", ReasonDescription("unknown_code"))
}
