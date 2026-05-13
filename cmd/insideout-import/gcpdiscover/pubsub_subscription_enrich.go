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

func (pubsubSubscriptionEnricher) ResourceType() string { return pubsubSubscriptionTFType }

// Enrich populates ir.Attrs with a typed GooglePubsubSubscription payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.Pubsub is nil; any
// other error reflects a real Pub/Sub API failure.
func (e pubsubSubscriptionEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.Pubsub == nil {
		return ErrEnrichClientUnavailable
	}
	full := pubsubSubscriptionFullNameForEnrich(ir, c.ProjectID)
	if full == "" {
		return fmt.Errorf("pubsub_subscription: cannot derive subscription resource name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NativeIDs["asset_name"])
	}
	s, err := e.fetch(ctx, c.Pubsub, full)
	if err != nil {
		return fmt.Errorf("pubsub_subscription: get %q: %w", full, err)
	}
	typed := mapPubsubSubscription(s, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("pubsub_subscription: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// pubsubSubscriptionFullNameForEnrich derives the fully-qualified
// "projects/<proj>/subscriptions/<short>" name required by
// Projects.Subscriptions.Get. The Discoverer populates Identity.ImportID
// with that shape; the asset-name + ProjectID fallback covers any
// future code path that doesn't.
func pubsubSubscriptionFullNameForEnrich(ir *imported.ImportedResource, projectID string) string {
	if ir.Identity.ImportID != "" {
		return ir.Identity.ImportID
	}
	if asset := ir.Identity.NativeIDs["asset_name"]; asset != "" {
		if short, err := pubsubSubscriptionNameFromID(asset); err == nil && projectID != "" {
			return fmt.Sprintf("projects/%s/subscriptions/%s", projectID, short)
		}
	}
	return ""
}

func defaultPubsubSubscriptionFetch(ctx context.Context, svc *pubsubv1.Service, fullName string) (*pubsubv1.Subscription, error) {
	return svc.Projects.Subscriptions.Get(fullName).Context(ctx).Do()
}
