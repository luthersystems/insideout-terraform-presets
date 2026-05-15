package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	computev1 "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestMapComputeAddress_Minimal pins the smallest non-trivial mapping:
// only the Name field populated. Every Optional+Computed scalar must
// stay nil so the generated HCL surface matches "import block fills
// the computed-only fields on first refresh."
func TestMapComputeAddress_Minimal(t *testing.T) {
	t.Parallel()
	src := &computev1.Address{Name: "lb-ip"}
	got := mapComputeAddress(src, "my-project", "us-central1")

	require.NotNil(t, got.Name)
	assert.Equal(t, "lb-ip", *got.Name.Literal)
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)
	require.NotNil(t, got.Region)
	assert.Equal(t, "us-central1", *got.Region.Literal,
		"region must come from the argument (the value the discoverer extracted into Identity.Location), not from the b.Region URL")

	// Optional scalars stay nil on a basic address.
	assert.Nil(t, got.Address)
	assert.Nil(t, got.AddressType)
	assert.Nil(t, got.Description)
	assert.Nil(t, got.IpVersion)
	assert.Nil(t, got.IPV6EndpointType)
	assert.Nil(t, got.Labels)
	assert.Nil(t, got.Network)
	assert.Nil(t, got.NetworkTier)
	assert.Nil(t, got.PrefixLength)
	assert.Nil(t, got.Purpose)
	assert.Nil(t, got.Subnetwork)

	// Pure computed-only fields must NOT be populated (decision #5).
	assert.Nil(t, got.CreationTimestamp, "creation_timestamp is computed-only")
	assert.Nil(t, got.EffectiveLabels, "effective_labels is computed-only")
	assert.Nil(t, got.ID, "id is computed-only")
	assert.Nil(t, got.LabelFingerprint, "label_fingerprint is computed-only")
	assert.Nil(t, got.SelfLink, "self_link is computed-only")
	assert.Nil(t, got.TerraformLabels, "terraform_labels is computed-only")
	assert.Empty(t, got.Users, "users is computed-only")
	assert.Nil(t, got.Timeouts, "timeouts is a TF-only sentinel")
}

// TestMapComputeAddress_FullyPopulated covers every user-editable
// field. Includes the int64-to-float64 cast on prefix_length and the
// goog-managed-label filter.
func TestMapComputeAddress_FullyPopulated(t *testing.T) {
	t.Parallel()
	src := &computev1.Address{
		Address:          "10.0.0.5",
		AddressType:      "INTERNAL",
		Description:      "Internal LB front-end",
		IpVersion:        "IPV4",
		Ipv6EndpointType: "",
		Labels: map[string]string{
			"team":          "platform",
			"env":           "prod",
			"goog-managed":  "true",
			"goog_internal": "ignore-me",
		},
		Name:         "internal-lb-ip",
		Network:      "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/vpc-prod",
		NetworkTier:  "PREMIUM",
		PrefixLength: 29,
		Purpose:      "GCE_ENDPOINT",
		Subnetwork:   "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/private",
	}
	got := mapComputeAddress(src, "my-project", "us-central1")

	require.NotNil(t, got.Address)
	assert.Equal(t, "10.0.0.5", *got.Address.Literal)
	require.NotNil(t, got.AddressType)
	assert.Equal(t, "INTERNAL", *got.AddressType.Literal)
	require.NotNil(t, got.Description)
	assert.Equal(t, "Internal LB front-end", *got.Description.Literal)
	require.NotNil(t, got.IpVersion)
	assert.Equal(t, "IPV4", *got.IpVersion.Literal)
	require.NotNil(t, got.Network)
	assert.Contains(t, *got.Network.Literal, "vpc-prod")
	require.NotNil(t, got.NetworkTier)
	assert.Equal(t, "PREMIUM", *got.NetworkTier.Literal)
	require.NotNil(t, got.PrefixLength)
	assert.Equal(t, float64(29), *got.PrefixLength.Literal,
		"engine must cast int64 API prefix_length to *Value[float64] (the typed schema's chosen shape)")
	require.NotNil(t, got.Purpose)
	assert.Equal(t, "GCE_ENDPOINT", *got.Purpose.Literal)
	require.NotNil(t, got.Subnetwork)
	assert.Contains(t, *got.Subnetwork.Literal, "private")

	// goog-managed labels stripped, user labels preserved.
	require.NotNil(t, got.Labels)
	assert.Len(t, got.Labels, 2, "goog-* and goog_* labels must be filtered out")
	assert.Equal(t, "platform", *got.Labels["team"].Literal)
	assert.Equal(t, "prod", *got.Labels["env"].Literal)
	_, hasGoogManaged := got.Labels["goog-managed"]
	assert.False(t, hasGoogManaged, "goog- prefix must be stripped")
	_, hasGoogInternal := got.Labels["goog_internal"]
	assert.False(t, hasGoogInternal, "goog_ prefix must be stripped")
}

