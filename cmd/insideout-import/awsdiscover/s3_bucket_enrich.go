package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// s3BucketTFType is the registered Terraform type for the S3 bucket
// enricher. Kept as a constant so the registry / ResourceType() stay
// in lockstep.
const s3BucketTFType = "aws_s3_bucket"

// s3BucketEnricher implements both AttributeEnricher and ByIDEnricher
// for aws_s3_bucket (#493). Pairs with the Cloud-Control-routed S3
// bucket discoverer registered in cloudControlTypeConfigs.
//
// **Multi-source overlay**: S3 has the largest overlay surface in the
// AWS-side enricher family — a single GetBucket* call returns only one
// configuration sub-resource at a time, so populating the typed
// AWSS3Bucket payload aggregates ~10 SDK calls (HeadBucket for
// existence/region, plus one Get* per configurable block). Following
// the dynamodb_table multi-overlay pattern, each overlay has its own
// function-field hook so tests can inject failures per sub-resource
// without spinning up a fake HTTP server. The load-bearing call is
// HeadBucket: if the bucket does not exist or we can't see it,
// nothing else matters and Enrich fails fast. Every other Get*
// failure is downgraded — we emit whatever succeeded.
//
// **Soft-fail discipline**: per the issue's "more specific about
// which error codes mean 'not configured'" guidance, we use
// isS3NotSetError (sdkonly_helpers.go) to distinguish the service-
// native "feature not configured" codes (NoSuchBucketPolicy,
// NoSuchCorsConfiguration, NoSuchLifecycleConfiguration,
// ServerSideEncryptionConfigurationNotFoundError, NoSuchTagSet,
// NoSuchWebsiteConfiguration, ObjectLockConfigurationNotFoundError)
// from real failures (AccessDenied, ThrottlingException, service
// outage). The two are handled identically at the typed-payload
// level (the block is omitted), but the distinction is preserved so
// future work could route real failures through a per-resource warn
// list. Today both shapes downgrade silently.
//
// **#490 follow-up**: the issue's "Alternative" section notes that
// the Cloud Control unified enricher (#490) could collapse this
// hand-rolled file into a 0-line override once Cloud Control proves
// out for S3. This file stays as the reliable path until that lands;
// after #490, the registration in NewAWSDiscoverer can flip to the
// unified enricher and this file can be retired (the framework
// preserves the override capability for any per-type quirk the
// unified path doesn't model).
//
// Sensitive fields: none on this resource (the bucket policy lives
// in the `policy` field as a JSON document — decision #36 redaction
// is downstream's concern).
type s3BucketEnricher struct {
	// fetchHead is the load-bearing existence probe. Returns the
	// HeadBucket response (carries BucketRegion when the SDK auto-
	// discovers the bucket's region) or an error. A typed NotFound /
	// NoSuchBucket is wrapped as ErrNotFound at the call site; other
	// errors bubble up unchanged. Overridable for tests.
	fetchHead func(ctx context.Context, c *s3.Client, bucket string) (*s3.HeadBucketOutput, error)

	// All fetch* hooks below are best-effort overlays. Each returns
	// (response, nil) when the sub-resource is configured, (nil, nil)
	// when the service-native "not set" code is observed, and (nil,
	// err) for any other failure. The Enrich path treats both nil
	// responses and real errors as "block omitted" — see soft-fail
	// discipline note above for the rationale.
	fetchEncryption func(ctx context.Context, c *s3.Client, bucket string) (*s3types.ServerSideEncryptionConfiguration, error)
	fetchVersioning func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketVersioningOutput, error)
	fetchLifecycle  func(ctx context.Context, c *s3.Client, bucket string) ([]s3types.LifecycleRule, error)
	fetchLogging    func(ctx context.Context, c *s3.Client, bucket string) (*s3types.LoggingEnabled, error)
	fetchCors       func(ctx context.Context, c *s3.Client, bucket string) ([]s3types.CORSRule, error)
	fetchPolicy     func(ctx context.Context, c *s3.Client, bucket string) (*string, error)
	fetchWebsite    func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketWebsiteOutput, error)
	fetchTags       func(ctx context.Context, c *s3.Client, bucket string) ([]s3types.Tag, error)
	fetchObjectLock func(ctx context.Context, c *s3.Client, bucket string) (*s3types.ObjectLockConfiguration, error)
}

