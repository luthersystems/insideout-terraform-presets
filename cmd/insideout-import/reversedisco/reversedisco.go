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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport"
)

// AWSAssumeRole is the optional customer-account assume-role identity the AWS
// discoverer adopts before issuing any direct SDK call. It mirrors the
// reverseimport.AWSProviderAuth Terraform's generated provider blocks use, so
// the discoverer's SDK calls run as the SAME principal Terraform's
// assume_role { role_arn } blocks reach the customer account with (#739).
//
// When RoleARN is empty the discoverer uses ambient credentials unchanged —
// the correct behavior for the local CLI, which is typically run with the
// customer credentials already in the environment. When RoleARN is set, the
// base config is wrapped with an STS AssumeRole credentials provider targeting
// that role (and ExternalID, when present).
type AWSAssumeRole struct {
	RoleARN    string
	ExternalID string
}

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
//
// awsAuth carries the optional customer-account assume-role identity. On the
// AWS path, a non-empty awsAuth.RoleARN makes the discoverer's SDK calls run as
// that role (the same one Terraform's provider blocks assume), rather than the
// ambient pod/CLI credentials — the #739 fix. It is ignored on the GCP path.
// The same wrapped config backs both DiscoverByID (dep-chase) and
// DiscoverClosure, so both surfaces inherit the fixed credentials.
func New(ctx context.Context, cloud, region, gcpProjectID, awsEndpointURL string, awsAuth AWSAssumeRole) (reverseimport.Discoverer, func(), error) {
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
		cfg = applyAWSAssumeRole(cfg, awsAuth)
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

// applyAWSAssumeRole returns cfg unchanged when no role is requested, otherwise
// a copy whose Credentials are an STS AssumeRole provider for awsAuth.RoleARN
// (and ExternalID, when set). The AssumeRole provider lazily calls
// sts:AssumeRole on first credential retrieval and caches/refreshes the
// short-lived session, so construction issues no network call — a discoverer
// built for an unreachable role still constructs and only fails when it makes
// its first real SDK call (surfaced as an AccessDenied that the engine's #739
// graceful degradation now tolerates).
//
// It is a package-level var so unit tests can swap in a recorder that asserts
// the role/external-id without standing up a live STS endpoint.
var applyAWSAssumeRole = func(cfg aws.Config, awsAuth AWSAssumeRole) aws.Config {
	roleARN := strings.TrimSpace(awsAuth.RoleARN)
	if roleARN == "" {
		return cfg
	}
	stsClient := sts.NewFromConfig(cfg)
	provider := stscreds.NewAssumeRoleProvider(stsClient, roleARN, func(o *stscreds.AssumeRoleOptions) {
		if externalID := strings.TrimSpace(awsAuth.ExternalID); externalID != "" {
			o.ExternalID = aws.String(externalID)
		}
	})
	cfg.Credentials = aws.NewCredentialsCache(provider)
	return cfg
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
