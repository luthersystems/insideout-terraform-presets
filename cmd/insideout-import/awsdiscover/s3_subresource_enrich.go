// Package awsdiscover — S3 bucket sub-resource attribute enrichers (#482).
//
// Five hand-rolled enrichers, one per S3 bucket sub-resource type:
//
//   - aws_s3_bucket_versioning
//   - aws_s3_bucket_lifecycle_configuration
//   - aws_s3_bucket_ownership_controls
//   - aws_s3_bucket_public_access_block
//   - aws_s3_bucket_server_side_encryption_configuration
//
// All five share the same skeleton: derive the bucket name from
// ir.Identity (matching s3BucketNameForEnrich's order of preference),
// issue a single GetBucket* call against c.S3, map the response into
// the matching Layer-1 typed struct, JSON-marshal into ir.Attrs.
//
// **Soft-fail discipline**: the service-native "this sub-resource is
// not configured on the bucket" codes (NoSuchLifecycleConfiguration,
// OwnershipControlsNotFoundError, NoSuchPublicAccessBlockConfiguration,
// ServerSideEncryptionConfigurationNotFoundError) and a NoSuchBucket
// from a vanished parent map to ErrNotFound. The bucket name itself is
// the Terraform import ID for all 5 — Identity.ImportID populates
// directly from the parent's bucket name. A nil S3 client surfaces as
// ErrEnrichClientUnavailable, downgraded by EnrichAttributes to a
// per-resource ServiceWarn.
//
// **Test injection**: each enricher carries a `fetch` function-field
// so unit tests can drive the SDK boundary with a fake without
// constructing a real *s3.Client. The production wiring routes to a
// thin `default*Fetch` helper that calls the SDK directly.
//
// Mirrors the resourceexplorer2_index_enrich.go pattern: single-Get
// SDK call, pure-mapping helper shared by Enrich + EnrichByID, identity-
// only stamping on ir.Identity.NativeIDs.
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

// =====================================================================
// aws_s3_bucket_versioning
// =====================================================================

const s3BucketVersioningTFType = "aws_s3_bucket_versioning"

type s3BucketVersioningEnricher struct {
	fetch func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketVersioningOutput, error)
}

func newS3BucketVersioningEnricher() *s3BucketVersioningEnricher {
	return &s3BucketVersioningEnricher{fetch: defaultS3BucketVersioningSubresourceFetch}
}

func (s3BucketVersioningEnricher) ResourceType() string { return s3BucketVersioningTFType }

func (e s3BucketVersioningEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.S3 == nil {
		return ErrEnrichClientUnavailable
	}
	bucket := s3BucketNameForEnrich(&ir.Identity)
	if bucket == "" {
		return fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketVersioningTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", s3BucketVersioningTFType, err)
	}
	ir.Attrs = raw
	return nil
}

func (e s3BucketVersioningEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.S3 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(s3BucketVersioningTFType + ": identity is nil")
	}
	bucket := s3BucketNameForEnrich(identity)
	if bucket == "" {
		return nil, fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketVersioningTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", s3BucketVersioningTFType, err)
	}
	return raw, nil
}

func (e s3BucketVersioningEnricher) fetchAndMap(ctx context.Context, c *s3.Client, bucket string) (*generated.AWSS3BucketVersioning, error) {
	out, err := e.fetch(ctx, c, bucket)
	if err != nil {
		if isS3NotSetError(err, "NoSuchBucket") {
			return nil, fmt.Errorf("%s (bucket=%s): %w", s3BucketVersioningTFType, bucket, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get versioning (bucket=%s): %w", s3BucketVersioningTFType, bucket, err)
	}
	return mapS3BucketVersioning(bucket, out), nil
}

func defaultS3BucketVersioningSubresourceFetch(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketVersioningOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucket)})
}

