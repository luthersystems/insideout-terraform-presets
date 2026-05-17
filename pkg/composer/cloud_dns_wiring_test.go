package composer

// cloud_dns_wiring_test.go covers the issue #593 composer wiring for
// the gcp/cloud_dns preset:
//
//   - ComponentKey + PresetKeyMap + ModulePath + AllComponentKeys +
//     ComposeOrder registry entries are exercised by
//     TestAllComponentKeysCoversPresetKeyMap and
//     TestMapperKeysSubsetOfModuleVariables (both in sibling files).
//   - Default mapper provides every required variable — exercised by
//     TestEveryRequiredVariableIsMappedOrWired.
//
// Cloud DNS has no cross-module wiring today (no GCP-side equivalent
// of ACM auto-feed, no LB alias auto-derivation), so the wiring
// contract is "DefaultWiring is inert for KeyGCPCloudDNS." The tests
// below pin that contract so a future PR adding wiring (e.g. feeding
// gcp_loadbalancer's address into cloud_dns.records) lands deliberately
// rather than by silent regression.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultWiring_GCPCloudDNS_InertStandalone(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyGCPCloudDNS: true,
	}
	wi := DefaultWiring(selected, KeyGCPCloudDNS, &Components{})

	require.Empty(t, wi.Names,
		"GCP Cloud DNS has no cross-module wiring today; DefaultWiring should be inert (got Names=%v)",
		wi.Names)
	require.Empty(t, wi.RawHCL,
		"GCP Cloud DNS has no cross-module wiring today; DefaultWiring should not emit any RawHCL (got %v)",
		wi.RawHCL)
}

func TestDefaultWiring_GCPCloudDNS_InertWithSiblings(t *testing.T) {
	t.Parallel()

	// Even with the full GCP catalog selected, Cloud DNS must stay
	// inert until a follow-up PR explicitly wires it.
	selected := map[ComponentKey]bool{
		KeyGCPCloudDNS:     true,
		KeyGCPVPC:          true,
		KeyGCPLoadbalancer: true,
		KeyGCPCloudRun:     true,
		KeyGCPCompute:      true,
	}
	wi := DefaultWiring(selected, KeyGCPCloudDNS, &Components{})

	require.Empty(t, wi.Names,
		"Cloud DNS should remain inert until a follow-up PR adds wiring (got Names=%v)",
		wi.Names)
}

// TestMapper_GCPCloudDNS_DefaultDNSName verifies the mapper supplies a
// placeholder dns_name when the caller hasn't provided one. dns_name
// is required by the preset (no default), so without a mapper fallback
// every single-module preview / kitchen-sink test would fail with
// `missing_required_variable`.
func TestMapper_GCPCloudDNS_DefaultDNSName(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPCloudDNS, &Components{}, &Config{}, "demo", "us-central1")
	require.NoError(t, err)

	dns, ok := vals["dns_name"]
	require.True(t, ok, "mapper must always set dns_name (preset has no default)")
	require.Equal(t, "example.invalid.", dns,
		"mapper should fall back to example.invalid. (with trailing dot) when cfg.GCPCloudDNS.DNSName is unset; .invalid is the IANA-reserved TLD for testing")
}

// TestMapper_GCPCloudDNS_CallerSuppliedConfig pins the per-field
// mapper plumbing.
func TestMapper_GCPCloudDNS_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	tr, fa := true, false
	cfg := &Config{
		GCPCloudDNS: &struct {
			DNSName          string   `json:"dnsName,omitempty"`
			CreateZone       *bool    `json:"createZone,omitempty"`
			ZoneShortName    string   `json:"zoneShortName,omitempty"`
			ZoneName         string   `json:"zoneName,omitempty"`
			PrivateZone      *bool    `json:"privateZone,omitempty"`
			NetworkSelfLinks []string `json:"networkSelfLinks,omitempty"`
			ForceDestroy     *bool    `json:"forceDestroy,omitempty"`
		}{
			DNSName:          "example.com.",
			CreateZone:       &tr,
			ZoneShortName:    "primary",
			ZoneName:         "example-com",
			PrivateZone:      &fa,
			NetworkSelfLinks: []string{"projects/p/global/networks/n"},
			ForceDestroy:     &tr,
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPCloudDNS, &Components{}, cfg, "demo", "us-central1")
	require.NoError(t, err)

	require.Equal(t, "example.com.", vals["dns_name"])
	require.Equal(t, true, vals["create_zone"])
	require.Equal(t, "primary", vals["zone_short_name"])
	require.Equal(t, "example-com", vals["zone_name"])
	require.Equal(t, false, vals["private_zone"])
	require.Equal(t, []any{"projects/p/global/networks/n"}, vals["network_self_links"])
	require.Equal(t, true, vals["force_destroy"])
}

// TestComposeStack_GCPCloudDNSStandalone confirms ComposeStack succeeds
// when Cloud DNS is the only component — the mapper must supply a
// placeholder dns_name (preset's variable has no default).
func TestComposeStack_GCPCloudDNSStandalone(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPCloudDNS},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "test",
		Region:       "us-central1",
		GCPProjectID: "test-project-12345",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok, "composed root must contain main.tf")
	rootStr := string(root)

	require.Contains(t, rootStr, `module "gcp_cloud_dns"`,
		"composed root must declare module gcp_cloud_dns")

	// Confirm the tfvars file landed with the placeholder dns_name.
	tfvars, ok := out["/gcp_cloud_dns.auto.tfvars"]
	require.True(t, ok, "expected gcp_cloud_dns.auto.tfvars")
	require.Contains(t, string(tfvars), "example.invalid.",
		"standalone Cloud DNS should land the placeholder dns_name so terraform plan can compile")
}