// TestMapComputeAddress_IPv6EndpointType covers the IPv6-only path
// since the FullyPopulated case leaves Ipv6EndpointType blank.
func TestMapComputeAddress_IPv6EndpointType(t *testing.T) {
	t.Parallel()
	got := mapComputeAddress(&computev1.Address{
		Name:             "v6-vm",
		Ipv6EndpointType: "VM",
	}, "p", "us-central1")
	require.NotNil(t, got.IPV6EndpointType)
	assert.Equal(t, "VM", *got.IPV6EndpointType.Literal)
}

// TestMapComputeAddress_LabelsAllGoogStripped covers the labels-map-
// empty-after-filter branch. An address whose labels map contains
// ONLY goog-managed entries must leave got.Labels nil (not an empty
// map) so the emit layer can omit the attribute entirely.
func TestMapComputeAddress_LabelsAllGoogStripped(t *testing.T) {
	t.Parallel()
	got := mapComputeAddress(&computev1.Address{
		Name:   "n",
		Labels: map[string]string{"goog-managed": "true"},
	}, "p", "r")
	assert.Nil(t, got.Labels, "all-goog map must collapse to nil so the emit layer omits the attribute")
}

// TestComputeAddressRegionAndNameForEnrich_Precedence pins the
// precedence chain in derivation. NameHint+Location win over
// NativeIDs win over ImportID parsing.
func TestComputeAddressRegionAndNameForEnrich_Precedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc       string
		id         imported.ResourceIdentity
		wantRegion string
		wantName   string
	}{
		{
			desc: "NameHint+Location_preferred",
			id: imported.ResourceIdentity{
				NameHint:  "lb-ip",
				Location:  "us-central1",
				NativeIDs: map[string]string{"name": "stale", "region": "us-east1"},
				ImportID:  "projects/p/regions/europe-west1/addresses/older",
			},
			wantRegion: "us-central1",
			wantName:   "lb-ip",
		},
		{
			desc: "NativeIDs_fallback_when_canonical_empty",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{"name": "lb-ip", "region": "us-central1"},
			},
			wantRegion: "us-central1",
			wantName:   "lb-ip",
		},
		{
			desc: "ImportID_last_resort",
			id: imported.ResourceIdentity{
				ImportID: "projects/p/regions/us-central1/addresses/lb-ip",
			},
			wantRegion: "us-central1",
			wantName:   "lb-ip",
		},
		{
			desc: "ImportID_fills_only_missing_slot",
			id: imported.ResourceIdentity{
				NameHint: "lb-ip",
				ImportID: "projects/p/regions/us-central1/addresses/other-name",
			},
			wantRegion: "us-central1",
			wantName:   "lb-ip",
		},
		{
			desc:       "all_empty_yields_empty",
			id:         imported.ResourceIdentity{},
			wantRegion: "",
			wantName:   "",
		},
		{
			desc: "global_importid_rejected_by_parts_parser",
			id: imported.ResourceIdentity{
				ImportID: "projects/p/global/addresses/global-ip",
			},
			wantRegion: "",
			wantName:   "",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			gotRegion, gotName := computeAddressRegionAndNameForEnrich(&tc.id)
			assert.Equal(t, tc.wantRegion, gotRegion)
			assert.Equal(t, tc.wantName, gotName)
		})
	}
}

