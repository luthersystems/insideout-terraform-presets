package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"

	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// pubsubTopicEnricher implements AttributeEnricher for
// google_pubsub_topic. Pairs with pubsubTopicDiscoverer (Identity)
// — same package convention as the per-type Discoverer files.
//
// The pure-mapping logic — converting a *pubsubv1.Topic into a
// *generated.GooglePubsubTopic — lives in pubsub_topic_enrich.gen.go,
// produced by cmd/enrichgen via compile-time reflection over the typed
// Layer 1 struct + the raw JSON API struct. To change a mapping or add
// a field, edit the override snippets in cmd/enrichgen/pubsub_topic.go
// and re-run `go generate ./cmd/insideout-import/gcpdiscover/...`.
//
// SDK source: google.golang.org/api/pubsub/v1.Topic — the raw JSON
// API client, matching what terraform-provider-google itself uses
// internally. Same rationale as storage_bucket_enrich.go: SDKs that
// strip/transform fields (cloud.google.com/go/pubsub) can't round-trip
// every TF attribute, so we use the raw v1 client.
//
// Sensitive fields: none on this resource. Decision #36 redaction is
// downstream's concern.
type pubsubTopicEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// Projects.Topics.Get call against the pubsubv1.Service in
	// EnrichClients. Tests inject a fake to avoid spinning up an
	// HTTP server for the pubsub client.
	fetch func(ctx context.Context, svc *pubsubv1.Service, fullName string) (*pubsubv1.Topic, error)
}

func newPubsubTopicEnricher() AttributeEnricher {
	return &pubsubTopicEnricher{fetch: defaultPubsubTopicFetch}
}

// Compile-time assertion that this enricher satisfies both interfaces.
// Phase 2 contract: every enricher implements ByIDEnricher in addition
// to AttributeEnricher (issue #571).
var (
	_ AttributeEnricher = (*pubsubTopicEnricher)(nil)
	_ ByIDEnricher      = (*pubsubTopicEnricher)(nil)
)

func (pubsubTopicEnricher) ResourceType() string { return pubsubTopicTFType }

// Enrich populates ir.Attrs with a typed GooglePubsubTopic payload for
// the topic identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.Pubsub is nil; any other
// error reflects a real Pub/Sub API failure.
func (e pubsubTopicEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling entry-point for the per-IR refresh path:
// it accepts a bare Identity and returns the same json.RawMessage shape
// Enrich would write into ir.Attrs. A 404 from the Pub/Sub API is
// translated to ErrNotFound so callers can distinguish "topic deleted
// since last discover" from a real API failure. See issue #571.
func (e pubsubTopicEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("pubsub_topic: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

// fetchTyped is the shared helper between Enrich and EnrichByID. It
// performs the client-availability check, derives the fully-qualified
// topic name, fires the SDK call, and marshals the typed payload.
func (e pubsubTopicEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Pubsub == nil {
		return nil, ErrEnrichClientUnavailable
	}
	full := pubsubTopicFullNameForEnrichIdentity(id, c.ProjectID)
	if full == "" {
		return nil, fmt.Errorf("pubsub_topic: cannot derive topic resource name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.NativeIDs["asset_name"])
	}
	t, err := e.fetch(ctx, c.Pubsub, full)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("pubsub_topic %q: %w", full, ErrNotFound)
		}
		return nil, fmt.Errorf("pubsub_topic: get %q: %w", full, err)
	}
	typed := mapPubsubTopic(t, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("pubsub_topic: marshal Attrs: %w", err)
	}
	return raw, nil
}

// pubsubTopicFullNameForEnrich derives the fully-qualified
// "projects/<proj>/topics/<short>" resource name that Projects.Topics.Get
// requires. The Discoverer already populates Identity.ImportID with
// that exact shape (pubsub_topic.go:39); falls back to constructing
// it from NativeIDs["asset_name"] + projectID for safety.
func pubsubTopicFullNameForEnrich(ir *imported.ImportedResource, projectID string) string {
	return pubsubTopicFullNameForEnrichIdentity(&ir.Identity, projectID)
}

// pubsubTopicFullNameForEnrichIdentity is the identity-only counterpart
// used by EnrichByID (and indirectly by pubsubTopicFullNameForEnrich).
func pubsubTopicFullNameForEnrichIdentity(id *imported.ResourceIdentity, projectID string) string {
	if id == nil {
		return ""
	}
	if id.ImportID != "" {
		return id.ImportID
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if short, err := pubsubTopicNameFromID(asset); err == nil && projectID != "" {
			return fmt.Sprintf("projects/%s/topics/%s", projectID, short)
		}
	}
	return ""
}

// defaultPubsubTopicFetch is the production fetch path: a single
// Projects.Topics.Get call. Context cancellation is honored via the
// standard tooling-API ctx wiring.
func defaultPubsubTopicFetch(ctx context.Context, svc *pubsubv1.Service, fullName string) (*pubsubv1.Topic, error) {
	return svc.Projects.Topics.Get(fullName).Context(ctx).Do()
}