// mapS3BucketVersioning projects the GetBucketVersioning response into
// the typed AWSS3BucketVersioning struct. A bucket whose versioning has
// never been configured (Status="" and MFADelete="") still emits a
// valid record with bucket+id populated; downstream consumers gate
// "exists" on the versioning_configuration block, not on the wrapper.
func mapS3BucketVersioning(bucket string, out *s3.GetBucketVersioningOutput) *generated.AWSS3BucketVersioning {
	typed := &generated.AWSS3BucketVersioning{}
	typed.Bucket = generated.LiteralOf(bucket)
	typed.ID = generated.LiteralOf(bucket)
	if out != nil && (out.Status != "" || out.MFADelete != "") {
		cfg := generated.AWSS3BucketVersioningVersioningConfiguration{}
		if out.Status != "" {
			cfg.Status = generated.LiteralOf(string(out.Status))
		}
		if out.MFADelete != "" {
			cfg.MFADelete = generated.LiteralOf(string(out.MFADelete))
		}
		typed.VersioningConfiguration = []generated.AWSS3BucketVersioningVersioningConfiguration{cfg}
	}
	return typed
}

// =====================================================================
// aws_s3_bucket_lifecycle_configuration
// =====================================================================

const s3BucketLifecycleConfigurationTFType = "aws_s3_bucket_lifecycle_configuration"

type s3BucketLifecycleConfigurationEnricher struct {
	fetch func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketLifecycleConfigurationOutput, error)
}

func newS3BucketLifecycleConfigurationEnricher() *s3BucketLifecycleConfigurationEnricher {
	return &s3BucketLifecycleConfigurationEnricher{fetch: defaultS3BucketLifecycleConfigurationSubresourceFetch}
}

func (s3BucketLifecycleConfigurationEnricher) ResourceType() string {
	return s3BucketLifecycleConfigurationTFType
}

func (e s3BucketLifecycleConfigurationEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.S3 == nil {
		return ErrEnrichClientUnavailable
	}
	bucket := s3BucketNameForEnrich(&ir.Identity)
	if bucket == "" {
		return fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketLifecycleConfigurationTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", s3BucketLifecycleConfigurationTFType, err)
	}
	ir.Attrs = raw
	return nil
}

func (e s3BucketLifecycleConfigurationEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.S3 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(s3BucketLifecycleConfigurationTFType + ": identity is nil")
	}
	bucket := s3BucketNameForEnrich(identity)
	if bucket == "" {
		return nil, fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketLifecycleConfigurationTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", s3BucketLifecycleConfigurationTFType, err)
	}
	return raw, nil
}

func (e s3BucketLifecycleConfigurationEnricher) fetchAndMap(ctx context.Context, c *s3.Client, bucket string) (*generated.AWSS3BucketLifecycleConfiguration, error) {
	out, err := e.fetch(ctx, c, bucket)
	if err != nil {
		if isS3NotSetError(err, "NoSuchLifecycleConfiguration", "NoSuchBucket") {
			return nil, fmt.Errorf("%s (bucket=%s): %w", s3BucketLifecycleConfigurationTFType, bucket, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get lifecycle (bucket=%s): %w", s3BucketLifecycleConfigurationTFType, bucket, err)
	}
	return mapS3BucketLifecycleConfiguration(bucket, out), nil
}

func defaultS3BucketLifecycleConfigurationSubresourceFetch(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(bucket)})
}