// TestComputeAddressEnrich_ClientUnavailable: nil Compute client must
// return ErrEnrichClientUnavailable; ir.Attrs must remain empty so
// the orchestrator can downgrade to a per-resource warn.
func TestComputeAddressEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newComputeAddressEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_compute_address",
			ImportID: "projects/p/regions/us-central1/addresses/lb-ip",
			Address:  "google_compute_address.lb_ip",
			Location: "us-central1",
			NameHint: "lb-ip",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

// TestComputeAddressEnrich_ProjectIDRequired pins the compute-API
// quirk: Addresses.Get is positional (project, region, name), not a
// single fully-qualified name, so ProjectID on EnrichClients is
// structurally required even when Identity.ImportID encodes the
// project.
func TestComputeAddressEnrich_ProjectIDRequired(t *testing.T) {
	t.Parallel()
	e := computeAddressEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Address, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_compute_address",
			ImportID: "projects/p/regions/us-central1/addresses/lb-ip",
			Location: "us-central1",
			NameHint: "lb-ip",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EnrichClients.ProjectID required")
}

// TestComputeAddressEnrich_RegionOrNameMissing covers the
// undeducible-Identity branch. The error must mention every input
// slot it consulted so a UI consumer can patch the right field.
func TestComputeAddressEnrich_RegionOrNameMissing(t *testing.T) {
	t.Parallel()
	e := computeAddressEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Address, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_compute_address"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive region/name")
}

// TestComputeAddressEnrich_FetchError pins the wrap shape on a real
// API error (something other than 404). The wrapped error must carry
// the (project/region/name) triple for triage and wrap the underlying
// error so callers can errors.Is into the SDK error type.
func TestComputeAddressEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 forbidden")
	var gotProject, gotRegion, gotName string
	e := computeAddressEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, project, region, name string) (*computev1.Address, error) {
			gotProject, gotRegion, gotName = project, region, name
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_compute_address",
			ImportID: "projects/p/regions/us-central1/addresses/lb-ip",
			Address:  "google_compute_address.lb_ip",
			Location: "us-central1",
			NameHint: "lb-ip",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "my-project/us-central1/lb-ip")
	assert.Equal(t, "my-project", gotProject, "fetch must receive ProjectID from EnrichClients, not from Identity")
	assert.Equal(t, "us-central1", gotRegion)
	assert.Equal(t, "lb-ip", gotName)
}

// TestComputeAddressEnrich_PopulatesAttrs is the happy-path end-to-
// end check: fake fetch returns a fully-populated Address, the
// enricher writes typed JSON into ir.Attrs, and a UnmarshalAttrs
// round-trip reproduces the field values.
func TestComputeAddressEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	addr := &computev1.Address{
		Address:      "10.0.0.5",
		AddressType:  "INTERNAL",
		Description:  "Internal LB",
		Name:         "lb-ip",
		NetworkTier:  "PREMIUM",
		PrefixLength: 29,
		Purpose:      "GCE_ENDPOINT",
		Subnetwork:   "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/private",
	}
	e := computeAddressEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, project, region, name string) (*computev1.Address, error) {
			assert.Equal(t, "my-project", project)
			assert.Equal(t, "us-central1", region)
			assert.Equal(t, "lb-ip", name)
			return addr, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_compute_address",
			ImportID: "projects/my-project/regions/us-central1/addresses/lb-ip",
			Address:  "google_compute_address.lb_ip",
			Location: "us-central1",
			NameHint: "lb-ip",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"}))

	decoded, err := generated.UnmarshalAttrs("google_compute_address", ir.Attrs)
	require.NoError(t, err)
	ca, ok := decoded.(*generated.GoogleComputeAddress)
	require.True(t, ok)
	require.NotNil(t, ca.Name)
	assert.Equal(t, "lb-ip", *ca.Name.Literal)
	require.NotNil(t, ca.AddressType)
	assert.Equal(t, "INTERNAL", *ca.AddressType.Literal)
	require.NotNil(t, ca.PrefixLength)
	assert.Equal(t, float64(29), *ca.PrefixLength.Literal)
	require.NotNil(t, ca.Region)
	assert.Equal(t, "us-central1", *ca.Region.Literal)
}

