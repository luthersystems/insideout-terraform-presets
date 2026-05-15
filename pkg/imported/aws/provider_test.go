package aws_test

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
	awsprov "github.com/luthersystems/insideout-terraform-presets/pkg/imported/aws"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

func TestNewProvider_NilDiscoverer_StaticIntrospection(t *testing.T) {
	t.Parallel()
	p := awsprov.NewProvider(nil, nil)

	// Static introspection works without a discoverer.
	types := p.SupportedTypes()
	want := registry.SupportedDiscoverTypes("aws")
	if len(types) != len(want) {
		t.Errorf("SupportedTypes len = %d, want %d", len(types), len(want))
	}

	// Capabilities: a known AWS type should report Discoverable=true even
	// without a discoverer (the discoverable flag comes from the registry).
	caps := p.Capabilities("aws_s3_bucket")
	if !caps.Discoverable {
		t.Error("aws_s3_bucket should be Discoverable from the registry")
	}
	// Without a discoverer, Enrichable is false.
	if caps.Enrichable {
		t.Error("Enrichable should be false without a discoverer")
	}

	// LabelFor falls back to the default rule.
	lbl, icon := p.LabelFor("aws_s3_bucket")
	if lbl == "" || icon == "" {
		t.Errorf("LabelFor(aws_s3_bucket) = (%q, %q); both should be non-empty", lbl, icon)
	}

	// Live calls return ErrEnrichClientUnavailable without a discoverer.
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
	d := awsdiscover.NewAWSDiscoverer(awssdk.Config{})
	p := awsprov.NewProvider(d, nil)

	// aws_dynamodb_table has a registered enricher (Bundle 1).
	if got := p.Capabilities("aws_dynamodb_table"); !got.Enrichable {
		t.Errorf("aws_dynamodb_table: Enrichable should be true, got %+v", got)
	}
	// After the final-2 push (#482) every registered AWS type has an
	// enricher (AWS 100% Enrichable), so the only remaining sample for
	// the "Enrichable false" branch is an unregistered type. Mirrors
	// GCP's provider_test post-100% push, which pivots on
	// google_bogus_unknown for the same reason.
	if got := p.Capabilities("aws_bogus_unknown"); got.Enrichable {
		t.Errorf("aws_bogus_unknown: Enrichable should be false, got %+v", got)
	}
	// Unknown type: all flags false (same sample as above, but pinned
	// against the Discoverable axis for completeness).
	if got := p.Capabilities("aws_bogus_unknown"); got.Discoverable || got.Enrichable {
		t.Errorf("unknown type: should be all false, got %+v", got)
	}
}

func TestProvider_Capabilities_DriftDetectable(t *testing.T) {
	t.Parallel()
	d := awsdiscover.NewAWSDiscoverer(awssdk.Config{})

	// Without a comparator, DriftDetectable is always false.
	p := awsprov.NewProvider(d, nil)
	if got := p.Capabilities("aws_dynamodb_table"); got.DriftDetectable {
		t.Errorf("Without comparator, DriftDetectable should be false; got %+v", got)
	}

	// With a comparator, DriftDetectable depends on policy registration.
	noopComparer := func(string, imp.Attrs, imp.Attrs) []imp.FieldMismatch { return nil }
	pWithCmp := awsprov.NewProvider(d, noopComparer)
	// aws_dynamodb_table has a registered policy.
	if got := pWithCmp.Capabilities("aws_dynamodb_table"); !got.DriftDetectable {
		t.Errorf("With comparator + policy, DriftDetectable should be true; got %+v", got)
	}
	// aws_acm_certificate has no policy registered today (used as a
	// stable "uncovered" negative example — previously aws_vpc, which
	// gained a curated policy in the #482 AWS drift bundle).
	if got := pWithCmp.Capabilities("aws_acm_certificate"); got.DriftDetectable {
		t.Errorf("aws_acm_certificate has no policy; DriftDetectable should be false; got %+v", got)
	}
}

func TestProvider_StableID(t *testing.T) {
	t.Parallel()
	p := awsprov.NewProvider(nil, nil)

	if got := p.StableID(nil); got != "" {
		t.Errorf("StableID(nil) = %q, want \"\"", got)
	}

	// ARN-bearing identity.
	id := &composerimported.ResourceIdentity{
		Type:      "aws_s3_bucket",
		Address:   "aws_s3_bucket.b",
		ImportID:  "my-bucket",
		NativeIDs: map[string]string{"arn": "arn:aws:s3:::my-bucket"},
	}
	if got := p.StableID(id); got != "arn:aws:s3:::my-bucket" {
		t.Errorf("StableID arn-bearing = %q", got)
	}

	// ImportID fallback when no ARN.
	id2 := &composerimported.ResourceIdentity{
		Type:     "aws_iam_role",
		Address:  "aws_iam_role.r",
		ImportID: "my-role",
	}
	if got := p.StableID(id2); got != "my-role" {
		t.Errorf("StableID ImportID fallback = %q", got)
	}

	// Address fallback when no ARN or ImportID.
	id3 := &composerimported.ResourceIdentity{
		Type:    "aws_iam_role",
		Address: "aws_iam_role.r",
	}
	if got := p.StableID(id3); got != "aws_iam_role.r" {
		t.Errorf("StableID address fallback = %q", got)
	}
}