// mapS3BucketLifecycleConfiguration projects the response into the
// typed struct. Rules-level fan-out keeps the mapping deterministic:
// each S3 LifecycleRule lands as one AWSS3BucketLifecycleConfigurationRule
// with id / status / prefix and the sub-blocks that came back populated.
//
// The TF resource carries a much richer filter shape than the SDK's
// LifecycleRule.Filter (which is itself a discriminated union); the
// minimal projection here surfaces the scalar identity (id / status /
// prefix) so the diff comparator can detect rule add/remove without
// over-claiming structural drift on the complex filter sub-tree. A
// downstream follow-up can extend the mapping if the inspector needs
// per-rule filter visibility.
func mapS3BucketLifecycleConfiguration(bucket string, out *s3.GetBucketLifecycleConfigurationOutput) *generated.AWSS3BucketLifecycleConfiguration {
	typed := &generated.AWSS3BucketLifecycleConfiguration{}
	typed.Bucket = generated.LiteralOf(bucket)
	typed.ID = generated.LiteralOf(bucket)
	if out == nil || len(out.Rules) == 0 {
		return typed
	}
	rules := make([]generated.AWSS3BucketLifecycleConfigurationRule, 0, len(out.Rules))
	for i := range out.Rules {
		r := out.Rules[i]
		rule := generated.AWSS3BucketLifecycleConfigurationRule{}
		if id := aws.ToString(r.ID); id != "" {
			rule.ID = generated.LiteralOf(id)
		}
		if r.Status != "" {
			rule.Status = generated.LiteralOf(string(r.Status))
		}
		// Legacy Prefix field — deprecated by AWS but still surfaces on
		// old buckets. The new filter block is too rich to project
		// faithfully; downstream consumers compare on rule.id + status.
		if p := aws.ToString(r.Prefix); p != "" {
			rule.Prefix = generated.LiteralOf(p)
		}
		if r.AbortIncompleteMultipartUpload != nil {
			a := generated.AWSS3BucketLifecycleConfigurationRuleAbortIncompleteMultipartUpload{}
			if r.AbortIncompleteMultipartUpload.DaysAfterInitiation != nil {
				a.DaysAfterInitiation = generated.LiteralOf(float64(*r.AbortIncompleteMultipartUpload.DaysAfterInitiation))
			}
			rule.AbortIncompleteMultipartUpload = []generated.AWSS3BucketLifecycleConfigurationRuleAbortIncompleteMultipartUpload{a}
		}
		if r.Expiration != nil {
			e := generated.AWSS3BucketLifecycleConfigurationRuleExpiration{}
			if r.Expiration.Days != nil && *r.Expiration.Days != 0 {
				e.Days = generated.LiteralOf(float64(*r.Expiration.Days))
			}
			if r.Expiration.Date != nil {
				e.Date = generated.LiteralOf(r.Expiration.Date.Format("2006-01-02T15:04:05Z"))
			}
			if r.Expiration.ExpiredObjectDeleteMarker != nil {
				e.ExpiredObjectDeleteMarker = generated.LiteralOf(*r.Expiration.ExpiredObjectDeleteMarker)
			}
			rule.Expiration = []generated.AWSS3BucketLifecycleConfigurationRuleExpiration{e}
		}
		if len(r.Transitions) > 0 {
			ts := make([]generated.AWSS3BucketLifecycleConfigurationRuleTransition, 0, len(r.Transitions))
			for j := range r.Transitions {
				t := r.Transitions[j]
				bt := generated.AWSS3BucketLifecycleConfigurationRuleTransition{}
				if t.Days != nil && *t.Days != 0 {
					bt.Days = generated.LiteralOf(float64(*t.Days))
				}
				if t.Date != nil {
					bt.Date = generated.LiteralOf(t.Date.Format("2006-01-02T15:04:05Z"))
				}
				if t.StorageClass != "" {
					bt.StorageClass = generated.LiteralOf(string(t.StorageClass))
				}
				ts = append(ts, bt)
			}
			rule.Transition = ts
		}
		if r.NoncurrentVersionExpiration != nil {
			n := generated.AWSS3BucketLifecycleConfigurationRuleNoncurrentVersionExpiration{}
			if r.NoncurrentVersionExpiration.NoncurrentDays != nil {
				n.NoncurrentDays = generated.LiteralOf(float64(*r.NoncurrentVersionExpiration.NoncurrentDays))
			}
			rule.NoncurrentVersionExpiration = []generated.AWSS3BucketLifecycleConfigurationRuleNoncurrentVersionExpiration{n}
		}
		if len(r.NoncurrentVersionTransitions) > 0 {
			nts := make([]generated.AWSS3BucketLifecycleConfigurationRuleNoncurrentVersionTransition, 0, len(r.NoncurrentVersionTransitions))
			for j := range r.NoncurrentVersionTransitions {
				nt := r.NoncurrentVersionTransitions[j]
				bnt := generated.AWSS3BucketLifecycleConfigurationRuleNoncurrentVersionTransition{}
				if nt.NoncurrentDays != nil {
					bnt.NoncurrentDays = generated.LiteralOf(float64(*nt.NoncurrentDays))
				}
				if nt.StorageClass != "" {
					bnt.StorageClass = generated.LiteralOf(string(nt.StorageClass))
				}
				nts = append(nts, bnt)
			}
			rule.NoncurrentVersionTransition = nts
		}
		rules = append(rules, rule)
	}
	typed.Rule = rules
	return typed
}