// TestComputeAddressEnrich_RoundTripThroughEmitImportedTF — decision-
// #34 contract. Critical assertions: every populated field appears at
// the top level (no nested-block emission for compute_address), and
// the computed-only fields stay absent from the emitted HCL.
func TestComputeAddressEnrich_RoundTripThroughEmitImportedTF(t *testing.T) {
	t.Parallel()
	addr := &computev1.Address{
		Address:      "10.0.0.5",
		AddressType:  "INTERNAL",
		Description:  "Internal LB",
		Name:         "lb-ip",
		NetworkTier:  "PREMIUM",
		PrefixLength: 29,
		Purpose:      "GCE_ENDPOINT",
		Subnetwork:   "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/private",
	}
	typed := mapComputeAddress(addr, "my-project", "us-central1")
	raw, err := json.Marshal(typed)
	require.NoError(t, err)

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "gcp", Type: "google_compute_address",
			Address:  "google_compute_address.lb_ip",
			ImportID: "projects/my-project/regions/us-central1/addresses/lb-ip",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: raw,
	}
	out, used := composer.EmitImportedTF("gcp", []imported.ImportedResource{ir}, composer.EmitImportedOpts{})
	require.NotNil(t, out)
	require.True(t, used["gcp"])
	s := string(out)

	assert.Contains(t, s, `resource "google_compute_address" "lb_ip"`)
	assert.Regexp(t, `(?m)^\s*name\s+=\s+"lb-ip"`, s)
	assert.Regexp(t, `(?m)^\s*project\s+=\s+"my-project"`, s)
	assert.Regexp(t, `(?m)^\s*region\s+=\s+"us-central1"`, s)
	assert.Regexp(t, `(?m)^\s*address\s+=\s+"10\.0\.0\.5"`, s)
	assert.Regexp(t, `(?m)^\s*address_type\s+=\s+"INTERNAL"`, s)
	assert.Regexp(t, `(?m)^\s*network_tier\s+=\s+"PREMIUM"`, s)
	assert.Regexp(t, `(?m)^\s*purpose\s+=\s+"GCE_ENDPOINT"`, s)
	assert.Regexp(t, `(?m)^\s*prefix_length\s+=\s+29`, s)

	// Computed-only fields must NOT appear in emitted HCL.
	for _, computed := range []string{"creation_timestamp", "label_fingerprint", "self_link", "effective_labels", "terraform_labels"} {
		assert.NotContains(t, s, computed, "computed-only field %q must not appear", computed)
	}
}

// ---------------------------------------------------------------
// ByIDEnricher tests.
// ---------------------------------------------------------------

// TestComputeAddressEnrichByID_NilIdentity confirms the EnrichByID
// surface gives a clean error on a nil identity (the dispatcher in
// pkg/imported should never call with nil, but a defensive check
// keeps the failure mode obvious if it does).
func TestComputeAddressEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newComputeAddressEnricher().(*computeAddressEnricher)
	raw, err := e.EnrichByID(context.Background(), nil, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil identity")
}

// TestComputeAddressEnrichByID_ClientUnavailable mirrors the Enrich
// equivalent: the by-ID surface must report the same sentinel so the
// per-IR refresh dispatcher can distinguish "not configured" from a
// real API error.
func TestComputeAddressEnrichByID_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newComputeAddressEnricher().(*computeAddressEnricher)
	id := &imported.ResourceIdentity{
		Type: "google_compute_address", NameHint: "lb-ip", Location: "us-central1",
		ImportID: "projects/p/regions/us-central1/addresses/lb-ip",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Compute: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Nil(t, raw)
}