func TestProvider_CanonicalAddress(t *testing.T) {
	t.Parallel()
	p := awsprov.NewProvider(nil, nil)

	if got := p.CanonicalAddress(nil); got != "" {
		t.Errorf("CanonicalAddress(nil) = %q, want \"\"", got)
	}

	// Address set → returned unchanged.
	id := &composerimported.ResourceIdentity{
		Type:    "aws_s3_bucket",
		Address: "aws_s3_bucket.mybucket",
	}
	if got := p.CanonicalAddress(id); got != "aws_s3_bucket.mybucket" {
		t.Errorf("CanonicalAddress with Address = %q, want unchanged", got)
	}

	// Address empty → regenerated from NameHint via imported.GenerateAddress.
	id2 := &composerimported.ResourceIdentity{
		Type:     "aws_s3_bucket",
		NameHint: "MyBucket",
	}
	got := p.CanonicalAddress(id2)
	if got == "" || got == "aws_s3_bucket." {
		t.Errorf("CanonicalAddress with empty Address returned %q", got)
	}
}

func TestProvider_AgentContext(t *testing.T) {
	t.Parallel()
	p := awsprov.NewProvider(nil, nil)

	if got := p.AgentContext(nil); got != nil {
		t.Errorf("AgentContext(nil) = %v, want nil", got)
	}

	irs := []composerimported.ImportedResource{
		{Identity: composerimported.ResourceIdentity{Type: "aws_s3_bucket", Address: "aws_s3_bucket.z"}},
		{Identity: composerimported.ResourceIdentity{Type: "aws_iam_role", Address: "aws_iam_role.a"}},
	}
	got := p.AgentContext(irs)
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	// Sorted by line — aws_iam_role.a (...) sorts before aws_s3_bucket.z (...)
	if got[0] >= got[1] {
		t.Errorf("AgentContext not sorted: %v", got)
	}
}

func TestProvider_CompareDrift(t *testing.T) {
	t.Parallel()

	// No comparator wired → nil.
	p := awsprov.NewProvider(nil, nil)
	if got := p.CompareDrift("aws_s3_bucket", nil, nil); got != nil {
		t.Errorf("CompareDrift with nil comparer = %v, want nil", got)
	}

	// With a comparator → delegated.
	want := []imp.FieldMismatch{{Field: "x", Snapshot: 1, Cloud: 2}}
	cmp := func(tfType string, snap, live imp.Attrs) []imp.FieldMismatch {
		return want
	}
	p2 := awsprov.NewProvider(nil, cmp)
	got := p2.CompareDrift("aws_s3_bucket", nil, nil)
	if len(got) != 1 || got[0].Field != "x" {
		t.Errorf("CompareDrift delegate = %v, want %v", got, want)
	}
}

func TestProvider_PolicyAndMetrics(t *testing.T) {
	t.Parallel()
	p := awsprov.NewProvider(nil, nil)

	// PolicyFor: aws_dynamodb_table has a curated policy.
	if _, ok := p.PolicyFor("aws_dynamodb_table"); !ok {
		t.Error("aws_dynamodb_table should have a registered policy")
	}
	// Unknown type: no policy.
	if _, ok := p.PolicyFor("aws_bogus_unknown"); ok {
		t.Error("unknown type should not have a policy")
	}

	// MetricsBinding: zero registered today, all bool-false (test pinned to
	// the registry contract, not specific data).
	_, _ = p.MetricsBinding("aws_s3_bucket")
}

func TestProvider_EnrichByID_NoEnricher(t *testing.T) {
	t.Parallel()
	d := awsdiscover.NewAWSDiscoverer(awssdk.Config{})
	p := awsprov.NewProvider(d, nil)

	// Unknown type: ErrEnrichByIDNotImplemented. After the final-2 push
	// (#482) every registered AWS type has an enricher, so the only
	// remaining sample for this branch is an unregistered type — the
	// provider's dispatch path returns the same sentinel for "type has
	// no enricher" and "type unknown to provider". Mirrors GCP's
	// provider_test post-100% push.
	id := &composerimported.ResourceIdentity{Type: "aws_bogus_unknown", ImportID: "irrelevant"}
	_, err := p.EnrichByID(context.Background(), id, imp.Clients{AWS: awsprov.Clients{}})
	if !errors.Is(err, imp.ErrEnrichByIDNotImplemented) {
		t.Errorf("EnrichByID for non-enriched type: err = %v, want ErrEnrichByIDNotImplemented", err)
	}
}