// newS3BucketEnricher returns the production-wired enricher.
// AWSDiscoverer's byTypeEnricher map registers this under "aws_s3_bucket".
func newS3BucketEnricher() *s3BucketEnricher {
	return &s3BucketEnricher{
		fetchHead:       defaultS3BucketFetchHead,
		fetchEncryption: defaultS3BucketFetchEncryption,
		fetchVersioning: defaultS3BucketFetchVersioning,
		fetchLifecycle:  defaultS3BucketFetchLifecycle,
		fetchLogging:    defaultS3BucketFetchLogging,
		fetchCors:       defaultS3BucketFetchCors,
		fetchPolicy:     defaultS3BucketFetchPolicy,
		fetchWebsite:    defaultS3BucketFetchWebsite,
		fetchTags:       defaultS3BucketFetchTags,
		fetchObjectLock: defaultS3BucketFetchObjectLock,
	}
}

func (s3BucketEnricher) ResourceType() string { return s3BucketTFType }

// Enrich populates ir.Attrs with a typed AWSS3Bucket payload for the
// bucket identified by ir.Identity. Returns ErrEnrichClientUnavailable
// if EnrichClients.S3 is nil; ErrNotFound if HeadBucket reports the
// bucket as missing; any other error reflects a real S3 API failure
// on the load-bearing HeadBucket call. Per-block overlay failures
// (encryption / versioning / lifecycle / logging / cors / policy /
// website / tags / object_lock) are downgraded to omitted blocks —
// the resource is still emitted with whatever succeeded.
func (e s3BucketEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.S3 == nil {
		return ErrEnrichClientUnavailable
	}
	bucket := s3BucketNameForEnrich(&ir.Identity)
	if bucket == "" {
		return fmt.Errorf("s3_bucket: cannot derive bucket name from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}

	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return err
	}

	// Stamp ARN + region onto Identity.NativeIDs so downstream
	// consumers don't have to round-trip back to the SDK. The pure-
	// mapping helper does not touch ir.Identity per the
	// AttributeEnricher contract; this is the only place the enricher
	// writes to it.
	if typed.ARN != nil && typed.ARN.Literal != nil && *typed.ARN.Literal != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["arn"] = *typed.ARN.Literal
	}
	if typed.Region != nil && typed.Region.Literal != nil && *typed.Region.Literal != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["region"] = *typed.Region.Literal
	}

	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("s3_bucket: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSS3Bucket payload for the bucket
// named by identity and returns it as the json.RawMessage shape that
// would land in ImportedResource.Attrs. Shares the SDK call + mapping
// with Enrich via the private fetchAndMap helper so the two paths
// cannot drift out of sync. Does not mutate identity — the ARN /
// region that Enrich stamps onto NativeIDs is intentionally NOT
// stamped here, since the per-IR drift refresh path expects the
// identity to be authoritative input, not a destination.
func (e s3BucketEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.S3 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New("s3_bucket: identity is nil")
	}
	bucket := s3BucketNameForEnrich(identity)
	if bucket == "" {
		return nil, fmt.Errorf("s3_bucket: cannot derive bucket name from Identity (Address=%q ImportID=%q NameHint=%q)",
			identity.Address, identity.ImportID, identity.NameHint)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("s3_bucket: marshal Attrs: %w", err)
	}
	return raw, nil
}