// =====================================================================
// aws_s3_bucket_ownership_controls
// =====================================================================

const s3BucketOwnershipControlsTFType = "aws_s3_bucket_ownership_controls"

type s3BucketOwnershipControlsEnricher struct {
	fetch func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketOwnershipControlsOutput, error)
}

func newS3BucketOwnershipControlsEnricher() *s3BucketOwnershipControlsEnricher {
	return &s3BucketOwnershipControlsEnricher{fetch: defaultS3BucketOwnershipControlsSubresourceFetch}
}

func (s3BucketOwnershipControlsEnricher) ResourceType() string {
	return s3BucketOwnershipControlsTFType
}

func (e s3BucketOwnershipControlsEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.S3 == nil {
		return ErrEnrichClientUnavailable
	}
	bucket := s3BucketNameForEnrich(&ir.Identity)
	if bucket == "" {
		return fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketOwnershipControlsTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", s3BucketOwnershipControlsTFType, err)
	}
	ir.Attrs = raw
	return nil
}

func (e s3BucketOwnershipControlsEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.S3 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(s3BucketOwnershipControlsTFType + ": identity is nil")
	}
	bucket := s3BucketNameForEnrich(identity)
	if bucket == "" {
		return nil, fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketOwnershipControlsTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", s3BucketOwnershipControlsTFType, err)
	}
	return raw, nil
}

func (e s3BucketOwnershipControlsEnricher) fetchAndMap(ctx context.Context, c *s3.Client, bucket string) (*generated.AWSS3BucketOwnershipControls, error) {
	out, err := e.fetch(ctx, c, bucket)
	if err != nil {
		if isS3NotSetError(err, "OwnershipControlsNotFoundError", "NoSuchOwnershipControls", "NoSuchBucket") {
			return nil, fmt.Errorf("%s (bucket=%s): %w", s3BucketOwnershipControlsTFType, bucket, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get ownership controls (bucket=%s): %w", s3BucketOwnershipControlsTFType, bucket, err)
	}
	return mapS3BucketOwnershipControls(bucket, out), nil
}

func defaultS3BucketOwnershipControlsSubresourceFetch(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketOwnershipControlsOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetBucketOwnershipControls(ctx, &s3.GetBucketOwnershipControlsInput{Bucket: aws.String(bucket)})
}

func mapS3BucketOwnershipControls(bucket string, out *s3.GetBucketOwnershipControlsOutput) *generated.AWSS3BucketOwnershipControls {
	typed := &generated.AWSS3BucketOwnershipControls{}
	typed.Bucket = generated.LiteralOf(bucket)
	typed.ID = generated.LiteralOf(bucket)
	if out == nil || out.OwnershipControls == nil || len(out.OwnershipControls.Rules) == 0 {
		return typed
	}
	rules := make([]generated.AWSS3BucketOwnershipControlsRule, 0, len(out.OwnershipControls.Rules))
	for i := range out.OwnershipControls.Rules {
		r := out.OwnershipControls.Rules[i]
		rule := generated.AWSS3BucketOwnershipControlsRule{}
		if r.ObjectOwnership != "" {
			rule.ObjectOwnership = generated.LiteralOf(string(r.ObjectOwnership))
		}
		rules = append(rules, rule)
	}
	typed.Rule = rules
	return typed
}

// =====================================================================
// aws_s3_bucket_public_access_block
// =====================================================================

const s3BucketPublicAccessBlockTFType = "aws_s3_bucket_public_access_block"

type s3BucketPublicAccessBlockEnricher struct {
	fetch func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetPublicAccessBlockOutput, error)
}

func newS3BucketPublicAccessBlockEnricher() *s3BucketPublicAccessBlockEnricher {
	return &s3BucketPublicAccessBlockEnricher{fetch: defaultS3BucketPublicAccessBlockSubresourceFetch}
}

func (s3BucketPublicAccessBlockEnricher) ResourceType() string {
	return s3BucketPublicAccessBlockTFType
}

func (e s3BucketPublicAccessBlockEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.S3 == nil {
		return ErrEnrichClientUnavailable
	}
	bucket := s3BucketNameForEnrich(&ir.Identity)
	if bucket == "" {
		return fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketPublicAccessBlockTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", s3BucketPublicAccessBlockTFType, err)
	}
	ir.Attrs = raw
	return nil
}

func (e s3BucketPublicAccessBlockEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.S3 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(s3BucketPublicAccessBlockTFType + ": identity is nil")
	}
	bucket := s3BucketNameForEnrich(identity)
	if bucket == "" {
		return nil, fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketPublicAccessBlockTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", s3BucketPublicAccessBlockTFType, err)
	}
	return raw, nil
}

