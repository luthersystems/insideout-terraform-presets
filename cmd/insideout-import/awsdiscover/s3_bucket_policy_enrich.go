// Package awsdiscover — S3 bucket-policy attribute enricher (#661
// follow-up).
//
// Pairs with the Cloud-Control-routed `aws_s3_bucket_policy` discoverer
// registered in cloudControlTypeConfigs ("AWS::S3::BucketPolicy").
//
// **Why a hand-rolled override**: `aws_s3_bucket_policy.policy` is a
// REQUIRED JSON-encoded string with the same Cloud-Control read-back
// gap as aws_iam_policy (#661) — the CFN AWS::S3::BucketPolicy
// `PolicyDocument` is create-time input, so GetResource leaves the
// required `policy` argument empty. One s3:GetBucketPolicy call returns
// the document.
//
// Unlike the IAM-family enrichers, s3:GetBucketPolicy returns the
// document already URL-decoded, so the mapping uses compactPolicyJSON
// directly rather than decodeIAMPolicyDocument.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// s3BucketPolicyTFType is the registered Terraform type for the S3
// bucket-policy enricher.
const s3BucketPolicyTFType = "aws_s3_bucket_policy"

// s3BucketPolicyEnricher implements both AttributeEnricher and
// ByIDEnricher for aws_s3_bucket_policy.
type s3BucketPolicyEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// GetBucketPolicy call against the s3.Client in EnrichClients.
	fetch func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketPolicyOutput, error)
}

// newS3BucketPolicyEnricher returns the production-wired enricher.
func newS3BucketPolicyEnricher() *s3BucketPolicyEnricher {
	return &s3BucketPolicyEnricher{fetch: defaultS3BucketPolicyFetch}
}

func (s3BucketPolicyEnricher) ResourceType() string { return s3BucketPolicyTFType }

// Enrich populates ir.Attrs with a typed AWSS3BucketPolicy payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.S3 is nil,
// ErrNotFound if the bucket has no policy (or no longer exists), and
// any other error reflects a real S3 API failure.
func (e s3BucketPolicyEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.S3 == nil {
		return ErrEnrichClientUnavailable
	}
	bucket := s3BucketNameForEnrich(&ir.Identity)
	if bucket == "" {
		return fmt.Errorf("%s: cannot derive bucket name from Identity (Address=%q ImportID=%q)",
			s3BucketPolicyTFType, ir.Identity.Address, ir.Identity.ImportID)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", s3BucketPolicyTFType, err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSS3BucketPolicy payload for the bucket
// named by identity. Shares the SDK call + mapping with Enrich.
func (e s3BucketPolicyEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.S3 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(s3BucketPolicyTFType + ": identity is nil")
	}
	bucket := s3BucketNameForEnrich(identity)
	if bucket == "" {
		return nil, fmt.Errorf("%s: cannot derive bucket name from Identity (Address=%q ImportID=%q)",
			s3BucketPolicyTFType, identity.Address, identity.ImportID)
	}
	typed, err := e.fetchAndMap(ctx, c.S3, bucket)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", s3BucketPolicyTFType, err)
	}
	return raw, nil
}

// fetchAndMap issues GetBucketPolicy and projects the result.
// NoSuchBucketPolicy (the bucket exists but carries no policy) and
// NoSuchBucket (the bucket is gone) both map to ErrNotFound, which
// EnrichAttributes downgrades to a per-resource warning rather than a
// batch-fatal error.
func (e s3BucketPolicyEnricher) fetchAndMap(ctx context.Context, c *s3.Client, bucket string) (*generated.AWSS3BucketPolicy, error) {
	out, err := e.fetch(ctx, c, bucket)
	if err != nil {
		if isS3NotSetError(err, "NoSuchBucketPolicy", "NoSuchBucket") {
			return nil, fmt.Errorf("%s (bucket=%s): %w", s3BucketPolicyTFType, bucket, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get bucket policy (bucket=%s): %w", s3BucketPolicyTFType, bucket, err)
	}
	typed, err := mapS3BucketPolicy(bucket, out)
	if err != nil {
		return nil, fmt.Errorf("%s (bucket=%s): %w", s3BucketPolicyTFType, bucket, err)
	}
	return typed, nil
}

// defaultS3BucketPolicyFetch is the production fetch path: a single
// GetBucketPolicy call.
func defaultS3BucketPolicyFetch(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketPolicyOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
}

// mapS3BucketPolicy projects the GetBucketPolicy response into the
// typed AWSS3BucketPolicy struct. The document is JSON-compacted for a
// stable `policy` string; a non-JSON document is a hard error (the
// required `policy` argument must hold valid JSON). The `region`
// attribute is Computed and intentionally not populated (decision #5).
func mapS3BucketPolicy(bucket string, out *s3.GetBucketPolicyOutput) (*generated.AWSS3BucketPolicy, error) {
	typed := &generated.AWSS3BucketPolicy{
		Bucket: generated.LiteralOf(bucket),
		ID:     generated.LiteralOf(bucket),
	}
	if out == nil {
		return typed, nil
	}
	doc, err := compactPolicyJSON(aws.ToString(out.Policy))
	if err != nil {
		return nil, fmt.Errorf("decode policy: %w", err)
	}
	if doc != "" {
		typed.Policy = generated.LiteralOf(doc)
	}
	return typed, nil
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*s3BucketPolicyEnricher)(nil)
	_ ByIDEnricher      = (*s3BucketPolicyEnricher)(nil)
)
