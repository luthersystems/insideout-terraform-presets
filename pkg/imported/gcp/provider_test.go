package gcp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
	gcpprov "github.com/luthersystems/insideout-terraform-presets/pkg/imported/gcp"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

func TestNewProvider_NilDiscoverer_StaticIntrospection(t *testing.T) {
	t.Parallel()
	p := gcpprov.NewProvider(nil, nil)

	types := p.SupportedTypes()
	want := registry.SupportedDiscoverTypes("gcp")
	if len(types) != len(want) {
		t.Errorf("SupportedTypes len = %d, want %d", len(types), len(want))
	}

	caps := p.Capabilities("google_storage_bucket")
	if !caps.Discoverable {
		t.Error("google_storage_bucket should be Discoverable from the registry")
	}
	if caps.Enrichable {
		t.Error("Enrichable should be false without a discoverer")
	}

	lbl, icon := p.LabelFor("google_storage_bucket")
	if lbl == "" || icon == "" {
		t.Errorf("LabelFor(google_storage_bucket) = (%q, %q); both should be non-empty", lbl, icon)
	}

	if _, err := p.Discover(context.Background(), nil, imp.Clients{}, imp.DiscoverOpts{}); !errors.Is(err, imp.ErrEnrichClientUnavailable) {
		t.Errorf("Discover with nil discoverer: err = %v, want ErrEnrichClientUnavailable", err)
	}
	if err := p.EnrichAttributes(context.Background(), nil, imp.Clients{}); !errors.Is(err, imp.ErrEnrichClientUnavailable) {
		t.Errorf("EnrichAttributes with nil discoverer: err = %v, want ErrEnrichClientUnavailable", err)
	}
	if _, err := p.EnrichByID(context.Background(), nil, imp.Clients{}); !errors.Is(err, imp.ErrEnrichClientUnavailable) {
		t.Errorf("EnrichByID with nil discoverer: err = %v, want ErrEnrichClientUnavailable", err)
	}
}

func TestProvider_Capabilities_Enrichable(t *testing.T) {
	t.Parallel()
	d := gcpdiscover.NewGCPDiscoverer(nil, "test-project", gcpdiscover.GCPDiscovererOpts{})
	p := gcpprov.NewProvider(d, nil)

	// google_storage_bucket has a registered enricher.
	if got := p.Capabilities("google_storage_bucket"); !got.Enrichable {
		t.Errorf("google_storage_bucket: Enrichable should be true, got %+v", got)
	}
	// google_sql_user is in the registry but no enricher.
	if got := p.Capabilities("google_sql_user"); got.Enrichable {
		t.Errorf("google_sql_user: Enrichable should be false, got %+v", got)
	}
}

func TestProvider_Capabilities_DriftDetectable(t *testing.T) {
	t.Parallel()
	d := gcpdiscover.NewGCPDiscoverer(nil, "test-project", gcpdiscover.GCPDiscovererOpts{})

	p := gcpprov.NewProvider(d, nil)
	if got := p.Capabilities("google_storage_bucket"); got.DriftDetectable {
		t.Errorf("Without comparator, DriftDetectable should be false; got %+v", got)
	}

	noopComparer := func(string, imp.Attrs, imp.Attrs) []imp.FieldMismatch { return nil }
	pWithCmp := gcpprov.NewProvider(d, noopComparer)
	// google_storage_bucket has a registered policy.
	if got := pWithCmp.Capabilities("google_storage_bucket"); !got.DriftDetectable {
		t.Errorf("With comparator + policy, DriftDetectable should be true; got %+v", got)
	}
}

func TestProvider_StableID(t *testing.T) {
	t.Parallel()
	p := gcpprov.NewProvider(nil, nil)

	if got := p.StableID(nil); got != "" {
		t.Errorf("StableID(nil) = %q, want \"\"", got)
	}

	// self_link-bearing identity.
	id := &composerimported.ResourceIdentity{
		Type:      "google_storage_bucket",
		Address:   "google_storage_bucket.b",
		ImportID:  "my-bucket",
		NativeIDs: map[string]string{"self_link": "https://www.googleapis.com/storage/v1/b/my-bucket"},
	}
	if got := p.StableID(id); got != "https://www.googleapis.com/storage/v1/b/my-bucket" {
		t.Errorf("StableID self_link-bearing = %q", got)
	}

	id2 := &composerimported.ResourceIdentity{
		Type:     "google_pubsub_topic",
		Address:  "google_pubsub_topic.t",
		ImportID: "projects/p/topics/t",
	}
	if got := p.StableID(id2); got != "projects/p/topics/t" {
		t.Errorf("StableID ImportID fallback = %q", got)
	}

	id3 := &composerimported.ResourceIdentity{
		Type:    "google_pubsub_topic",
		Address: "google_pubsub_topic.t",
	}
	if got := p.StableID(id3); got != "google_pubsub_topic.t" {
		t.Errorf("StableID address fallback = %q", got)
	}
}