// fetchAndMap issues the HeadBucket existence probe + all overlays
// and returns the populated typed struct. Shared between Enrich and
// EnrichByID so the SDK-call layout lives in one place. HeadBucket
// failures are propagated; overlay failures are downgraded.
//
// For standard S3 buckets, the ARN is deterministic from the name
// (`arn:aws:s3:::<bucket>`), so we synthesize it whenever
// HeadBucket doesn't return one (HeadBucket's BucketArn field is
// directory-bucket-only).
func (e s3BucketEnricher) fetchAndMap(ctx context.Context, c *s3.Client, bucket string) (*generated.AWSS3Bucket, error) {
	head, err := e.fetchHead(ctx, c, bucket)
	if err != nil {
		// HeadBucket surfaces a not-found bucket as a generic 404 with
		// ErrorCode "NotFound" or "NoSuchBucket" — both via the typed
		// NotFound shape or via the smithy.APIError code. Map either
		// onto ErrNotFound so drift / dispatch flows can distinguish a
		// deleted bucket from a real API failure.
		var notFound *s3types.NotFound
		if errors.As(err, &notFound) || isS3NotSetError(err, "NotFound", "NoSuchBucket") {
			return nil, fmt.Errorf("s3_bucket %q: %w", bucket, ErrNotFound)
		}
		return nil, fmt.Errorf("s3_bucket: head %q: %w", bucket, err)
	}

	typed := mapS3Bucket(bucket, head)

	// Encryption — GetBucketEncryption. Soft-fail.
	if sse, ferr := e.fetchEncryption(ctx, c, bucket); ferr == nil && sse != nil {
		if block := mapS3SSE(sse); block != nil {
			typed.ServerSideEncryptionConfiguration = []generated.AWSS3BucketServerSideEncryptionConfiguration{*block}
		}
	}

	// Versioning — GetBucketVersioning. Soft-fail. Absent when Status
	// and MFADelete are both empty (matches the bucket-versioning
	// sub-resource "never configured" semantic from sdkonly_s3.go).
	if v, ferr := e.fetchVersioning(ctx, c, bucket); ferr == nil && v != nil {
		if block := mapS3Versioning(v); block != nil {
			typed.Versioning = []generated.AWSS3BucketVersioning{*block}
		}
	}

	// Lifecycle — GetBucketLifecycleConfiguration. Soft-fail.
	if rules, ferr := e.fetchLifecycle(ctx, c, bucket); ferr == nil && len(rules) > 0 {
		typed.LifecycleRule = mapS3LifecycleRules(rules)
	}

	// Logging — GetBucketLogging. Soft-fail.
	if lg, ferr := e.fetchLogging(ctx, c, bucket); ferr == nil && lg != nil {
		if block := mapS3Logging(lg); block != nil {
			typed.Logging = []generated.AWSS3BucketLogging{*block}
		}
	}

	// CORS — GetBucketCors. Soft-fail.
	if rules, ferr := e.fetchCors(ctx, c, bucket); ferr == nil && len(rules) > 0 {
		typed.CorsRule = mapS3CorsRules(rules)
	}

	// Policy — GetBucketPolicy. Soft-fail.
	if p, ferr := e.fetchPolicy(ctx, c, bucket); ferr == nil && p != nil && *p != "" {
		typed.Policy = generated.LiteralOf(*p)
	}

	// Website — GetBucketWebsite. Soft-fail.
	if w, ferr := e.fetchWebsite(ctx, c, bucket); ferr == nil && w != nil {
		if block := mapS3Website(w); block != nil {
			typed.Website = []generated.AWSS3BucketWebsite{*block}
		}
	}

	// Tags — GetBucketTagging. Soft-fail.
	if tags, ferr := e.fetchTags(ctx, c, bucket); ferr == nil && len(tags) > 0 {
		m := map[string]*generated.Value[string]{}
		for _, t := range tags {
			if t.Key != nil {
				m[*t.Key] = generated.LiteralOf(aws.ToString(t.Value))
			}
		}
		if len(m) > 0 {
			typed.Tags = m
		}
	}

	// Object Lock — GetObjectLockConfiguration. Soft-fail. Also
	// mirrors `object_lock_enabled` (top-level bool) since the SDK's
	// ObjectLockEnabled enum is the only reliable signal — HeadBucket
	// does not surface it.
	if ol, ferr := e.fetchObjectLock(ctx, c, bucket); ferr == nil && ol != nil {
		if block := mapS3ObjectLock(ol); block != nil {
			typed.ObjectLockConfiguration = []generated.AWSS3BucketObjectLockConfiguration{*block}
		}
		if string(ol.ObjectLockEnabled) == string(s3types.ObjectLockEnabledEnabled) {
			typed.ObjectLockEnabled = generated.LiteralOf(true)
		}
	}

	return typed, nil
}

