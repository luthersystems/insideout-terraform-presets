// Package reversedisco builds a closure-capable cloud discoverer for the
// reverse-import engine (pkg/reverseimport).
//
// The reverse-import engine (pkg/reverseimport) optionally expands the
// operator's selected parent resources into their registered child resources
// ("auto-included N dependencies") and chases dangling references during
// dep-chase. Both behaviors require a discoverer that implements the engine's
// reverseimport.Discoverer (DiscoverByID) and reverseimport.ClosureDiscoverer
// (DiscoverClosure) surfaces. When neither is wired the engine silently emits
// the `selection_closure_unavailable` diagnostic and skips closure + dep-chase.
//
// This package is the single shared wiring used by both the local CLI
// (cmd/insideout-import reverse) and the Mars reverse-import job
// (luthersystems/mars internal/reverseimportjob). It previously lived in the
// CLI's package main (newReverseDiscoverer + awsAggAdapter/gcpAggAdapter), so
// Mars could not reach it and shipped with no discoverer at all — closure
// expansion no-op'd in production. See luthersystems/mars#195.
package reversedisco

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport"
)

// New constructs a closure-capable discoverer for the given cloud, suitable
// for reverseimport.Options.Discoverer (the returned value also satisfies
// reverseimport.ClosureDiscoverer, so the engine resolves both the dep-chase
// and selection-closure surfaces from it).
//
// region/gcpProjectID/awsEndpointURL mirror the reverseimport.Options of the
// same name: region is the AWS region (or GCP provider region) the underlying
// SDK clients target, gcpProjectID is the real GCP project ID scoping Cloud
// Asset Inventory, and awsEndpointURL optionally retargets every AWS SDK client
// at a compatible endpoint (e.g. LocalStack).
//
// The returned cleanup func releases any cloud connections (the GCP asset
// searcher's gRPC client); it is always non-nil and safe to call even on the
// AWS path (where it is a no-op). Callers should defer it.
func New(ctx context.Context, cloud, region, gcpProjectID, awsEndpointURL string) (reverseimport.Discoverer, func(), error) {
	switch cloud {
	case "aws":
		opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
		opts = append(opts, awsdiscover.RetryLoadOptions()...)
		if awsEndpointURL != "" {
			opts = append(opts, config.WithBaseEndpoint(awsEndpointURL))
		}
		cfg, err := config.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, func() {}, err
		}
		return awsAggAdapter{d: awsdiscover.NewAWSDiscovererWithConcurrency(cfg, awsdiscover.DefaultMaxConcurrency)}, func() {}, nil
	case "gcp":
		searcher, err := gcpdiscover.NewRealAssetSearcher(ctx)
		if err != nil {
			return nil, func() {}, err
		}
		return gcpAggAdapter{d: gcpdiscover.NewGCPDiscoverer(searcher, gcpProjectID, gcpdiscover.GCPDiscovererOpts{})}, func() { _ = searcher.Close() }, nil
	default:
		return nil, func() {}, fmt.Errorf("unknown cloud %q", cloud)
	}
}

// awsAggAdapter wraps *awsdiscover.AWSDiscoverer so it satisfies both
// reverseimport.Discoverer (DiscoverByID) and reverseimport.ClosureDiscoverer
// (DiscoverClosure).
type awsAggAdapter struct {
	d *awsdiscover.AWSDiscoverer
}

func (a awsAggAdapter) DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error) {
	return a.d.DiscoverByID(ctx, tfType, id, region, accountID)
}

func (a awsAggAdapter) DiscoverClosure(ctx context.Context, req reverseimport.ClosureRequest) ([]imported.ImportedResource, error) {
	types := unionStrings(req.ParentTypes, req.ChildTypes)
	return a.d.DiscoverTypes(ctx, types, awsdiscover.DiscoverArgs{
		Project:   req.Project,
		Regions:   req.Regions,
		AccountID: req.AccountID,
	})
}

// gcpAggAdapter is the GCP analogue of awsAggAdapter.
type gcpAggAdapter struct {
	d *gcpdiscover.GCPDiscoverer
}

func (a gcpAggAdapter) DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error) {
	return a.d.DiscoverByID(ctx, tfType, id, region, accountID)
}

func (a gcpAggAdapter) DiscoverClosure(ctx context.Context, req reverseimport.ClosureRequest) ([]imported.ImportedResource, error) {
	types := unionStrings(req.ParentTypes, req.ChildTypes)
	return a.d.DiscoverTypes(ctx, types, gcpdiscover.DiscoverArgs{
		Project: req.Project,
		Regions: req.Regions,
	})
}

func unionStrings(groups ...[]string) []string {
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
