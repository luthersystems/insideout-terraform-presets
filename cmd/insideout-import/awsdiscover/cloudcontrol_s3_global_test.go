package awsdiscover

import "testing"

// TestS3BucketIsGlobal pins the #1860 fix: aws_s3_bucket must be a GLOBAL
// Cloud Control type. S3 bucket ARNs carry no region and ListBuckets/RGT are
// account-global, so a per-region S3 config emits the same bucket once per
// scanned region (each stamped with the wrong scan region) — which broke the
// per-region reverse-import engine. Marking it global makes the discoverer
// scan once (region="") and read the ARN-deduped set via RGTCacheForGlobalCFN,
// so each bucket appears exactly once. A regression that flips this back to
// per-region resurrects the cross-region duplicate/mis-tag bug.
func TestS3BucketIsGlobal(t *testing.T) {
	t.Parallel()
	var matches []cloudControlConfig
	for _, cfg := range cloudControlTypeConfigs {
		if cfg.TFType == "aws_s3_bucket" {
			matches = append(matches, cfg)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one aws_s3_bucket config in cloudControlTypeConfigs, found %d", len(matches))
	}
	if !matches[0].IsGlobal {
		t.Errorf("aws_s3_bucket must be IsGlobal=true (#1860): S3 is account-global; "+
			"per-region scanning duplicates buckets across regions with bogus scan-region tags. got IsGlobal=%v", matches[0].IsGlobal)
	}
}