// s3BucketNameForEnrich pulls the bucket name from the identifiers
// the discoverer populates. Order of preference mirrors
// dynamodb_table_enrich.go:
//
//  1. Identity.NameHint — explicit bucket name set by
//     nameOrIdentifier("BucketName") in cloudControlTypeConfigs.
//  2. Identity.NativeIDs["bucket"] — set by the SDK-only sub-resource
//     discoverers (sdkonly_s3.go) for cross-resource lookups.
//  3. Identity.NativeIDs["name"] — generic fallback.
//  4. Identity.ImportID — last resort.
func s3BucketNameForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if s := strings.TrimSpace(id.NameHint); s != "" {
		return s
	}
	if s := strings.TrimSpace(id.NativeIDs["bucket"]); s != "" {
		return s
	}
	if s := strings.TrimSpace(id.NativeIDs["name"]); s != "" {
		return s
	}
	return strings.TrimSpace(id.ImportID)
}

// mapS3Bucket builds the typed top-level surface from the bucket
// name + HeadBucket response. ARN is synthesized from the bucket
// name when HeadBucket does not return one (standard S3 buckets do
// not carry the BucketArn field — it's directory-bucket-only). The
// ARN follows the canonical `arn:aws:s3:::<bucket>` form per the AWS
// S3 ARN reference.
func mapS3Bucket(bucket string, head *s3.HeadBucketOutput) *generated.AWSS3Bucket {
	out := &generated.AWSS3Bucket{}
	out.Bucket = generated.LiteralOf(bucket)
	// TF state stores the bucket name as the resource id.
	out.ID = generated.LiteralOf(bucket)

	// ARN: directory buckets carry BucketArn; standard buckets do
	// not. Synthesize the standard form when missing — the inspector
	// downstream keys off this string.
	arn := aws.ToString(head.BucketArn)
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:s3:::%s", bucket)
	}
	out.ARN = generated.LiteralOf(arn)

	if region := aws.ToString(head.BucketRegion); region != "" {
		out.Region = generated.LiteralOf(region)
	}

	return out
}

// mapS3SSE projects a ServerSideEncryptionConfiguration into the
// typed block. Returns nil when the configuration has no usable
// rules.
func mapS3SSE(sse *s3types.ServerSideEncryptionConfiguration) *generated.AWSS3BucketServerSideEncryptionConfiguration {
	if sse == nil || len(sse.Rules) == 0 {
		return nil
	}
	rules := make([]generated.AWSS3BucketServerSideEncryptionConfigurationRule, 0, len(sse.Rules))
	for i := range sse.Rules {
		r := sse.Rules[i]
		rule := generated.AWSS3BucketServerSideEncryptionConfigurationRule{}
		if r.BucketKeyEnabled != nil {
			rule.BucketKeyEnabled = generated.LiteralOf(*r.BucketKeyEnabled)
		}
		if r.ApplyServerSideEncryptionByDefault != nil {
			apply := generated.AWSS3BucketServerSideEncryptionConfigurationRuleApplyServerSideEncryptionByDefault{}
			if alg := string(r.ApplyServerSideEncryptionByDefault.SSEAlgorithm); alg != "" {
				apply.SSEAlgorithm = generated.LiteralOf(alg)
			}
			if k := aws.ToString(r.ApplyServerSideEncryptionByDefault.KMSMasterKeyID); k != "" {
				apply.KMSMasterKeyID = generated.LiteralOf(k)
			}
			rule.ApplyServerSideEncryptionByDefault = []generated.AWSS3BucketServerSideEncryptionConfigurationRuleApplyServerSideEncryptionByDefault{apply}
		}
		rules = append(rules, rule)
	}
	return &generated.AWSS3BucketServerSideEncryptionConfiguration{Rule: rules}
}

