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
		// GCP credential context (#777, the GCP analog of the AWS
		// mars#198 wrong-principal fix). NewRealAssetSearcher with no
		// option.ClientOption falls back to Application Default
		// Credentials. In the Mars reverse-import job that ADC is the
		// customer-scoped credential the job is launched with (the WIF /
		// service-account identity that can read the customer's Cloud
		// Asset Inventory), NOT the pod-default identity — so the closure
		// sweep runs as the same principal Terraform's google provider
		// reaches the customer project with. There is no STS-style
		// assume-role hop to wire here (the AWS awsAuth.RoleARN path):
		// GCP impersonation/WIF is established before the process starts,
		// via GOOGLE_APPLICATION_CREDENTIALS / the metadata server /
		// option.WithTokenSource at the caller, so this constructor
		// inherits the right principal without an explicit role argument.
		// Multi-tenant server-side callers that must carry per-request
		// credentials pass an option.WithTokenSource(ts) (see
		// NewRealAssetSearcher's #445 doc); the Mars job runs one
		// customer per process, so process-default ADC is correct here.
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
		Project:     req.Project,
		Regions:     req.Regions,
		AccountID:   req.AccountID,
		ParentScope: awsParentScope(req.ParentResources),
	})
}

// awsParentScope builds the per-CloudFormation-type selected-parent scope the
// #739 closure-scoping fix uses to restrict child + parent re-discovery to the
// operator's selected parents. For each selected parent whose Terraform type is
// a known Cloud Control type it records the parent's identifier (its ImportID,
// falling back to NameHint) AND its region (Identity.Region) under the parent's
// CloudFormation type. Parent types not routed through Cloud Control are
// skipped — their children are discovered account-wide and the engine's
// mergeClosureResources still filters them to the selected parents, so closure
// semantics are unchanged. Returns nil when no usable scope can be built (the
// caller then sweeps account-wide as before).
//
// The region is load-bearing for multi-region closures: each scoped seam
// enumerates a parent's sub-resources only in the parent's region, so the same
// bucket / log group is not re-fetched once per requested region. A region-less
// parent (Identity.Region == "", e.g. a global type) is enumerated exactly once
// across the request (see ParentScope.scopedParents).
func awsParentScope(parents []imported.ImportedResource) awsdiscover.ParentScope {
	byCFN := map[string][]awsdiscover.ScopedParent{}
	for _, p := range parents {
		cfnType, ok := awsdiscover.CloudFormationTypeForTF(p.Identity.Type)
		if !ok {
			continue
		}
		id := strings.TrimSpace(p.Identity.ImportID)
		if id == "" {
			id = strings.TrimSpace(p.Identity.NameHint)
		}
		if id == "" {
			continue
		}
		region := strings.TrimSpace(p.Identity.Region)
		sp := awsdiscover.ScopedParent{Identifier: id, Region: region}
		byCFN[cfnType] = append(byCFN[cfnType], sp)
		// Also scope any child whose CC identifier IS this parent's
		// identifier (e.g. aws_s3_bucket_policy, whose identifier is the
		// bucket name) so its discovery is restricted to the selected
		// parents too — no account-wide list of the child type. The child
		// inherits the parent's region so the per-region enumeration is
		// scoped identically (#739 codex follow-up).
		for _, childCFN := range awsdiscover.IdentifierSharedChildCFNTypes(cfnType) {
			byCFN[childCFN] = append(byCFN[childCFN], sp)
		}
	}
	return awsdiscover.NewParentScope(byCFN)
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
		Project:     req.Project,
		Regions:     req.Regions,
		ParentScope: a.gcpParentScope(req.ParentResources),
	})
}

// gcpParentScope builds the per-Cloud-Asset-type selected-parent scope the
// #777 closure-scoping fix uses to restrict GCP child + parent
// re-enumeration to the operator's selected parents — the GCP twin of
// awsParentScope. For each selected parent whose Terraform type maps to a
// registered Cloud Asset type it records the parent's name (its NameHint,
// falling back to ImportID) AND its location (Identity.Location) under the
// parent's asset type. Parent types with no registered discoverer are
// skipped — their children fall back to the project-wide CAI sweep and the
// engine's mergeClosureResources still filters them to the selected
// parents, so closure semantics are unchanged. Returns nil when no usable
// scope can be built (the caller then sweeps project-wide as before).
//
// Unlike the AWS path there is no identifier-shared-child fan-out: GCP
// child types are non-CAI (they fan out from the already-scoped parent CAI
// rows in DiscoverTypes' non-CAI phase), so scoping the parent asset type
// is sufficient to scope its children too.
func (a gcpAggAdapter) gcpParentScope(parents []imported.ImportedResource) gcpdiscover.ParentScope {
	byAsset := map[string][]gcpdiscover.ScopedParent{}
	for _, p := range parents {
		assetType, ok := a.d.AssetTypeForTF(p.Identity.Type)
		if !ok {
			continue
		}
		name := strings.TrimSpace(p.Identity.NameHint)
		if name == "" {
			name = strings.TrimSpace(p.Identity.ImportID)
		}
		if name == "" {
			continue
		}
		location := strings.TrimSpace(p.Identity.Location)
		byAsset[assetType] = append(byAsset[assetType], gcpdiscover.ScopedParent{Name: name, Location: location})
	}
	return gcpdiscover.NewParentScope(byAsset)
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
