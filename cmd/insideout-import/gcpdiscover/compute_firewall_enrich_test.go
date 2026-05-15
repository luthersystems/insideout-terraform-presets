package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	computev1 "google.golang.org/api/compute/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// compile-time: the enricher must satisfy BOTH interfaces.
var (
	_ AttributeEnricher = (*computeFirewallEnricher)(nil)
	_ ByIDEnricher      = (*computeFirewallEnricher)(nil)
)

// firewallIdentity is the standard Identity used by happy-path tests
// in this file. Mirrors what computeFirewallDiscoverer.FromAsset would
// produce.
func firewallIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:    "gcp",
		Type:     "google_compute_firewall",
		NameHint: "allow-ssh",
		Address:  "google_compute_firewall.allow_ssh",
		ImportID: "projects/my-project/global/firewalls/allow-ssh",
		NativeIDs: map[string]string{
			"name":       "allow-ssh",
			"asset_name": "//compute.googleapis.com/projects/my-project/global/firewalls/allow-ssh",
			"self_link":  "https://www.googleapis.com/compute/v1/projects/my-project/global/firewalls/allow-ssh",
		},
	}
}

// TestMapComputeFirewall_Minimal pins the smallest non-trivial shape:
// name + network only, with everything else absent.
func TestMapComputeFirewall_Minimal(t *testing.T) {
	t.Parallel()
	src := &computev1.Firewall{
		Name:    "allow-ssh",
		Network: "projects/my-project/global/networks/default",
	}
	got := mapComputeFirewall(src, "my-project")

	require.NotNil(t, got.Name)
	assert.Equal(t, "allow-ssh", *got.Name.Literal)
	require.NotNil(t, got.Network)
	assert.Equal(t, "projects/my-project/global/networks/default", *got.Network.Literal)
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)

	// All other fields untouched.
	assert.Nil(t, got.Description)
	assert.Nil(t, got.Direction)
	assert.Nil(t, got.Priority)
	assert.Nil(t, got.Disabled)
	assert.Nil(t, got.SourceRanges)
	assert.Nil(t, got.DestinationRanges)
	assert.Nil(t, got.SourceTags)
	assert.Nil(t, got.TargetTags)
	assert.Nil(t, got.SourceServiceAccounts)
	assert.Nil(t, got.TargetServiceAccounts)
	assert.Empty(t, got.Allow)
	assert.Empty(t, got.Deny)
	assert.Empty(t, got.LogConfig)
	assert.Nil(t, got.EnableLogging)
	assert.Nil(t, got.CreationTimestamp)
	assert.Nil(t, got.SelfLink)
	assert.Nil(t, got.Timeouts)
}

// TestMapComputeFirewall_AllowOnly covers a typical ingress allow rule
// (the most common firewall shape).
func TestMapComputeFirewall_AllowOnly(t *testing.T) {
	t.Parallel()
	src := &computev1.Firewall{
		Name:         "allow-ssh",
		Network:      "global/networks/default",
		Direction:    "INGRESS",
		Priority:     1000,
		SourceRanges: []string{"0.0.0.0/0"},
		TargetTags:   []string{"ssh-allowed"},
		Allowed: []*computev1.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"22"}},
		},
	}
	got := mapComputeFirewall(src, "my-project")

	require.NotNil(t, got.Direction)
	assert.Equal(t, "INGRESS", *got.Direction.Literal)
	require.NotNil(t, got.Priority)
	assert.Equal(t, float64(1000), *got.Priority.Literal,
		"engine must cast int64 API priority to *Value[float64] (the typed schema's chosen shape)")
	require.Len(t, got.SourceRanges, 1)
	assert.Equal(t, "0.0.0.0/0", *got.SourceRanges[0].Literal)
	require.Len(t, got.TargetTags, 1)
	assert.Equal(t, "ssh-allowed", *got.TargetTags[0].Literal)

	require.Len(t, got.Allow, 1)
	require.NotNil(t, got.Allow[0].Protocol)
	assert.Equal(t, "tcp", *got.Allow[0].Protocol.Literal,
		"FirewallAllowed.IPProtocol must map to Allow.Protocol (TF rename)")
	require.Len(t, got.Allow[0].Ports, 1)
	assert.Equal(t, "22", *got.Allow[0].Ports[0].Literal)
	assert.Empty(t, got.Deny, "deny block must stay empty when no Denied rules")
}