// mapS3Versioning projects GetBucketVersioning output into the typed
// block. Returns nil when neither Status nor MFADelete is set —
// matching the "never configured" semantic from sdkonly_s3.go's
// fetchS3BucketVersioning.
func mapS3Versioning(v *s3.GetBucketVersioningOutput) *generated.AWSS3BucketVersioning {
	if v == nil || (v.Status == "" && v.MFADelete == "") {
		return nil
	}
	block := generated.AWSS3BucketVersioning{}
	// TF's `enabled` bool maps from Status == "Enabled".
	block.Enabled = generated.LiteralOf(v.Status == s3types.BucketVersioningStatusEnabled)
	// TF's `mfa_delete` bool maps from MFADelete == "Enabled".
	if v.MFADelete != "" {
		block.MFADelete = generated.LiteralOf(v.MFADelete == s3types.MFADeleteStatusEnabled)
	}
	return &block
}

// mapS3LifecycleRules projects a slice of LifecycleRule into the
// typed block list. The legacy `aws_s3_bucket` resource only carries
// a subset of the modern `aws_s3_bucket_lifecycle_configuration`
// surface — fields not on the Layer 1 typed model are intentionally
// skipped (e.g. Filter.And, ObjectSizeGreaterThan, …) per the
// typed-struct-is-truth contract.
func mapS3LifecycleRules(rules []s3types.LifecycleRule) []generated.AWSS3BucketLifecycleRule {
	out := make([]generated.AWSS3BucketLifecycleRule, 0, len(rules))
	for i := range rules {
		r := rules[i]
		block := generated.AWSS3BucketLifecycleRule{}
		if r.ID != nil && *r.ID != "" {
			block.ID = generated.LiteralOf(*r.ID)
		}
		// Status -> Enabled bool.
		block.Enabled = generated.LiteralOf(r.Status == s3types.ExpirationStatusEnabled)
		if r.Prefix != nil && *r.Prefix != "" {
			block.Prefix = generated.LiteralOf(*r.Prefix)
		}
		if r.AbortIncompleteMultipartUpload != nil && r.AbortIncompleteMultipartUpload.DaysAfterInitiation != nil {
			block.AbortIncompleteMultipartUploadDays = generated.LiteralOf(float64(*r.AbortIncompleteMultipartUpload.DaysAfterInitiation))
		}
		if r.Filter != nil && r.Filter.Tag != nil && r.Filter.Tag.Key != nil {
			block.Tags = map[string]*generated.Value[string]{
				*r.Filter.Tag.Key: generated.LiteralOf(aws.ToString(r.Filter.Tag.Value)),
			}
		}
		if r.Expiration != nil {
			exp := generated.AWSS3BucketLifecycleRuleExpiration{}
			if r.Expiration.Date != nil {
				exp.Date = generated.LiteralOf(r.Expiration.Date.UTC().Format("2006-01-02T15:04:05Z"))
			}
			if r.Expiration.Days != nil {
				exp.Days = generated.LiteralOf(float64(*r.Expiration.Days))
			}
			if r.Expiration.ExpiredObjectDeleteMarker != nil {
				exp.ExpiredObjectDeleteMarker = generated.LiteralOf(*r.Expiration.ExpiredObjectDeleteMarker)
			}
			block.Expiration = []generated.AWSS3BucketLifecycleRuleExpiration{exp}
		}
		if r.NoncurrentVersionExpiration != nil && r.NoncurrentVersionExpiration.NoncurrentDays != nil {
			block.NoncurrentVersionExpiration = []generated.AWSS3BucketLifecycleRuleNoncurrentVersionExpiration{{
				Days: generated.LiteralOf(float64(*r.NoncurrentVersionExpiration.NoncurrentDays)),
			}}
		}
		if len(r.NoncurrentVersionTransitions) > 0 {
			ts := make([]generated.AWSS3BucketLifecycleRuleNoncurrentVersionTransition, 0, len(r.NoncurrentVersionTransitions))
			for j := range r.NoncurrentVersionTransitions {
				t := r.NoncurrentVersionTransitions[j]
				e := generated.AWSS3BucketLifecycleRuleNoncurrentVersionTransition{}
				if t.NoncurrentDays != nil {
					e.Days = generated.LiteralOf(float64(*t.NoncurrentDays))
				}
				if sc := string(t.StorageClass); sc != "" {
					e.StorageClass = generated.LiteralOf(sc)
				}
				ts = append(ts, e)
			}
			block.NoncurrentVersionTransition = ts
		}
		if len(r.Transitions) > 0 {
			ts := make([]generated.AWSS3BucketLifecycleRuleTransition, 0, len(r.Transitions))
			for j := range r.Transitions {
				t := r.Transitions[j]
				e := generated.AWSS3BucketLifecycleRuleTransition{}
				if t.Date != nil {
					e.Date = generated.LiteralOf(t.Date.UTC().Format("2006-01-02T15:04:05Z"))
				}
				if t.Days != nil {
					e.Days = generated.LiteralOf(float64(*t.Days))
				}
				if sc := string(t.StorageClass); sc != "" {
					e.StorageClass = generated.LiteralOf(sc)
				}
				ts = append(ts, e)
			}
			block.Transition = ts
		}
		out = append(out, block)
	}
	return out
}