func (e s3BucketPublicAccessBlockEnricher) fetchAndMap(ctx context.Context, c *s3.Client, bucket string) (*generated.AWSS3BucketPublicAccessBlock, error) {
	out, err := e.fetch(ctx, c, bucket)
	if err != nil {
		if isS3NotSetError(err, "NoSuchPublicAccessBlockConfiguration", "NoSuchBucket") {
			return nil, fmt.Errorf("%s (bucket=%s): %w", s3BucketPublicAccessBlockTFType, bucket, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get public access block (bucket=%s): %w", s3BucketPublicAccessBlockTFType, bucket, err)
	}
	return mapS3BucketPublicAccessBlock(bucket, out), nil
}

func defaultS3BucketPublicAccessBlockSubresourceFetch(ctx context.Context, c *s3.Client, bucket string) (*s3.GetPublicAccessBlockOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: aws.String(bucket)})
}

func mapS3BucketPublicAccessBlock(bucket string, out *s3.GetPublicAccessBlockOutput) *generated.AWSS3BucketPublicAccessBlock {
	typed := &generated.AWSS3BucketPublicAccessBlock{}
	typed.Bucket = generated.LiteralOf(bucket)
	typed.ID = generated.LiteralOf(bucket)
	if out == nil || out.PublicAccessBlockConfiguration == nil {
		return typed
	}
	cfg := out.PublicAccessBlockConfiguration
	if cfg.BlockPublicAcls != nil {
		typed.BlockPublicAcls = generated.LiteralOf(*cfg.BlockPublicAcls)
	}
	if cfg.BlockPublicPolicy != nil {
		typed.BlockPublicPolicy = generated.LiteralOf(*cfg.BlockPublicPolicy)
	}
	if cfg.IgnorePublicAcls != nil {
		typed.IgnorePublicAcls = generated.LiteralOf(*cfg.IgnorePublicAcls)
	}
	if cfg.RestrictPublicBuckets != nil {
		typed.RestrictPublicBuckets = generated.LiteralOf(*cfg.RestrictPublicBuckets)
	}
	return typed
}

// =====================================================================
// aws_s3_bucket_server_side_encryption_configuration
// =====================================================================

const s3BucketServerSideEncryptionConfigurationTFType = "aws_s3_bucket_server_side_encryption_configuration"

type s3BucketServerSideEncryptionConfigurationEnricher struct {
	fetch func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketEncryptionOutput, error)
}

func newS3BucketServerSideEncryptionConfigurationEnricher() *s3BucketServerSideEncryptionConfigurationEnricher {
	return &s3BucketServerSideEncryptionConfigurationEnricher{fetch: defaultS3BucketSSESubresourceFetch}
}

