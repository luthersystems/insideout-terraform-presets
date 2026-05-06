package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_pubsub_topic.
//
// Cloud Asset Inventory: pubsub.googleapis.com/Topic
// Asset name shape:      //pubsub.googleapis.com/projects/<proj>/topics/<name>
// Terraform import ID:   projects/<proj>/topics/<name>
//
// Pub/Sub topics are project-global — they have no `location`, so the
// FromAsset translation leaves Identity.Location empty. The provider's
// import handler at this writing also accepts the bare topic name
// (deriving project from the provider config), but emitting the fully-
// qualified `projects/<proj>/topics/<name>` form is unambiguous and
// matches the archive PR58 reference.

const (
	pubsubTopicTFType    = "google_pubsub_topic"
	pubsubTopicAssetType = "pubsub.googleapis.com/Topic"
)

type pubsubTopicDiscoverer struct{}

func newPubsubTopicDiscoverer() Discoverer { return &pubsubTopicDiscoverer{} }

func (pubsubTopicDiscoverer) ResourceType() string { return pubsubTopicTFType }
func (pubsubTopicDiscoverer) AssetType() string    { return pubsubTopicAssetType }

func (pubsubTopicDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/topics/%s", projectID, name)
	return makeImportedResource(book, pubsubTopicTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	})
}

func (pubsubTopicDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := pubsubTopicNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/topics/%s", projectID, name)
	return makeImportedResource(addressBook{}, pubsubTopicTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/topics/%s", pubsubAssetHost, projectID, name),
	}), nil
}

const pubsubAssetHost = "pubsub.googleapis.com"

// pubsubTopicNameFromID extracts the topic name from one of three accepted
// inputs: a Cloud Asset full resource name (//pubsub.googleapis.com/projects/<p>/topics/<n>),
// the projects/<p>/topics/<n> Terraform import-ID form, or a bare topic
// name. Anything else returns ErrNotSupported so dep-chase can route it
// to its unresolvable-warning bucket.
func pubsubTopicNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("pubsub_topic: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/topics/"); idx >= 0 {
		return id[idx+len("/topics/"):], nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("pubsub_topic: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