// mapS3Logging projects a LoggingEnabled into the typed block.
// Returns nil when TargetBucket is empty.
func mapS3Logging(lg *s3types.LoggingEnabled) *generated.AWSS3BucketLogging {
	if lg == nil || lg.TargetBucket == nil || *lg.TargetBucket == "" {
		return nil
	}
	block := generated.AWSS3BucketLogging{
		TargetBucket: generated.LiteralOf(*lg.TargetBucket),
	}
	if lg.TargetPrefix != nil && *lg.TargetPrefix != "" {
		block.TargetPrefix = generated.LiteralOf(*lg.TargetPrefix)
	}
	return &block
}

// mapS3CorsRules projects a slice of CORSRule into typed blocks.
func mapS3CorsRules(rules []s3types.CORSRule) []generated.AWSS3BucketCorsRule {
	out := make([]generated.AWSS3BucketCorsRule, 0, len(rules))
	for i := range rules {
		r := rules[i]
		block := generated.AWSS3BucketCorsRule{}
		block.AllowedMethods = literalStringSlice(r.AllowedMethods)
		block.AllowedOrigins = literalStringSlice(r.AllowedOrigins)
		if len(r.AllowedHeaders) > 0 {
			block.AllowedHeaders = literalStringSlice(r.AllowedHeaders)
		}
		if len(r.ExposeHeaders) > 0 {
			block.ExposeHeaders = literalStringSlice(r.ExposeHeaders)
		}
		if r.MaxAgeSeconds != nil {
			block.MaxAgeSeconds = generated.LiteralOf(int64(*r.MaxAgeSeconds))
		}
		out = append(out, block)
	}
	return out
}

