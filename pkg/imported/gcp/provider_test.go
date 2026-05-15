package gcp_test

import (
	"context"
	"errors"
	"strings"
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
	// google_project_iam_member is in the registry but no enricher.
	if got := p.Capabilities("google_project_iam_member"); got.Enrichable {
		t.Errorf("google_project_iam_member: Enrichable should be false, got %+v", got)
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
	imp.ResetAgentContextCacheForTest()

	if got := p.AgentContext(nil); got != nil {
		t.Errorf("AgentContext(nil) = %v, want nil", got)
	}

	// Two registered GCP types, given in reverse alphabetical type
	// order. The shared renderer (see pkg/imported/agentcontext.go)
	// emits per-type blocks in stable Terraform-type-name order, so
	// the pubsub block must come before the storage_bucket block in
	// the output.
	irs := []composerimported.ImportedResource{
		{Identity: composerimported.ResourceIdentity{Type: "google_storage_bucket", Address: "google_storage_bucket.z"}},
		{Identity: composerimported.ResourceIdentity{Type: "google_pubsub_topic", Address: "google_pubsub_topic.a"}},
	}
	got := p.AgentContext(irs)
	if len(got) == 0 {
		t.Fatal("AgentContext returned no lines for registered types")
	}
	joined := strings.Join(got, "\n")

	pubIdx := strings.Index(joined, "== Imported.google_pubsub_topic ==")
	bucketIdx := strings.Index(joined, "== Imported.google_storage_bucket ==")
	if pubIdx < 0 || bucketIdx < 0 {
		t.Fatalf("AgentContext missing expected type headers:\n%s", joined)
	}
	if pubIdx >= bucketIdx {
		t.Errorf("type blocks must render in alphabetical order; pubIdx=%d bucketIdx=%d", pubIdx, bucketIdx)
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

	id := &composerimported.ResourceIdentity{Type: "google_project_iam_member", ImportID: "p roles/x"}
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

// TestProvider_CapabilitiesParity — see aws/provider_test.go for the
// full doc; the GCP enrichableNoByID exemption mirrors the 5 pre-Phase-2
// GCP enrichers that satisfy AttributeEnricher but not ByIDEnricher
// yet. Bundle 1's compute_address / compute_firewall implement both, so
// they're NOT in the exemption list — the strong parity direction
// guards them.
func TestProvider_CapabilitiesParity(t *testing.T) {
	t.Parallel()
	d := gcpdiscover.NewGCPDiscoverer(nil, "test-project", gcpdiscover.GCPDiscovererOpts{})
	p := gcpprov.NewProvider(d, nil)

	// Types whose AttributeEnricher exists but doesn't satisfy
	// ByIDEnricher yet. Mirrors notImplemented in
	// cmd/insideout-import/gcpdiscover/byid_enricher_test.go.
	enrichableNoByID := map[string]bool{
		"google_storage_bucket":        true,
		"google_pubsub_topic":          true,
		"google_pubsub_subscription":   true,
		"google_secret_manager_secret": true,
		"google_compute_network":       true,
	}

	for _, tfType := range p.SupportedTypes() {
		caps := p.Capabilities(tfType)
		id := &composerimported.ResourceIdentity{
			Type:     tfType,
			ImportID: "test-id",
		}
		_, err := p.EnrichByID(context.Background(), id, imp.Clients{GCP: gcpprov.Clients{}})

		notImpl := errors.Is(err, imp.ErrEnrichByIDNotImplemented)

		switch {
		case !caps.Enrichable && !notImpl:
			t.Errorf("%s: Enrichable=false but EnrichByID returned %v; want ErrEnrichByIDNotImplemented", tfType, err)
		case caps.Enrichable && notImpl && !enrichableNoByID[tfType]:
			t.Errorf("%s: Enrichable=true but EnrichByID returned ErrEnrichByIDNotImplemented (add to enrichableNoByID exemption if intentional, else wire ByIDEnricher impl)", tfType)
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