// TestMapComputeFirewall_DenyOnly covers a deny rule + EGRESS
// direction + destination_ranges (the inverse-of-allow flow).
func TestMapComputeFirewall_DenyOnly(t *testing.T) {
	t.Parallel()
	src := &computev1.Firewall{
		Name:              "deny-egress-bad-ip",
		Network:           "global/networks/default",
		Direction:         "EGRESS",
		Priority:          500,
		DestinationRanges: []string{"10.0.0.0/8", "192.168.0.0/16"},
		Denied: []*computev1.FirewallDenied{
			{IPProtocol: "all"},
		},
	}
	got := mapComputeFirewall(src, "my-project")

	require.NotNil(t, got.Direction)
	assert.Equal(t, "EGRESS", *got.Direction.Literal)
	require.Len(t, got.DestinationRanges, 2)
	assert.Equal(t, "10.0.0.0/8", *got.DestinationRanges[0].Literal)
	assert.Equal(t, "192.168.0.0/16", *got.DestinationRanges[1].Literal)

	assert.Empty(t, got.Allow, "allow block must stay empty when no Allowed rules")
	require.Len(t, got.Deny, 1)
	require.NotNil(t, got.Deny[0].Protocol)
	assert.Equal(t, "all", *got.Deny[0].Protocol.Literal)
	assert.Nil(t, got.Deny[0].Ports, "Deny.Ports must be nil when SDK Ports is empty")
}

// TestMapComputeFirewall_MixedAllowDeny exercises the multi-rule
// translation: 2 allow rules + 1 deny rule, each with multiple ports.
func TestMapComputeFirewall_MixedAllowDeny(t *testing.T) {
	t.Parallel()
	src := &computev1.Firewall{
		Name:    "mixed-fw",
		Network: "global/networks/default",
		Allowed: []*computev1.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"80", "443"}},
			{IPProtocol: "udp", Ports: []string{"53"}},
		},
		Denied: []*computev1.FirewallDenied{
			{IPProtocol: "icmp"},
		},
	}
	got := mapComputeFirewall(src, "my-project")

	require.Len(t, got.Allow, 2)
	assert.Equal(t, "tcp", *got.Allow[0].Protocol.Literal)
	require.Len(t, got.Allow[0].Ports, 2)
	assert.Equal(t, "80", *got.Allow[0].Ports[0].Literal)
	assert.Equal(t, "443", *got.Allow[0].Ports[1].Literal)
	assert.Equal(t, "udp", *got.Allow[1].Protocol.Literal)
	require.Len(t, got.Allow[1].Ports, 1)
	assert.Equal(t, "53", *got.Allow[1].Ports[0].Literal)

	require.Len(t, got.Deny, 1)
	assert.Equal(t, "icmp", *got.Deny[0].Protocol.Literal)
}

// TestMapComputeFirewall_LogConfigEnabled pins the LogConfig split:
// FirewallLogConfig.Enable becomes top-level EnableLogging;
// FirewallLogConfig.Metadata becomes a log_config{} block.
func TestMapComputeFirewall_LogConfigEnabled(t *testing.T) {
	t.Parallel()
	src := &computev1.Firewall{
		Name:    "audit-fw",
		Network: "global/networks/default",
		LogConfig: &computev1.FirewallLogConfig{
			Enable:   true,
			Metadata: "INCLUDE_ALL_METADATA",
		},
	}
	got := mapComputeFirewall(src, "my-project")

	require.NotNil(t, got.EnableLogging,
		"FirewallLogConfig.Enable must become top-level enable_logging (not nested under log_config)")
	assert.True(t, *got.EnableLogging.Literal)
	require.Len(t, got.LogConfig, 1)
	require.NotNil(t, got.LogConfig[0].Metadata)
	assert.Equal(t, "INCLUDE_ALL_METADATA", *got.LogConfig[0].Metadata.Literal)
}

// TestMapComputeFirewall_LogConfigAbsent confirms the EnableLogging /
// LogConfig fields stay nil when the API returns no LogConfig at all.
func TestMapComputeFirewall_LogConfigAbsent(t *testing.T) {
	t.Parallel()
	src := &computev1.Firewall{
		Name:    "no-log-fw",
		Network: "global/networks/default",
	}
	got := mapComputeFirewall(src, "my-project")
	assert.Nil(t, got.EnableLogging)
	assert.Empty(t, got.LogConfig)
}

// TestMapComputeFirewall_LogConfigEnableOnly covers an enable-only
// LogConfig (no metadata). The TF top-level attr must populate, but the
// nested block stays empty — emitting `log_config {}` with all defaults
// is noise.
func TestMapComputeFirewall_LogConfigEnableOnly(t *testing.T) {
	t.Parallel()
	src := &computev1.Firewall{
		Name:      "enable-only-fw",
		Network:   "global/networks/default",
		LogConfig: &computev1.FirewallLogConfig{Enable: true},
	}
	got := mapComputeFirewall(src, "my-project")
	require.NotNil(t, got.EnableLogging)
	assert.True(t, *got.EnableLogging.Literal)
	assert.Empty(t, got.LogConfig, "no Metadata → no log_config{} block")
}