// mapS3Website projects a GetBucketWebsite response into the typed
// block. Returns nil when neither index nor error document nor
// redirect is set — matches the "never configured" semantic.
func mapS3Website(w *s3.GetBucketWebsiteOutput) *generated.AWSS3BucketWebsite {
	if w == nil {
		return nil
	}
	populated := false
	block := generated.AWSS3BucketWebsite{}
	if w.IndexDocument != nil && w.IndexDocument.Suffix != nil && *w.IndexDocument.Suffix != "" {
		block.IndexDocument = generated.LiteralOf(*w.IndexDocument.Suffix)
		populated = true
	}
	if w.ErrorDocument != nil && w.ErrorDocument.Key != nil && *w.ErrorDocument.Key != "" {
		block.ErrorDocument = generated.LiteralOf(*w.ErrorDocument.Key)
		populated = true
	}
	if w.RedirectAllRequestsTo != nil && w.RedirectAllRequestsTo.HostName != nil && *w.RedirectAllRequestsTo.HostName != "" {
		// TF's redirect_all_requests_to is a string of "<host>" or
		// "<protocol>://<host>" depending on whether protocol is set.
		host := *w.RedirectAllRequestsTo.HostName
		if proto := string(w.RedirectAllRequestsTo.Protocol); proto != "" {
			host = proto + "://" + host
		}
		block.RedirectAllRequestsTo = generated.LiteralOf(host)
		populated = true
	}
	if !populated {
		return nil
	}
	return &block
}

// mapS3ObjectLock projects an ObjectLockConfiguration into the typed
// block. Returns nil when neither ObjectLockEnabled nor a Rule is
// set.
func mapS3ObjectLock(ol *s3types.ObjectLockConfiguration) *generated.AWSS3BucketObjectLockConfiguration {
	if ol == nil {
		return nil
	}
	block := generated.AWSS3BucketObjectLockConfiguration{}
	populated := false
	if s := string(ol.ObjectLockEnabled); s != "" {
		block.ObjectLockEnabled = generated.LiteralOf(s)
		populated = true
	}
	if ol.Rule != nil && ol.Rule.DefaultRetention != nil {
		dr := ol.Rule.DefaultRetention
		ret := generated.AWSS3BucketObjectLockConfigurationRuleDefaultRetention{}
		if m := string(dr.Mode); m != "" {
			ret.Mode = generated.LiteralOf(m)
		}
		if dr.Days != nil {
			ret.Days = generated.LiteralOf(float64(*dr.Days))
		}
		if dr.Years != nil {
			ret.Years = generated.LiteralOf(float64(*dr.Years))
		}
		block.Rule = []generated.AWSS3BucketObjectLockConfigurationRule{{
			DefaultRetention: []generated.AWSS3BucketObjectLockConfigurationRuleDefaultRetention{ret},
		}}
		populated = true
	}
	if !populated {
		return nil
	}
	return &block
}

// literalStringSlice projects a slice of plain strings into the
// []*Value[string] shape the Layer 1 typed model uses for list
// attributes.
func literalStringSlice(in []string) []*generated.Value[string] {
	if len(in) == 0 {
		return nil
	}
	out := make([]*generated.Value[string], 0, len(in))
	for _, s := range in {
		out = append(out, generated.LiteralOf(s))
	}
	return out
}

// defaultS3BucketFetchHead is the production HeadBucket call.
func defaultS3BucketFetchHead(ctx context.Context, c *s3.Client, bucket string) (*s3.HeadBucketOutput, error) {
	out, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("head bucket: nil output")
	}
	return out, nil
}

// defaultS3BucketFetchEncryption is the production encryption fetch.
// Returns (nil, nil) when the bucket has no encryption configuration.
func defaultS3BucketFetchEncryption(ctx context.Context, c *s3.Client, bucket string) (*s3types.ServerSideEncryptionConfiguration, error) {
	out, err := c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(bucket)})
	if err != nil {
		if isS3NotSetError(err, "ServerSideEncryptionConfigurationNotFoundError", "NoSuchEncryptionConfiguration") {
			return nil, nil
		}
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.ServerSideEncryptionConfiguration, nil
}