func (s3BucketServerSideEncryptionConfigurationEnricher) ResourceType() string {
	return s3BucketServerSideEncryptionConfigurationTFType
}

func (e s3BucketServerSideEncryptionConfigurationEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.S3 == nil {
		return ErrEnrichClientUnavailable
	}
	bucket := s3BucketNameForEnrich(&ir.Identity)
	if bucket == "" {
		return fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketServerSideEncryptionConfigurationTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", s3BucketServerSideEncryptionConfigurationTFType, err)
	}
	ir.Attrs = raw
	return nil
}

func (e s3BucketServerSideEncryptionConfigurationEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.S3 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(s3BucketServerSideEncryptionConfigurationTFType + ": identity is nil")
	}
	bucket := s3BucketNameForEnrich(identity)
	if bucket == "" {
		return nil, fmt.Errorf("%s: cannot derive bucket name from Identity", s3BucketServerSideEncryptionConfigurationTFType)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", s3BucketServerSideEncryptionConfigurationTFType, err)
	}
	return raw, nil
}

func (e s3BucketServerSideEncryptionConfigurationEnricher) fetchAndMap(ctx context.Context, c *s3.Client, bucket string) (*generated.AWSS3BucketServerSideEncryptionConfiguration, error) {
	out, err := e.fetch(ctx, c, bucket)
	if err != nil {
		if isS3NotSetError(err, "ServerSideEncryptionConfigurationNotFoundError", "NoSuchEncryptionConfiguration", "NoSuchBucket") {
			return nil, fmt.Errorf("%s (bucket=%s): %w", s3BucketServerSideEncryptionConfigurationTFType, bucket, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get encryption (bucket=%s): %w", s3BucketServerSideEncryptionConfigurationTFType, bucket, err)
	}
	return mapS3BucketSSEConfiguration(bucket, out), nil
}

func defaultS3BucketSSESubresourceFetch(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketEncryptionOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(bucket)})
}

func mapS3BucketSSEConfiguration(bucket string, out *s3.GetBucketEncryptionOutput) *generated.AWSS3BucketServerSideEncryptionConfiguration {
	typed := &generated.AWSS3BucketServerSideEncryptionConfiguration{}
	typed.Bucket = generated.LiteralOf(bucket)
	typed.ID = generated.LiteralOf(bucket)
	if out == nil || out.ServerSideEncryptionConfiguration == nil || len(out.ServerSideEncryptionConfiguration.Rules) == 0 {
		return typed
	}
	rules := make([]generated.AWSS3BucketServerSideEncryptionConfigurationRule, 0, len(out.ServerSideEncryptionConfiguration.Rules))
	for i := range out.ServerSideEncryptionConfiguration.Rules {
		r := out.ServerSideEncryptionConfiguration.Rules[i]
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
	typed.Rule = rules
	return typed
}

// Compile-time assertions: each enricher implements both
// AttributeEnricher and ByIDEnricher.
var (
	_ AttributeEnricher = (*s3BucketVersioningEnricher)(nil)
	_ ByIDEnricher      = (*s3BucketVersioningEnricher)(nil)
	_ AttributeEnricher = (*s3BucketLifecycleConfigurationEnricher)(nil)
	_ ByIDEnricher      = (*s3BucketLifecycleConfigurationEnricher)(nil)
	_ AttributeEnricher = (*s3BucketOwnershipControlsEnricher)(nil)
	_ ByIDEnricher      = (*s3BucketOwnershipControlsEnricher)(nil)
	_ AttributeEnricher = (*s3BucketPublicAccessBlockEnricher)(nil)
	_ ByIDEnricher      = (*s3BucketPublicAccessBlockEnricher)(nil)
	_ AttributeEnricher = (*s3BucketServerSideEncryptionConfigurationEnricher)(nil)
	_ ByIDEnricher      = (*s3BucketServerSideEncryptionConfigurationEnricher)(nil)
)

// Keep s3types referenced from package even when nothing here uses it
// directly; future SDK-shape changes will routes through this anchor.
var _ s3types.BucketVersioningStatus
var _ = strings.TrimSpace