func TestProvider_EnrichByID_HasByIDEnricher(t *testing.T) {
	t.Parallel()
	d := awsdiscover.NewAWSDiscoverer(awssdk.Config{})
	p := awsprov.NewProvider(d, nil)

	// aws_cloudwatch_log_group is registered AND implements ByIDEnricher
	// per byid_enricher_test.go. With nil clients on EnrichClients it
	// should surface ErrEnrichClientUnavailable from the underlying
	// enricher — that's the expected path for a no-creds test.
	id := &composerimported.ResourceIdentity{
		Type:     "aws_cloudwatch_log_group",
		Region:   "us-east-1",
		ImportID: "/aws/lambda/test",
	}
	_, err := p.EnrichByID(context.Background(), id, imp.Clients{AWS: awsprov.Clients{}})
	// The exact error depends on the enricher implementation; we accept
	// either ErrEnrichClientUnavailable (downgraded path) or some other
	// wrapped error. The key contract is: NOT ErrEnrichByIDNotImplemented
	// (the enricher exists and implements the interface).
	if errors.Is(err, imp.ErrEnrichByIDNotImplemented) {
		t.Errorf("aws_cloudwatch_log_group should implement ByIDEnricher; got ErrEnrichByIDNotImplemented")
	}
}

func TestProvider_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	d := awsdiscover.NewAWSDiscoverer(awssdk.Config{})
	p := awsprov.NewProvider(d, nil)
	_, err := p.EnrichByID(context.Background(), nil, imp.Clients{AWS: awsprov.Clients{}})
	if err == nil {
		t.Error("expected error for nil identity")
	}
}

func TestProvider_Discover_WrongCloud(t *testing.T) {
	t.Parallel()
	d := awsdiscover.NewAWSDiscoverer(awssdk.Config{})
	p := awsprov.NewProvider(d, nil)

	// GCP clients on AWS provider → EnrichAttributes returns
	// ErrClientsWrongCloud. (Discover does not unwrap clients today —
	// the AWSDiscoverer doesn't take EnrichClients on Discover — so
	// we test the EnrichAttributes path.)
	err := p.EnrichAttributes(context.Background(), nil, imp.Clients{GCP: struct{}{}})
	if !errors.Is(err, imp.ErrClientsWrongCloud) {
		t.Errorf("AWS provider with GCP clients: err = %v, want ErrClientsWrongCloud", err)
	}
}

func TestProvider_Discover_PassesThrough(t *testing.T) {
	t.Parallel()
	d := awsdiscover.NewAWSDiscoverer(awssdk.Config{Region: "us-east-1"})
	p := awsprov.NewProvider(d, nil)

	// Empty types and no real cloud creds — the call should return
	// an error from the underlying discoverer rather than panic. We
	// can't drive the SDK without credentials, so the assertion here
	// is: no panic, error path returns something inspectable.
	_, err := p.Discover(context.Background(), []string{"aws_bogus"}, imp.Clients{AWS: awsprov.Clients{}}, imp.DiscoverOpts{Project: "test"})
	if err == nil {
		t.Error("expected error for unknown type, got nil")
	}
}

// TestProvider_CapabilitiesParity pins bidirectional agreement between
// Capabilities(t).Enrichable and the EnrichByID dispatch result:
//
//   - Enrichable=false ⇒ EnrichByID returns ErrEnrichByIDNotImplemented.
//     If a type isn't enrichable, the by-ID path must NOT silently work.
//   - Enrichable=true AND tfType not in enrichableNoByID ⇒ EnrichByID
//     must NOT return ErrEnrichByIDNotImplemented (it may return other
//     errors from nil SDK clients, but the by-ID dispatcher must
//     resolve to a real impl).
//
// The enrichableNoByID exemption list pins the known state where a
// type has an AttributeEnricher but not a ByIDEnricher today
// (aws_dynamodb_table per byid_enricher_test.go's notImplemented
// allowlist). When Phase 2 lands a real ByIDEnricher for dynamodb,
// drop the entry — the test will then guard that direction too.
func TestProvider_CapabilitiesParity(t *testing.T) {
	t.Parallel()
	d := awsdiscover.NewAWSDiscoverer(awssdk.Config{})
	p := awsprov.NewProvider(d, nil)

	// Types whose AttributeEnricher exists but doesn't satisfy
	// ByIDEnricher yet. Mirrors notImplemented in
	// cmd/insideout-import/awsdiscover/byid_enricher_test.go.
	enrichableNoByID := map[string]bool{
		"aws_dynamodb_table": true,
	}

	for _, tfType := range p.SupportedTypes() {
		caps := p.Capabilities(tfType)
		id := &composerimported.ResourceIdentity{
			Type:     tfType,
			Region:   "us-east-1",
			ImportID: "test-id",
		}
		_, err := p.EnrichByID(context.Background(), id, imp.Clients{AWS: awsprov.Clients{}})

		notImpl := errors.Is(err, imp.ErrEnrichByIDNotImplemented)

		switch {
		case !caps.Enrichable && !notImpl:
			t.Errorf("%s: Enrichable=false but EnrichByID returned %v; want ErrEnrichByIDNotImplemented", tfType, err)
		case caps.Enrichable && notImpl && !enrichableNoByID[tfType]:
			t.Errorf("%s: Enrichable=true but EnrichByID returned ErrEnrichByIDNotImplemented (add to enrichableNoByID exemption if intentional, else wire ByIDEnricher impl)", tfType)
		}
	}
}