// defaultS3BucketFetchVersioning is the production versioning fetch.
// GetBucketVersioning has no NotFound code: AWS treats "versioning
// never set" as a successful response with empty Status / MFADelete.
func defaultS3BucketFetchVersioning(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketVersioningOutput, error) {
	return c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucket)})
}

// defaultS3BucketFetchLifecycle is the production lifecycle fetch.
// Returns (nil, nil) when the bucket has no lifecycle configuration.
func defaultS3BucketFetchLifecycle(ctx context.Context, c *s3.Client, bucket string) ([]s3types.LifecycleRule, error) {
	out, err := c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(bucket)})
	if err != nil {
		if isS3NotSetError(err, "NoSuchLifecycleConfiguration") {
			return nil, nil
		}
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.Rules, nil
}

// defaultS3BucketFetchLogging is the production logging fetch.
// GetBucketLogging returns success with LoggingEnabled=nil when
// logging is not configured.
func defaultS3BucketFetchLogging(ctx context.Context, c *s3.Client, bucket string) (*s3types.LoggingEnabled, error) {
	out, err := c.GetBucketLogging(ctx, &s3.GetBucketLoggingInput{Bucket: aws.String(bucket)})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.LoggingEnabled, nil
}

// defaultS3BucketFetchCors is the production CORS fetch.
// Returns (nil, nil) when the bucket has no CORS configuration.
func defaultS3BucketFetchCors(ctx context.Context, c *s3.Client, bucket string) ([]s3types.CORSRule, error) {
	out, err := c.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: aws.String(bucket)})
	if err != nil {
		if isS3NotSetError(err, "NoSuchCORSConfiguration") {
			return nil, nil
		}
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.CORSRules, nil
}

// defaultS3BucketFetchPolicy is the production policy fetch.
// Returns (nil, nil) when the bucket has no policy.
func defaultS3BucketFetchPolicy(ctx context.Context, c *s3.Client, bucket string) (*string, error) {
	out, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
	if err != nil {
		if isS3NotSetError(err, "NoSuchBucketPolicy") {
			return nil, nil
		}
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.Policy, nil
}

// defaultS3BucketFetchWebsite is the production website fetch.
// Returns (nil, nil) when the bucket has no website configuration.
func defaultS3BucketFetchWebsite(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketWebsiteOutput, error) {
	out, err := c.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{Bucket: aws.String(bucket)})
	if err != nil {
		if isS3NotSetError(err, "NoSuchWebsiteConfiguration") {
			return nil, nil
		}
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out, nil
}

// defaultS3BucketFetchTags is the production tags fetch.
// Returns (nil, nil) when the bucket has no tag set.
func defaultS3BucketFetchTags(ctx context.Context, c *s3.Client, bucket string) ([]s3types.Tag, error) {
	out, err := c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(bucket)})
	if err != nil {
		if isS3NotSetError(err, "NoSuchTagSet") {
			return nil, nil
		}
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.TagSet, nil
}

// defaultS3BucketFetchObjectLock is the production object-lock fetch.
// Returns (nil, nil) when the bucket has no object-lock configuration.
// Note the SDK operation is named GetObjectLockConfiguration (not
// GetBucketObjectLockConfiguration); the naming asymmetry is a
// historical S3 API quirk.
func defaultS3BucketFetchObjectLock(ctx context.Context, c *s3.Client, bucket string) (*s3types.ObjectLockConfiguration, error) {
	out, err := c.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: aws.String(bucket)})
	if err != nil {
		if isS3NotSetError(err, "ObjectLockConfigurationNotFoundError") {
			return nil, nil
		}
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.ObjectLockConfiguration, nil
}
