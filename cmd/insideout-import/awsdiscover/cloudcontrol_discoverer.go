package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	cctypes "github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// cloudControlClient is the narrow subset of the AWS Cloud Control API
// the generic discoverer uses. Both real *cloudcontrol.Client and
// in-test fakes satisfy this interface; tests inject the latter via the
// `new` closure on cloudControlDiscoverer to exercise pagination, error
// propagation, and per-item soft-fail semantics without an AWS account.
//
// Only the two RPCs the discoverer issues are listed: ListResources
// (paginated identifier enumeration) and GetResource (per-identifier
// properties fetch). Tag-aware filtering rides entirely on the
// properties payload — Cloud Control has no server-side tag filter.
type cloudControlClient interface {
	ListResources(ctx context.Context, in *cloudcontrol.ListResourcesInput, opts ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error)
	GetResource(ctx context.Context, in *cloudcontrol.GetResourceInput, opts ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error)
}

// cloudControlConfig is the per-Terraform-type contract for routing a
// resource type through the generic Cloud Control discoverer. Every
// entry in cloudControlTypeConfigs (see cloudcontrol_types.go) populates
// this struct so the generic Discover/DiscoverByID can be shared across
// dozens of CloudFormation-shaped types without per-type SDK code.
//
// Field semantics:
//
//   - TFType / CloudFormationType: Terraform-side and CloudFormation-side
//     type identifiers. Both are required.
//   - Slug: progress-event service slug (matches serviceSlugByTFType for
//     this TFType). Used for ServiceStart/ServiceFinish/ItemFound emits.
//   - IsGlobal: when true, the discoverer issues one call (region="")
//     instead of looping args.Regions. Maps to CloudFormation type-level
//     global classifications (e.g. AWS::IAM::Role would be global).
//   - ImportIDFromIdentifier: converts the Cloud Control primary
//     identifier string (which uses "|" as a separator for compound
//     identifiers) into the import ID format the Terraform AWS provider
//     accepts (which usually uses ":" or "/" instead). Some types are
//     simple passthroughs; others need a separator rewrite. The
//     properties payload is passed in case the identifier alone is
//     ambiguous.
//   - NameHintFromProperties: extracts the human-readable name (becomes
//     Identity.NameHint and NativeIDs["name"]). Falls back to the
//     identifier if the properties payload is missing a name field.
//   - NativeIDsFromProperties: optional extra cloud-side IDs to stamp
//     under Identity.NativeIDs (e.g. ARN, queue URL). Returns nil if
//     no extras are needed.
//   - TagsFromProperties: extracts the tag map from the properties
//     payload. Returns nil (not empty) when the resource simply carries
//     no tags — the nil-vs-empty distinction is load-bearing for
//     downstream selector matching. Exception: genuinely-untaggable
//     types (paired with SkipProjectTagFilter=true) use the
//     emptyTagsExtractor helper which returns a non-nil empty map so
//     in-memory consumers can iterate without nil-check. JSON output
//     elides the field either way via `omitempty`.
//   - ParentLister: optional. When set, the discoverer fans out one
//     ListResources call per parent context (e.g. AWS::Cognito::UserPoolClient
//     is parent-scoped on UserPoolId). The returned slice contains one
//     ResourceModel JSON-string per parent; the discoverer threads it
//     through ListResourcesInput.ResourceModel. Returns nil for non-
//     parent-scoped types. Receives an aws.Config so the lister can
//     construct typed SDK clients to enumerate parents
//     (cognito-idp:ListUserPools, lambda:ListFunctions, …).
//     Mutually exclusive with SDKLister; setting both panics at
//     registration time.
//   - SDKLister: optional. When set, the discoverer bypasses Cloud
//     Control ListResources entirely and seeds the per-identifier
//     GetResource fan-out with the identifiers this function returns.
//     Use for types where CC GetResource is supported but CC
//     ListResources returns UnsupportedActionException (e.g.
//     AWS::Cognito::UserPoolDomain via cognito-idp:DescribeUserPool;
//     AWS::CertificateManager::Certificate via acm:ListCertificates).
//     Mutually exclusive with ParentLister; setting both panics at
//     registration time.
//   - SkipProjectTagFilter: when true, the discoverer (a) bypasses the
//     RGT-cache short-circuit and always drives through ListResources,
//     and (b) bypasses the post-fetch `args.Project` Project-tag filter
//     for this type. Set this for genuinely-untaggable types (e.g.
//     AWS::IAM::InstanceProfile, AWS::Backup::BackupSelection) whose
//     CFN schema has no Tags property and whose ARNs never surface via
//     RGT. Without (a), the cache reports authoritative-empty for these
//     types (RGT can't see them) and the discoverer emits zero.
//
//     The flag does NOT bypass the args.TagSelectors filter: that
//     filter is operator-explicit (the operator typed --tag-selector
//     foo=bar) and the right behavior for untaggable types is "no
//     match" because they carry no tags. Operators combining
//     --tag-selector with untaggable types will get zero items; the
//     CLI can be invoked with --resource-types to exclude untaggable
//     types from such scans.
//
//     The other trade-off: scoping a discover via --project on these
//     types returns every instance in the account rather than only
//     project-tagged ones.
type cloudControlConfig struct {
	TFType                  string
	CloudFormationType      string
	Slug                    string
	IsGlobal                bool
	SkipProjectTagFilter    bool
	ImportIDFromIdentifier  func(identifier string, props map[string]any) string
	NameHintFromProperties  func(identifier string, props map[string]any) string
	NativeIDsFromProperties func(identifier string, props map[string]any) map[string]string
	TagsFromProperties      func(props map[string]any) map[string]string
	ParentLister            func(ctx context.Context, awsCfg aws.Config, region string, args DiscoverArgs) ([]string, error)
	SDKLister               func(ctx context.Context, awsCfg aws.Config, region string, args DiscoverArgs) ([]string, error)
}

