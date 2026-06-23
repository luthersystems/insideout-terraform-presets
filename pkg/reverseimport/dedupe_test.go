package reverseimport

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestDedupeByAddress is the source-of-truth guard for the prod reverse-import
// failure (session sess_v2_Jok8JjJhzJER): a duplicate-address resource must be
// dropped from the canonical set so it appears once in imported.json (the
// reliable-persisted/counted set) and once in /imported.tf — never producing
// Terraform's "Duplicate import configuration" error or a phantom UI count.
func TestDedupeByAddress(t *testing.T) {
	t.Parallel()

	mk := func(addr, importID string) imported.ImportedResource {
		return imported.ImportedResource{Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_iam_role_policy_attachment",
			Address:  addr,
			ImportID: importID,
		}}
	}
	const dupAddr = "aws_iam_role_policy_attachment.arn_aws_iam_aws_policy_administratoraccess_1e6a3817"

	in := []imported.ImportedResource{
		mk("aws_iam_role.platform_test_admin", "platform-test-admin"),
		mk(dupAddr, "platform-test-admin/arn:aws:iam::aws:policy/AdministratorAccess"),
		mk(dupAddr, "platform-test-admin/arn:aws:iam::aws:policy/AdministratorAccess"),
	}

	out := dedupeByAddress(in)
	require.Len(t, out, 2, "duplicate address must collapse to one entry")
	require.Equal(t, "aws_iam_role.platform_test_admin", out[0].Identity.Address, "first-occurrence order preserved")
	require.Equal(t, dupAddr, out[1].Identity.Address)
}

// TestDedupeByAddress_KeepsEmptyAddresses ensures address-less resources (which
// can't be keyed) are all preserved rather than collapsed onto each other.
func TestDedupeByAddress_KeepsEmptyAddresses(t *testing.T) {
	t.Parallel()

	in := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Cloud: "aws", ImportID: "a"}},
		{Identity: imported.ResourceIdentity{Cloud: "aws", ImportID: "b"}},
	}
	out := dedupeByAddress(in)
	require.Len(t, out, 2, "empty-address resources must not collapse")
}
