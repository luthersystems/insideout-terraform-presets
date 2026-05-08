package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_pubsub_subscription.
//
// Cloud Asset Inventory: pubsub.googleapis.com/Subscription
// Asset name shape:      //pubsub.googleapis.com/projects/<proj>/subscriptions/<name>
// Terraform import ID:   projects/<proj>/subscriptions/<name>

const (
	pubsubSubscriptionTFType    = "google_pubsub_subscription"
	pubsubSubscriptionAssetType = "pubsub.googleapis.com/Subscription"
)

type pubsubSubscriptionDiscoverer struct{}

func newPubsubSubscriptionDiscoverer() Discoverer { return &pubsubSubscriptionDiscoverer{} }

func (pubsubSubscriptionDiscoverer) ResourceType() string { return pubsubSubscriptionTFType }
func (pubsubSubscriptionDiscoverer) AssetType() string    { return pubsubSubscriptionAssetType }

func (pubsubSubscriptionDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, name)
	return makeImportedResource(book, pubsubSubscriptionTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (pubsubSubscriptionDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := pubsubSubscriptionNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, name)
	return makeImportedResource(addressBook{}, pubsubSubscriptionTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/subscriptions/%s", pubsubAssetHost, projectID, name),
	}, nil), nil
}

func pubsubSubscriptionNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("pubsub_subscription: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/subscriptions/"); idx >= 0 {
		return id[idx+len("/subscriptions/"):], nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("pubsub_subscription: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