// TestComputeAddressEnrichByID_NotFound pins the 404-to-ErrNotFound
// translation. The compute API surfaces a *googleapi.Error with
// Code = 404 on a deleted-since-discover row; per the ByIDEnricher
// contract that must become ErrNotFound so the caller can prune the
// row from its UI without surfacing an alarming "API error".
func TestComputeAddressEnrichByID_NotFound(t *testing.T) {
	t.Parallel()
	e := computeAddressEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Address, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{
		Type: "google_compute_address", NameHint: "lb-ip", Location: "us-central1",
		ImportID: "projects/p/regions/us-central1/addresses/lb-ip",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

// TestComputeAddressEnrichByID_NonNotFoundErrorPassesThrough — a
// non-404 googleapi error (e.g. 500 or 403) MUST NOT be confused
// with ErrNotFound. The wrapper passes the original error through so
// the caller can errors.As back to *googleapi.Error if it cares.
func TestComputeAddressEnrichByID_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden, Message: "denied"}
	e := computeAddressEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Address, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{
		Type: "google_compute_address", NameHint: "lb-ip", Location: "us-central1",
		ImportID: "projects/p/regions/us-central1/addresses/lb-ip",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound, "403 must NOT be classified as ErrNotFound")
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr), "underlying googleapi.Error must be preserved for caller inspection")
	assert.Equal(t, http.StatusForbidden, gerr.Code)
	assert.Nil(t, raw)
}

// TestComputeAddressEnrichByID_HappyPath confirms EnrichByID returns
// the exact JSON shape Enrich would have written into ir.Attrs.
// Pin via byte-equality so a future divergence between the two
// entry-points is caught loud.
func TestComputeAddressEnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	addr := &computev1.Address{
		Address:     "10.0.0.5",
		AddressType: "INTERNAL",
		Name:        "lb-ip",
		Purpose:     "GCE_ENDPOINT",
	}
	mkFetch := func() func(context.Context, *computev1.Service, string, string, string) (*computev1.Address, error) {
		return func(_ context.Context, _ *computev1.Service, project, region, name string) (*computev1.Address, error) {
			assert.Equal(t, "my-project", project)
			assert.Equal(t, "us-central1", region)
			assert.Equal(t, "lb-ip", name)
			return addr, nil
		}
	}
	enrichEnr := computeAddressEnricher{fetch: mkFetch()}
	byIDEnr := computeAddressEnricher{fetch: mkFetch()}

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_compute_address",
			ImportID: "projects/my-project/regions/us-central1/addresses/lb-ip",
			Address:  "google_compute_address.lb_ip",
			Location: "us-central1",
			NameHint: "lb-ip",
		},
	}
	require.NoError(t, enrichEnr.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"}))

	id := &imported.ResourceIdentity{
		Type:     "google_compute_address",
		ImportID: "projects/my-project/regions/us-central1/addresses/lb-ip",
		Address:  "google_compute_address.lb_ip",
		Location: "us-central1",
		NameHint: "lb-ip",
	}
	raw, err := byIDEnr.EnrichByID(context.Background(), id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw),
		"EnrichByID must return the same JSON shape Enrich writes into ir.Attrs")
}

// TestComputeAddressEnrichByID_DerivesFromImportIDOnly confirms the
// caller can pass an Identity that only carries ImportID (the
// pkg/imported.Provider refresh path can construct one from a TF
// address alone) and the enricher still derives (region, name).
func TestComputeAddressEnrichByID_DerivesFromImportIDOnly(t *testing.T) {
	t.Parallel()
	var gotRegion, gotName string
	e := computeAddressEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, region, name string) (*computev1.Address, error) {
			gotRegion, gotName = region, name
			return &computev1.Address{Name: name}, nil
		},
	}
	id := &imported.ResourceIdentity{
		Type:     "google_compute_address",
		ImportID: "projects/my-project/regions/europe-west4/addresses/eu-lb-ip",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	assert.NotNil(t, raw)
	assert.Equal(t, "europe-west4", gotRegion)
	assert.Equal(t, "eu-lb-ip", gotName)
}

// TestIsComputeNotFound_Predicate is a direct unit on the helper so a
// future change to its error-classification logic (e.g. accepting
// gRPC NotFound too) doesn't have to be inferred from a fetch-level
// integration test.
func TestIsComputeNotFound_Predicate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain_error", errors.New("boom"), false},
		{"googleapi_404", &googleapi.Error{Code: http.StatusNotFound}, true},
		{"googleapi_403", &googleapi.Error{Code: http.StatusForbidden}, false},
		{"googleapi_500", &googleapi.Error{Code: http.StatusInternalServerError}, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			assert.Equal(t, tc.want, isComputeNotFound(tc.err))
		})
	}
}
