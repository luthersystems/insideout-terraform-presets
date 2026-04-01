package discovery

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// AWSDiscoverer runs all per-service discoverers and aggregates results.
type AWSDiscoverer struct {
	discoverers []Discoverer
	logger      *slog.Logger
}

func NewAWSDiscoverer(cfg aws.Config, logger *slog.Logger) *AWSDiscoverer {
	return &AWSDiscoverer{
		discoverers: []Discoverer{
			NewSQSDiscoverer(cfg),
			NewDynamoDBDiscoverer(cfg),
			NewCloudWatchLogsDiscoverer(cfg),
			NewSecretsManagerDiscoverer(cfg),
			NewLambdaDiscoverer(cfg),
		},
		logger: logger,
	}
}

// DiscoverAll runs all discoverers and returns the combined results.
func (d *AWSDiscoverer) DiscoverAll(ctx context.Context, filter Filter) ([]DiscoveredResource, error) {
	var all []DiscoveredResource
	for _, disc := range d.discoverers {
		d.logger.Info("discovering resources", "type", disc.ResourceType())
		resources, err := disc.Discover(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("discover %s: %w", disc.ResourceType(), err)
		}
		d.logger.Info("found resources", "type", disc.ResourceType(), "count", len(resources))
		all = append(all, resources...)
	}
	return all, nil
}

// DiscoverTypes runs only the discoverers matching the given resource types.
func (d *AWSDiscoverer) DiscoverTypes(ctx context.Context, filter Filter, types []string) ([]DiscoveredResource, error) {
	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}

	var all []DiscoveredResource
	for _, disc := range d.discoverers {
		if !typeSet[disc.ResourceType()] {
			continue
		}
		d.logger.Info("discovering resources", "type", disc.ResourceType())
		resources, err := disc.Discover(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("discover %s: %w", disc.ResourceType(), err)
		}
		d.logger.Info("found resources", "type", disc.ResourceType(), "count", len(resources))
		all = append(all, resources...)
	}
	return all, nil
}
