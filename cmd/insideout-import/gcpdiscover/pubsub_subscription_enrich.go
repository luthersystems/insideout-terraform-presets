package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"

	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// pubsubSubscriptionEnricher implements AttributeEnricher for
// google_pubsub_subscription. Pairs with pubsubSubscriptionDiscoverer.
//
// The pure-mapping logic lives in pubsub_subscription_enrich.gen.go,
// produced by cmd/enrichgen. To change a mapping or add a field, edit
// the override snippets in cmd/enrichgen/pubsub_subscription.go and
// re-run `go generate ./cmd/insideout-import/gcpdiscover/...`.
type pubsubSubscriptionEnricher struct {
	fetch func(ctx context.Context, svc *pubsubv1.Service, fullName string) (*pubsubv1.Subscription, error)
}

func newPubsubSubscriptionEnricher() AttributeEnricher {
	return &pubsubSubscriptionEnricher{fetch: defaultPubsubSubscriptionFetch}
}

// Compile-time assertion that this enricher satisfies both interfaces.
// Phase 2 contract: every enricher implements ByIDEnricher in addition
// to AttributeEnricher (issue #571).
var (
	_ AttributeEnricher = (*pubsubSubscriptionEnricher)(nil)
	_ ByIDEnricher      = (*pubsubSubscriptionEnricher)(nil)
)

func (pubsubSubscriptionEnricher) ResourceType() string { return pubsubSubscriptionTFType }

// Enrich populates ir.Attrs with a typed GooglePubsubSubscription payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.Pubsub is nil; any
// other error reflects a real Pub/Sub API failure.
func (e pubsubSubscriptionEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
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
// translated to ErrNotFound so callers can distinguish "subscription
// deleted since last discover" from a real API failure. See issue #571.
func (e pubsubSubscriptionEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("pubsub_subscription: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

// fetchTyped is the shared helper between Enrich and EnrichByID. It
// performs the client-availability check, derives the fully-qualified
// subscription name, fires the SDK call, and marshals the typed payload.
func (e pubsubSubscriptionEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Pubsub == nil {
		return nil, ErrEnrichClientUnavailable
	}
	full := pubsubSubscriptionFullNameForEnrichIdentity(id, c.ProjectID)
	if full == "" {
		return nil, fmt.Errorf("pubsub_subscription: cannot derive subscription resource name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.NativeIDs["asset_name"])
	}
	s, err := e.fetch(ctx, c.Pubsub, full)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("pubsub_subscription %q: %w", full, ErrNotFound)
		}
		return nil, fmt.Errorf("pubsub_subscription: get %q: %w", full, err)
	}
	typed := mapPubsubSubscription(s, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("pubsub_subscription: marshal Attrs: %w", err)
	}
	return raw, nil
}

// pubsubSubscriptionFullNameForEnrich derives the fully-qualified
// "projects/<proj>/subscriptions/<short>" name required by
// Projects.Subscriptions.Get. The Discoverer populates Identity.ImportID
// with that shape; the asset-name + ProjectID fallback covers any
// future code path that doesn't.
func pubsubSubscriptionFullNameForEnrich(ir *imported.ImportedResource, projectID string) string {
	return pubsubSubscriptionFullNameForEnrichIdentity(&ir.Identity, projectID)
}

// pubsubSubscriptionFullNameForEnrichIdentity is the identity-only
// counterpart used by EnrichByID.
func pubsubSubscriptionFullNameForEnrichIdentity(id *imported.ResourceIdentity, projectID string) string {
	if id == nil {
		return ""
	}
	if id.ImportID != "" {
		return id.ImportID
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if short, err := pubsubSubscriptionNameFromID(asset); err == nil && projectID != "" {
			return fmt.Sprintf("projects/%s/subscriptions/%s", projectID, short)
		}
	}
	return ""
}

func defaultPubsubSubscriptionFetch(ctx context.Context, svc *pubsubv1.Service, fullName string) (*pubsubv1.Subscription, error) {
	return svc.Projects.Subscriptions.Get(fullName).Context(ctx).Do()
}