// TestMapComputeFirewall_ComputedRoundTrip populates computed-only
// fields (creation_timestamp, self_link) on the typed struct. The emit
// layer is responsible for dropping them based on Computed=true in
// GoogleComputeFirewallSchema.
func TestMapComputeFirewall_ComputedRoundTrip(t *testing.T) {
	t.Parallel()
	src := &computev1.Firewall{
		Name:              "fw",
		Network:           "global/networks/default",
		CreationTimestamp: "2024-01-02T03:04:05Z",
		SelfLink:          "https://www.googleapis.com/compute/v1/projects/p/global/firewalls/fw",
	}
	got := mapComputeFirewall(src, "p")
	require.NotNil(t, got.CreationTimestamp)
	assert.Equal(t, "2024-01-02T03:04:05Z", *got.CreationTimestamp.Literal)
	require.NotNil(t, got.SelfLink)
	assert.Contains(t, *got.SelfLink.Literal, "/firewalls/fw")
}

// Enricher contract tests --------------------------------------------

func TestComputeFirewallEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newComputeFirewallEnricher()
	ir := &imported.ImportedResource{Identity: firewallIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

// TestComputeFirewallEnrichByID_ClientUnavailable mirrors the
// AttributeEnricher contract on the ByID entry point.
func TestComputeFirewallEnrichByID_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newComputeFirewallEnricher().(ByIDEnricher)
	id := firewallIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{Compute: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Nil(t, raw)
}

func TestComputeFirewallEnrich_ProjectIDRequired(t *testing.T) {
	t.Parallel()
	e := computeFirewallEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _ string) (*computev1.Firewall, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: firewallIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EnrichClients.ProjectID required")
}

func TestComputeFirewallEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := computeFirewallEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _ string) (*computev1.Firewall, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_compute_firewall"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive firewall name")
}

func TestComputeFirewallEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("404 not found")
	var gotProject, gotName string
	e := computeFirewallEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, project, name string) (*computev1.Firewall, error) {
			gotProject, gotName = project, name
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: firewallIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "my-project/allow-ssh")
	assert.Equal(t, "my-project", gotProject, "fetch must receive ProjectID from EnrichClients, not Identity")
	assert.Equal(t, "allow-ssh", gotName)
}

// TestComputeFirewallEnrich_PopulatesAttrs is the happy path: a
// mixed-rule firewall flows through Enrich and decodes cleanly via
// generated.UnmarshalAttrs.
func TestComputeFirewallEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	fw := &computev1.Firewall{
		Name:         "allow-ssh",
		Network:      "projects/my-project/global/networks/default",
		Direction:    "INGRESS",
		Priority:     1000,
		SourceRanges: []string{"0.0.0.0/0"},
		TargetTags:   []string{"ssh-allowed"},
		Allowed: []*computev1.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"22"}},
		},
		LogConfig: &computev1.FirewallLogConfig{Enable: true, Metadata: "INCLUDE_ALL_METADATA"},
	}
	e := computeFirewallEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, project, name string) (*computev1.Firewall, error) {
			assert.Equal(t, "my-project", project)
			assert.Equal(t, "allow-ssh", name)
			return fw, nil
		},
	}
	ir := &imported.ImportedResource{Identity: firewallIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"}))

	decoded, err := generated.UnmarshalAttrs("google_compute_firewall", ir.Attrs)
	require.NoError(t, err)
	gf, ok := decoded.(*generated.GoogleComputeFirewall)
	require.True(t, ok)
	require.NotNil(t, gf.Name)
	assert.Equal(t, "allow-ssh", *gf.Name.Literal)
	require.NotNil(t, gf.Direction)
	assert.Equal(t, "INGRESS", *gf.Direction.Literal)
	require.Len(t, gf.Allow, 1)
	assert.Equal(t, "tcp", *gf.Allow[0].Protocol.Literal)
	require.NotNil(t, gf.EnableLogging)
	assert.True(t, *gf.EnableLogging.Literal)
	require.Len(t, gf.LogConfig, 1)
	assert.Equal(t, "INCLUDE_ALL_METADATA", *gf.LogConfig[0].Metadata.Literal)
}

