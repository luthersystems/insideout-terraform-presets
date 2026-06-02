package awsdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// S3 bucket PostDiscover follow-up (#cust3 item 3).
//
// aws_s3_bucket is discovered IsGlobal (one ARN-deduped ListBuckets / RGT
// pass) because S3 ARNs are region-less and ListBuckets is account-global.
// That leaves the discovered IR's Identity.Region empty, which reliable /
// genconfig backfill to the session/primary region (us-east-1). A bucket
// that actually lives in another region (us-west-2, eu-central-1, …) then
// lands in the wrong per-region genconfig pass under a us-east-1 provider,
// where `terraform plan -generate-config-out` fails the cross-region
// import and the bucket is silently dropped as no_generated_config.
//
// The s3_bucket AttributeEnricher already learns the true region from
// HeadBucket and promotes it into Identity.Region — but the reverse-import
// / genconfig dry-run path never runs EnrichAttributes, so that promotion
// never happens there and every bucket keeps an empty region. (Confirmed
// against a real whole-account run: every aws_s3_bucket in imported.json
// had region="" and enrichment_status=unset, and the lone us-west-2 bucket
// dropped.)
//
// s3BucketPostDiscover resolves the bucket's true region at DISCOVER time
// via GetBucketLocation and stamps both Identity.Region and
// NativeIDs["region"] so genconfig groups every bucket into its real
// region dir regardless of whether enrichment later runs. Soft-fails
// (returns an error the discoverer logs) without clobbering the IR.

// s3BucketLocator is the narrow subset of the S3 API the PostDiscover hook
// issues. Real *s3.Client and in-test fakes satisfy it.
type s3BucketLocator interface {
	GetBucketLocation(ctx context.Context, in *s3.GetBucketLocationInput, opts ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
}

// newS3BucketLocator is the production factory; tests swap it (or call
// s3BucketPostDiscoverWithClient directly) to inject a fake.
var newS3BucketLocator = func(awsCfg aws.Config, region string) s3BucketLocator {
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// s3BucketPostDiscover is the cloudControlConfig.PostDiscover hook for
// aws_s3_bucket. It resolves the bucket's region and promotes it into
// Identity.Region.
func s3BucketPostDiscover(ctx context.Context, awsCfg aws.Config, region string, ir *imported.ImportedResource) error {
	return s3BucketPostDiscoverWithClient(ctx, newS3BucketLocator(awsCfg, region), ir)
}

func s3BucketPostDiscoverWithClient(ctx context.Context, client s3BucketLocator, ir *imported.ImportedResource) error {
	if ir == nil {
		return nil
	}
	bucket := s3BucketNameForEnrich(&ir.Identity)
	if bucket == "" {
		return fmt.Errorf("s3_bucket: cannot derive bucket name from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	out, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(bucket)})
	if err != nil {
		return fmt.Errorf("s3_bucket %q: GetBucketLocation: %w", bucket, err)
	}
	region := s3LocationConstraintToRegion(out)
	if region == "" {
		// us-east-1 returns an empty LocationConstraint; default to it so a
		// bucket in the legacy global endpoint still groups deterministically.
		region = "us-east-1"
	}
	if ir.Identity.NativeIDs == nil {
		ir.Identity.NativeIDs = map[string]string{}
	}
	ir.Identity.NativeIDs["region"] = region
	ir.Identity.Region = region
	return nil
}

// s3LocationConstraintToRegion maps a GetBucketLocation response to a
// region string. The S3 API returns an empty LocationConstraint for
// us-east-1 (the legacy default) and the literal region for every other
// region; the historical "EU" alias maps to eu-west-1.
func s3LocationConstraintToRegion(out *s3.GetBucketLocationOutput) string {
	if out == nil {
		return ""
	}
	lc := strings.TrimSpace(string(out.LocationConstraint))
	switch lc {
	case "":
		return ""
	case "EU":
		return "eu-west-1"
	default:
		return lc
	}
}
