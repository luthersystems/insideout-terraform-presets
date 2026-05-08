package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// s3Client is the narrow subset of the S3 SDK the discoverer uses.
type s3Client interface {
	ListBuckets(ctx context.Context, in *s3.ListBucketsInput, opts ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	HeadBucket(ctx context.Context, in *s3.HeadBucketInput, opts ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	GetBucketTagging(ctx context.Context, in *s3.GetBucketTaggingInput, opts ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error)
}

type s3Discoverer struct {
	new func(region string) s3Client
}

func newS3Discoverer(cfg aws.Config) Discoverer {
	return &s3Discoverer{new: func(region string) s3Client {
		return s3.NewFromConfig(cfg, func(o *s3.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *s3Discoverer) ResourceType() string { return "aws_s3_bucket" }

// Discover lists buckets and filters by name prefix matching project.
// S3's ListBuckets is account-global (returns every bucket regardless
// of region) and unpaginated; the prefix filter is client-side.
//
// S3 buckets have a real home region, but ListBuckets does not return
// it inline. PR 2 stamps Identity.Region with args.Regions[0] (the
// operator's first --regions value, or the configured cfg.Region for
// back-compat). Per-bucket GetBucketLocation is a follow-up to get the
// accurate stamp; the operator-supplied region carries forward as
// today.
//
// Per-bucket GetBucketTagging fetches the tag map for tag-selector
// post-filtering and tag persistence onto Identity.Tags. S3 returns
// NoSuchTagSet for buckets without any tags — we normalize that into
// an empty map so the nil-vs-empty distinction holds.
//
// Import ID for aws_s3_bucket is the bucket name.
func (d *s3Discoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	stampRegion := ""
	if len(args.Regions) > 0 {
		stampRegion = args.Regions[0]
	}
	client := d.new(stampRegion)
	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("ListBuckets: %w", err)
	}

	var names []string
	for _, b := range out.Buckets {
		name := aws.ToString(b.Name)
		if args.Project != "" && !strings.HasPrefix(name, args.Project) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(names))
	for _, name := range names {
		tags, err := fetchS3BucketTags(ctx, client, name)
		if err != nil {
			return nil, fmt.Errorf("GetBucketTagging (bucket=%s): %w", name, err)
		}
		if !MatchesAll(tags, args.TagSelectors) {
			continue
		}
		arn := fmt.Sprintf("arn:aws:s3:::%s", name)
		imps = append(imps, makeImportedResource(
			book,
			"aws_s3_bucket",
			name,
			name,
			stampRegion,
			args.AccountID,
			map[string]string{"arn": arn},
			tags,
		))
	}
	return imps, nil
}

// fetchS3BucketTags returns the bucket's tag map. S3's GetBucketTagging
// returns NoSuchTagSet (typed *s3types.NoSuchBucket is rare here; the
// real error code is "NoSuchTagSet" surfaced as a generic API error
// with that code) for buckets that have never had tags applied; we
// normalize that into an empty map so the nil-vs-empty distinction
// (nil ⇒ "didn't fetch", empty ⇒ "no tags") holds.
func fetchS3BucketTags(ctx context.Context, client s3Client, bucket string) (map[string]string, error) {
	out, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(bucket)})
	if err != nil {
		// Match by error-code string — S3 surfaces NoSuchTagSet as a
		// generic *smithy.GenericAPIError (no typed shape); using
		// strings.Contains keeps us decoupled from the smithy package.
		if strings.Contains(err.Error(), "NoSuchTagSet") {
			return map[string]string{}, nil
		}
		return nil, err
	}
	tags := make(map[string]string, len(out.TagSet))
	for _, t := range out.TagSet {
		tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return tags, nil
}

// DiscoverByID resolves an S3 bucket by ARN (arn:aws:s3:::<name>) or
// bare bucket name. Issues a single HeadBucket call to verify
// existence; HeadBucket returns *s3types.NotFound for missing buckets
// in the v2 SDK.
func (d *s3Discoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := s3NameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(name)}); err != nil {
		var notFound *s3types.NotFound
		var noBucket *s3types.NoSuchBucket
		if errors.As(err, &notFound) || errors.As(err, &noBucket) {
			return imported.ImportedResource{}, fmt.Errorf("aws_s3_bucket %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("HeadBucket: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:s3:::%s", name)
	return makeImportedResource(
		addressBook{},
		"aws_s3_bucket",
		name,
		name,
		region,
		accountID,
		map[string]string{"arn": arn},
		nil,
	), nil
}

// s3NameFromID extracts the bucket name from an ARN (arn:aws:s3:::<name>)
// or bare bucket name. S3 ARNs are unique in that the resource portion
// is the entire bucket name (no service-region/account scoping).
func s3NameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("s3: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("s3: parse arn: %w", err)
		}
		if parsed.Service != "s3" {
			return "", fmt.Errorf("s3: not an s3 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// arn:aws:s3:::<bucket> — Resource = "<bucket>"; reject ARNs
		// whose Resource carries an object key (contains "/") since
		// those refer to object identities, not the bucket resource.
		if parsed.Resource == "" || strings.Contains(parsed.Resource, "/") {
			return "", fmt.Errorf("s3: arn resource %q is not a bare bucket name: %w", parsed.Resource, ErrNotSupported)
		}
		return parsed.Resource, nil
	}
	// S3 bucket names: lowercase letters, digits, hyphens, dots; reject
	// anything obviously malformed.
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("s3: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