// TestComputeFirewallEnrichByID_PopulatesAttrs validates the ByID
// entry point returns the same JSON shape ir.Attrs gets via Enrich.
// Shared fetchAndMap helper guarantees parity — this test is the
// black-box pin.
func TestComputeFirewallEnrichByID_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	fw := &computev1.Firewall{
		Name:    "allow-ssh",
		Network: "global/networks/default",
		Allowed: []*computev1.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"22"}},
		},
	}
	e := computeFirewallEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, project, name string) (*computev1.Firewall, error) {
			assert.Equal(t, "my-project", project)
			assert.Equal(t, "allow-ssh", name)
			return fw, nil
		},
	}
	id := firewallIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	// Result decodes through the same UnmarshalAttrs path that Enrich
	// would use.
	decoded, err := generated.UnmarshalAttrs("google_compute_firewall", raw)
	require.NoError(t, err)
	gf, ok := decoded.(*generated.GoogleComputeFirewall)
	require.True(t, ok)
	require.NotNil(t, gf.Name)
	assert.Equal(t, "allow-ssh", *gf.Name.Literal)
	require.Len(t, gf.Allow, 1)
	assert.Equal(t, "tcp", *gf.Allow[0].Protocol.Literal)
}

// TestComputeFirewallEnrichByID_NilIdentity guards against a nil
// pointer from a misbehaving caller (the dispatcher is responsible
// for non-nil, but defense-in-depth).
func TestComputeFirewallEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newComputeFirewallEnricher().(ByIDEnricher)
	raw, err := e.EnrichByID(context.Background(), nil, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil Identity")
}

// TestComputeFirewallEnrich_EnrichAndByIDProduceIdenticalAttrs locks
// in the contract that Enrich and EnrichByID, given the same inputs,
// emit byte-identical JSON. If a future change splits the two paths,
// this guards against silent divergence.
func TestComputeFirewallEnrich_EnrichAndByIDProduceIdenticalAttrs(t *testing.T) {
	t.Parallel()
	fw := &computev1.Firewall{
		Name:    "allow-ssh",
		Network: "global/networks/default",
		Allowed: []*computev1.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"22", "443"}},
		},
		Denied: []*computev1.FirewallDenied{
			{IPProtocol: "icmp"},
		},
		LogConfig: &computev1.FirewallLogConfig{Enable: true, Metadata: "INCLUDE_ALL_METADATA"},
	}
	mk := func() computeFirewallEnricher {
		return computeFirewallEnricher{
			fetch: func(_ context.Context, _ *computev1.Service, _, _ string) (*computev1.Firewall, error) {
				return fw, nil
			},
		}
	}
	c := EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"}

	ir := &imported.ImportedResource{Identity: firewallIdentity()}
	require.NoError(t, mk().Enrich(context.Background(), ir, c))

	id := firewallIdentity()
	raw, err := mk().EnrichByID(context.Background(), &id, c)
	require.NoError(t, err)

	// Compare JSON shapes structurally so a future field-order shuffle
	// in encoding/json doesn't false-fail. (json.Marshal is stable for
	// the same struct shape, but the structural compare is the load-
	// bearing assertion either way.)
	var a, b map[string]any
	require.NoError(t, json.Unmarshal(ir.Attrs, &a))
	require.NoError(t, json.Unmarshal(raw, &b))
	assert.Equal(t, a, b, "Enrich and EnrichByID must produce identical typed payloads")
}

// TestComputeFirewallShortNameForEnrich pins the Identity → name
// precedence: NameHint > NativeIDs["name"] > NativeIDs["asset_name"]
// > ImportID.
func TestComputeFirewallShortNameForEnrich(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   imported.ResourceIdentity
		want string
	}{
		{
			name: "name_hint_wins",
			id: imported.ResourceIdentity{
				NameHint:  "from-hint",
				NativeIDs: map[string]string{"name": "from-native"},
				ImportID:  "projects/p/global/firewalls/from-import",
			},
			want: "from-hint",
		},
		{
			name: "native_id_name_beats_asset",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{
					"name":       "from-native",
					"asset_name": "//compute.googleapis.com/projects/p/global/firewalls/from-asset",
				},
				ImportID: "projects/p/global/firewalls/from-import",
			},
			want: "from-native",
		},
		{
			name: "asset_name_beats_import_id",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{
					"asset_name": "//compute.googleapis.com/projects/p/global/firewalls/from-asset",
				},
				ImportID: "projects/p/global/firewalls/from-import",
			},
			want: "from-asset",
		},
		{
			name: "import_id_fallback",
			id: imported.ResourceIdentity{
				ImportID: "projects/p/global/firewalls/from-import",
			},
			want: "from-import",
		},
		{
			name: "nothing_yields_empty",
			id:   imported.ResourceIdentity{},
			want: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := tc.id
			assert.Equal(t, tc.want, computeFirewallShortNameForEnrich(&id))
		})
	}
}