func TestProvider_CanonicalAddress(t *testing.T) {
	t.Parallel()
	p := gcpprov.NewProvider(nil, nil)

	if got := p.CanonicalAddress(nil); got != "" {
		t.Errorf("CanonicalAddress(nil) = %q, want \"\"", got)
	}

	id := &composerimported.ResourceIdentity{
		Type:    "google_storage_bucket",
		Address: "google_storage_bucket.mybucket",
	}
	if got := p.CanonicalAddress(id); got != "google_storage_bucket.mybucket" {
		t.Errorf("CanonicalAddress unchanged = %q", got)
	}

	id2 := &composerimported.ResourceIdentity{
		Type:     "google_storage_bucket",
		NameHint: "MyBucket",
	}
	if got := p.CanonicalAddress(id2); got == "" || got == "google_storage_bucket." {
		t.Errorf("CanonicalAddress regenerated empty: %q", got)
	}
}

func TestProvider_AgentContext(t *testing.T) {
	t.Parallel()
	p := gcpprov.NewProvider(nil, nil)

	if got := p.AgentContext(nil); got != nil {
		t.Errorf("AgentContext(nil) = %v, want nil", got)
	}

	irs := []composerimported.ImportedResource{
		{Identity: composerimported.ResourceIdentity{Type: "google_storage_bucket", Address: "google_storage_bucket.z"}},
		{Identity: composerimported.ResourceIdentity{Type: "google_pubsub_topic", Address: "google_pubsub_topic.a"}},
	}
	got := p.AgentContext(irs)
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	if got[0] >= got[1] {
		t.Errorf("AgentContext not sorted: %v", got)
	}
}

func TestProvider_CompareDrift(t *testing.T) {
	t.Parallel()

	p := gcpprov.NewProvider(nil, nil)
	if got := p.CompareDrift("google_storage_bucket", nil, nil); got != nil {
		t.Errorf("CompareDrift with nil comparer = %v, want nil", got)
	}

	want := []imp.FieldMismatch{{Field: "x", Snapshot: 1, Cloud: 2}}
	cmp := func(tfType string, snap, live imp.Attrs) []imp.FieldMismatch {
		return want
	}
	p2 := gcpprov.NewProvider(nil, cmp)
	got := p2.CompareDrift("google_storage_bucket", nil, nil)
	if len(got) != 1 || got[0].Field != "x" {
		t.Errorf("CompareDrift delegate = %v, want %v", got, want)
	}
}

func TestProvider_PolicyAndMetrics(t *testing.T) {
	t.Parallel()
	p := gcpprov.NewProvider(nil, nil)

	if _, ok := p.PolicyFor("google_storage_bucket"); !ok {
		t.Error("google_storage_bucket should have a registered policy")
	}
	if _, ok := p.PolicyFor("google_bogus_unknown"); ok {
		t.Error("unknown type should not have a policy")
	}
	_, _ = p.MetricsBinding("google_storage_bucket")
}

func TestProvider_EnrichByID_NoEnricher(t *testing.T) {
	t.Parallel()
	d := gcpdiscover.NewGCPDiscoverer(nil, "test-project", gcpdiscover.GCPDiscovererOpts{})
	p := gcpprov.NewProvider(d, nil)

	id := &composerimported.ResourceIdentity{Type: "google_sql_user", ImportID: "u/v"}
	_, err := p.EnrichByID(context.Background(), id, imp.Clients{GCP: gcpprov.Clients{}})
	if !errors.Is(err, imp.ErrEnrichByIDNotImplemented) {
		t.Errorf("EnrichByID for non-enriched type: err = %v, want ErrEnrichByIDNotImplemented", err)
	}
}

func TestProvider_EnrichAttributes_WrongCloud(t *testing.T) {
	t.Parallel()
	d := gcpdiscover.NewGCPDiscoverer(nil, "test-project", gcpdiscover.GCPDiscovererOpts{})
	p := gcpprov.NewProvider(d, nil)

	err := p.EnrichAttributes(context.Background(), nil, imp.Clients{AWS: struct{}{}})
	if !errors.Is(err, imp.ErrClientsWrongCloud) {
		t.Errorf("GCP provider with AWS clients: err = %v, want ErrClientsWrongCloud", err)
	}
}

func TestProvider_CapabilitiesParity(t *testing.T) {
	t.Parallel()
	d := gcpdiscover.NewGCPDiscoverer(nil, "test-project", gcpdiscover.GCPDiscovererOpts{})
	p := gcpprov.NewProvider(d, nil)

	for _, tfType := range p.SupportedTypes() {
		caps := p.Capabilities(tfType)
		id := &composerimported.ResourceIdentity{
			Type:     tfType,
			ImportID: "test-id",
		}
		_, err := p.EnrichByID(context.Background(), id, imp.Clients{GCP: gcpprov.Clients{}})

		notImpl := errors.Is(err, imp.ErrEnrichByIDNotImplemented)
		if caps.Enrichable && notImpl {
			// Same weaker parity as AWS — Enrichable=true with no
			// ByIDEnricher is an allowed transitional state for
			// types whose enricher hasn't yet been extended.
			continue
		}
		if !caps.Enrichable && !notImpl {
			t.Errorf("%s: Enrichable=false but EnrichByID returned %v; want ErrEnrichByIDNotImplemented", tfType, err)
		}
	}
}

func TestProvider_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	d := gcpdiscover.NewGCPDiscoverer(nil, "test-project", gcpdiscover.GCPDiscovererOpts{})
	p := gcpprov.NewProvider(d, nil)
	_, err := p.EnrichByID(context.Background(), nil, imp.Clients{GCP: gcpprov.Clients{}})
	if err == nil {
		t.Error("expected error for nil identity")
	}
}