// cloudControlDiscoverer is the generic per-type Discoverer that routes
// a Terraform resource type through the AWS Cloud Control API. One
// instance is constructed per registered TFType; the per-type behavior
// lives entirely in cfg (see cloudControlConfig).
//
// The same struct handles bulk Discover (paginated ListResources +
// bounded errgroup fan-out for GetResource) and single-resource
// DiscoverByID (one GetResource call). Per-item GetResource failures
// soft-fail through ServiceWarn so a single throttled item does not
// abort the whole region's scope — matches the gcpdiscover Bundle 11
// non-CAI fanout posture.
type cloudControlDiscoverer struct {
	cfg            cloudControlConfig
	awsCfg         aws.Config
	new            func(region string) cloudControlClient
	maxConcurrency int
}

func newCloudControlDiscoverer(cfg cloudControlConfig, awsCfg aws.Config, maxConcurrency int) *cloudControlDiscoverer {
	if cfg.SDKLister != nil && cfg.ParentLister != nil {
		// Programming error at registration time, not a runtime
		// failure: a registrant that wires both fields would silently
		// pick one branch over the other and quietly skip the other's
		// enumeration. Panic so the regression surfaces in any test
		// run that constructs the discoverer.
		panic(fmt.Sprintf("cloudControlConfig %s: SDKLister and ParentLister are mutually exclusive", cfg.TFType))
	}
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &cloudControlDiscoverer{
		cfg:    cfg,
		awsCfg: awsCfg,
		new: func(region string) cloudControlClient {
			return cloudcontrol.NewFromConfig(awsCfg, func(o *cloudcontrol.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

// ResourceType returns the Terraform type this discoverer covers.
func (d *cloudControlDiscoverer) ResourceType() string { return d.cfg.TFType }

// Discover paginates Cloud Control ListResources for the configured
// CloudFormationType across args.Regions (or a single "" region for
// global types), then fans out per-identifier GetResource calls under
// a bounded errgroup. Each GetResource response's properties payload is
// parsed via the per-type extractors; tag filtering applies
// args.TagSelectors and the legacy args.Project="Project" back-compat
// check from lambda.go.
//
// Per-item GetResource errors are soft-fails: a ServiceWarn is emitted
// and the item is skipped. Parent-context cancellation propagates via
// gctx, so a shutdown signal still tears down in-flight goroutines
// cleanly. ListResources errors abort the region.
func (d *cloudControlDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	var out []imported.ImportedResource

	regions := args.Regions
	if d.cfg.IsGlobal {
		regions = []string{""}
	}

	for _, region := range regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(d.cfg.Slug, region)
		client := d.new(region)
		// regionCount tracks the per-region emit count so every
		// ServiceFinish on every exit path attributes the right number
		// to *this* region (not the cross-region accumulator). Matches
		// the pattern in sqs.go / lambda.go.
		regionCount := 0

		// Aggregate identifiers across all parents (or via RGT
		// prefetch — see below) before fanning out per-item
		// GetResource so the bounded errgroup runs against the full
		// work set.
		type itemRef struct {
			identifier  string
			parentModel string
			parentLabel string // for ServiceWarn context
			// rgtTags is the tag map returned by the RGT prefetcher
			// when this ref came from the cache. Non-nil means the
			// caller already filtered server-side by Project/TagSelectors
			// so the post-fetch tag filter becomes belt-and-suspenders.
			// Nil for refs sourced from ListResources.
			rgtTags map[string]string
		}
		var refs []itemRef

		// RGT prefetch cache short-circuit: when the orchestrator's
		// pre-pass found ARNs for our CloudFormation type, skip
		// ListResources entirely. The cache is empty (or absent) for
		// types whose ARNs don't carry tags, when the caller passed
		// no TagSelectors/Project, or when the per-region RGT call
		// failed (downgraded to warn, not error). See rgt_prefetcher.go
		// and #406. The cache also bypasses the ParentLister branch —
		// each cached ARN is a self-contained CC identifier, no
		// parent context required.
		//
		// Untaggable types (SkipProjectTagFilter=true) bypass the cache
		// entirely. RGT can only see tagged ARNs, so for genuinely
		// untaggable types (AWS::IAM::InstanceProfile,
		// AWS::Backup::BackupSelection, …) the cache is authoritatively
		// empty — trusting it would emit zero. ListResources is the
		// only path that can surface these; the legacy Project filter
		// further down is already skipped for these types.
		cacheUsed := false
		if !d.cfg.SkipProjectTagFilter {
			if d.cfg.IsGlobal {
				if cached, ok := args.RGTCacheForGlobalCFN(d.cfg.CloudFormationType); ok {
					for _, info := range cached {
						refs = append(refs, itemRef{identifier: info.Identifier, rgtTags: info.Tags})
					}
					cacheUsed = true
				}
			} else if cached, ok := args.RGTCacheForCFN(region, d.cfg.CloudFormationType); ok {
				for _, info := range cached {
					refs = append(refs, itemRef{identifier: info.Identifier, rgtTags: info.Tags})
				}
				cacheUsed = true
			}
		}

		if !cacheUsed {
			if d.cfg.SDKLister != nil {
				// Native-SDK enumeration: types whose CC ListResources
				// returns UnsupportedActionException despite CC
				// GetResource being supported (e.g.
				// AWS::Cognito::UserPoolDomain via
				// cognito-idp:DescribeUserPool walking;
				// AWS::CertificateManager::Certificate via
				// acm:ListCertificates). SDKLister returns primary
				// identifiers directly; the standard GetResource
				// fan-out + extractor pipeline runs unchanged.
				ids, err := d.cfg.SDKLister(ctx, d.awsCfg, region, args)
				if err != nil {
					args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
					return nil, fmt.Errorf("%s SDK enumeration (region=%s): %w", d.cfg.Slug, region, err)
				}
				if len(ids) == 0 {
					args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
					continue
				}
				for _, id := range ids {
					refs = append(refs, itemRef{identifier: id})
				}
			} else {
				// Per-parent enumeration: ParentLister returns N
				// parent-scoped resource-model JSON strings; for
				// non-parent types it returns nil and we issue one
				// ListResources without a ResourceModel.
				parentModels := []string{""}
				if d.cfg.ParentLister != nil {
					models, err := d.cfg.ParentLister(ctx, d.awsCfg, region, args)
					if err != nil {
						args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
						return nil, fmt.Errorf("%s parent enumeration (region=%s): %w", d.cfg.Slug, region, err)
					}
					parentModels = models
					if len(parentModels) == 0 {
						args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
						continue
					}
				}

				for _, parentModel := range parentModels {
					input := &cloudcontrol.ListResourcesInput{
						TypeName: aws.String(d.cfg.CloudFormationType),
					}
					if parentModel != "" {
						input.ResourceModel = aws.String(parentModel)
					}
					paginator := cloudcontrol.NewListResourcesPaginator(client, input)
					for paginator.HasMorePages() {
						page, err := paginator.NextPage(ctx)
						if err != nil {
							args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
							return nil, fmt.Errorf("ListResources %s (region=%s): %w", d.cfg.CloudFormationType, region, err)
						}
						for _, desc := range page.ResourceDescriptions {
							refs = append(refs, itemRef{
								identifier:  aws.ToString(desc.Identifier),
								parentModel: parentModel,
								parentLabel: parentLabelFromModel(parentModel),
							})
						}
					}
				}
			}
		}

		// Per-identifier GetResource fan-out under bounded errgroup.
		type fetched struct {
			identifier  string
			parentModel string
			props       map[string]any
			tags        map[string]string
		}
		var (
			mu   sync.Mutex
			done []fetched
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, ref := range refs {
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				in := &cloudcontrol.GetResourceInput{
					TypeName:   aws.String(d.cfg.CloudFormationType),
					Identifier: aws.String(ref.identifier),
				}
				resp, err := client.GetResource(gctx, in)
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					args.Emitter.ServiceWarn(d.cfg.Slug, region,
						fmt.Sprintf("GetResource %s %q%s: %v", d.cfg.CloudFormationType, ref.identifier, ref.parentLabel, err))
					return nil
				}
				props, err := parsePropertiesPayload(resp)
				if err != nil {
					args.Emitter.ServiceWarn(d.cfg.Slug, region,
						fmt.Sprintf("parse properties %s %q: %v", d.cfg.CloudFormationType, ref.identifier, err))
					return nil
				}
				// Prefer the RGT-supplied tag map when present —
				// RGT already filtered server-side and the tag map
				// is authoritative. Falls back to
				// TagsFromProperties for refs sourced via
				// ListResources (RGT cache miss).
				var tags map[string]string
				if ref.rgtTags != nil {
					tags = ref.rgtTags
				} else if d.cfg.TagsFromProperties != nil {
					tags = d.cfg.TagsFromProperties(props)
				}
				mu.Lock()
				done = append(done, fetched{identifier: ref.identifier, parentModel: ref.parentModel, props: props, tags: tags})
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("GetResource %s (region=%s): %w", d.cfg.CloudFormationType, region, err)
		}

		sort.Slice(done, func(i, j int) bool { return done[i].identifier < done[j].identifier })

		for _, f := range done {
			// Legacy Project tag back-compat. Skipped on the RGT-cache
			// hit path because the prefetcher already filtered
			// server-side; running it again would force a redundant
			// map lookup per resource.
			//
			// Additional skip: cfg.SkipProjectTagFilter is set for
			// genuinely-untaggable types (e.g. AWS::IAM::InstanceProfile,
			// AWS::Backup::BackupSelection) whose CFN schema has no
			// Tags property — their tag bag is always empty by design,
			// so applying the Project filter would silently drop every
			// item. Operators scoping a discover via --project get all
			// instances of these types account-wide.
			cacheUsedForRef := cacheUsed
			if !cacheUsedForRef && !d.cfg.SkipProjectTagFilter && args.Project != "" && f.tags["Project"] != args.Project {
				continue
			}
			if !MatchesAll(f.tags, args.TagSelectors) {
				continue
			}
			importID := f.identifier
			if d.cfg.ImportIDFromIdentifier != nil {
				importID = d.cfg.ImportIDFromIdentifier(f.identifier, f.props)
			}
			name := f.identifier
			if d.cfg.NameHintFromProperties != nil {
				name = d.cfg.NameHintFromProperties(f.identifier, f.props)
			}
			var native map[string]string
			if d.cfg.NativeIDsFromProperties != nil {
				native = d.cfg.NativeIDsFromProperties(f.identifier, f.props)
			}
			out = append(out, makeImportedResource(
				book,
				d.cfg.TFType,
				name,
				importID,
				region,
				args.AccountID,
				native,
				f.tags,
			))
			args.Emitter.ItemFound(d.cfg.Slug, region, d.cfg.TFType, importID)
			regionCount++
		}
		args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a single Cloud Control resource by its
// identifier, in the given region. Used by Stage 2c3's dep-chase loop.
// An empty id, an id Cloud Control's GetResource rejects as malformed,
// or an id pointing at a deleted resource each yield (zero, ErrNotFound)
// or (zero, ErrNotSupported) appropriately.
func (d *cloudControlDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("%s: empty id: %w", d.cfg.TFType, ErrNotSupported)
	}
	client := d.new(region)
	resp, err := client.GetResource(ctx, &cloudcontrol.GetResourceInput{
		TypeName:   aws.String(d.cfg.CloudFormationType),
		Identifier: aws.String(id),
	})
	if err != nil {
		if isCloudControlNotFound(err) {
			return imported.ImportedResource{}, fmt.Errorf("%s %q: %w", d.cfg.TFType, id, ErrNotFound)
		}
		if isCloudControlMalformedIdentifier(err) {
			// Cloud Control rejected the identifier shape — Stage 2c3's
			// dep-chase loop treats this as "not parseable by this
			// discoverer" so it can try a different one.
			return imported.ImportedResource{}, fmt.Errorf("%s %q: %w", d.cfg.TFType, id, ErrNotSupported)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetResource %s: %w", d.cfg.CloudFormationType, err)
	}
	props, err := parsePropertiesPayload(resp)
	if err != nil {
		return imported.ImportedResource{}, fmt.Errorf("parse properties %s %q: %w", d.cfg.CloudFormationType, id, err)
	}
	importID := id
	if d.cfg.ImportIDFromIdentifier != nil {
		importID = d.cfg.ImportIDFromIdentifier(id, props)
	}
	name := id
	if d.cfg.NameHintFromProperties != nil {
		name = d.cfg.NameHintFromProperties(id, props)
	}
	var native map[string]string
	if d.cfg.NativeIDsFromProperties != nil {
		native = d.cfg.NativeIDsFromProperties(id, props)
	}
	var tags map[string]string
	if d.cfg.TagsFromProperties != nil {
		tags = d.cfg.TagsFromProperties(props)
	}
	return makeImportedResource(
		addressBook{},
		d.cfg.TFType,
		name,
		importID,
		region,
		accountID,
		native,
		tags,
	), nil
}

// parsePropertiesPayload extracts the JSON properties blob from a Cloud
// Control GetResource response into a map. Cloud Control returns the
// properties as a JSON-encoded string under ResourceDescription.Properties,
// so the discoverer parses it once and hands the map to the per-type
// extractors. Returns an error only when the payload is malformed —
// downstream extractors tolerate missing or nil fields by returning
// zero values, which the discoverer maps to "no tags" / "fall back to
// identifier" semantics.
func parsePropertiesPayload(resp *cloudcontrol.GetResourceOutput) (map[string]any, error) {
	if resp == nil || resp.ResourceDescription == nil {
		return nil, errors.New("nil resource description")
	}
	raw := aws.ToString(resp.ResourceDescription.Properties)
	if raw == "" {
		return map[string]any{}, nil
	}
	var props map[string]any
	if err := json.Unmarshal([]byte(raw), &props); err != nil {
		return nil, fmt.Errorf("unmarshal properties: %w", err)
	}
	return props, nil
}

// isCloudControlNotFound returns true when the underlying SDK error is
// Cloud Control's ResourceNotFoundException. Used by DiscoverByID to
// distinguish a legitimately-missing resource (worth a Stage-2c3 warning)
// from a real SDK fault (a hard error). The Cloud Control SDK exposes
// the typed exception in cctypes; smithy.APIError ErrorCode is the
// fallback when the typed shape evolves.
func isCloudControlNotFound(err error) bool {
	if err == nil {
		return false
	}
	var notFound *cctypes.ResourceNotFoundException
	if errors.As(err, &notFound) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "ResourceNotFoundException"
	}
	return false
}

// isCloudControlMalformedIdentifier returns true when the underlying SDK
// error is Cloud Control's ValidationException or InvalidRequestException
// — the codes the service returns when a primary-identifier string is
// the wrong shape for the requested TypeName (e.g. an SQS queue URL
// passed for AWS::Backup::BackupVault). Stage 2c3's dep-chase loop
// treats this as "this discoverer cannot parse the id", letting it
// fall through to a sibling discoverer rather than hard-failing.
func isCloudControlMalformedIdentifier(err error) bool {
	if err == nil {
		return false
	}
	var validation *cctypes.InvalidRequestException
	if errors.As(err, &validation) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "ValidationException" || code == "InvalidRequestException"
	}
	return false
}

// parentLabelFromModel formats a parent-scoped resource-model JSON for
// inclusion in a soft-fail ServiceWarn message. Returns "" when the
// model is empty (non-parent-scoped scope). The "(parent=…)" suffix
// includes a leading space so call sites can use a bare "%s" concat
// without their own separator; that placement is load-bearing because
// the suffix is conditionally empty.
func parentLabelFromModel(parentModel string) string {
	if parentModel == "" {
		return ""
	}
	return fmt.Sprintf(" (parent=%s)", parentModel)
}

// extractStringMap reads a string→string map from a JSON-decoded
// properties subtree at the given key. Used by per-type TagsFromProperties
// extractors when the cloud schema represents tags as a flat
// {"key": "value"} object (e.g. AWS::Backup::BackupVault.BackupVaultTags).
// Returns nil when the key is absent or carries a non-map value.
func extractStringMap(props map[string]any, key string) map[string]string {
	if props == nil {
		return nil
	}
	raw, ok := props[key]
	if !ok || raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if len(m) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			continue
		}
		out[k] = s
	}
	return out
}

// extractTagList reads a list-of-{Key,Value} pairs from a JSON-decoded
// properties subtree at the given key. Used by per-type TagsFromProperties
// extractors when the cloud schema represents tags as an array of
// objects (e.g. AWS::CloudWatch::Alarm.Tags = [{"Key":"k","Value":"v"}]).
// Returns nil when the key is absent or the slice is nil; returns an
// empty (non-nil) map when the slice is present but empty.
func extractTagList(props map[string]any, key string) map[string]string {
	if props == nil {
		return nil
	}
	raw, ok := props[key]
	if !ok || raw == nil {
		return nil
	}
	slice, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(slice))
	for _, entry := range slice {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		k, _ := obj["Key"].(string)
		v, _ := obj["Value"].(string)
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// extractString reads a string at the given properties key, returning
// "" when absent or non-string. Convenience helper for NameHintFromProperties
// extractors that pull a Name / FunctionName / ClusterArn-style field.
func extractString(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	v, ok := props[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
