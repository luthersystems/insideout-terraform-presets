package imported

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resWith is a tiny helper that builds an ImportedResource fixture for
// summary tests; only the Identity fields the summarizer reads are
// populated.
func resWith(tfType, region string, tags map[string]string) ImportedResource {
	return ImportedResource{
		Identity: ResourceIdentity{
			Cloud:    "aws",
			Type:     tfType,
			Address:  tfType + ".x",
			ImportID: tfType + "-x",
			Region:   region,
			Tags:     tags,
		},
	}
}

// TestSummarizeResources_TotalAndImportable pins the Total / Importable /
// Unsupported relationship: Importable == len(resources), Unsupported is
// the caller-supplied count, Total == sum.
func TestSummarizeResources_TotalAndImportable(t *testing.T) {
	t.Parallel()
	five := []ImportedResource{
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", nil),
	}

	t.Run("no_unsupported", func(t *testing.T) {
		t.Parallel()
		got := SummarizeResources(five, SummaryOpts{Cloud: "aws"})
		assert.Equal(t, 5, got.Total, "Total")
		assert.Equal(t, 5, got.Importable, "Importable")
		assert.Equal(t, 0, got.Unsupported, "Unsupported")
	})

	t.Run("with_unsupported", func(t *testing.T) {
		t.Parallel()
		got := SummarizeResources(five, SummaryOpts{Cloud: "aws", UnsupportedCount: 2})
		assert.Equal(t, 7, got.Total, "Total = Importable + Unsupported")
		assert.Equal(t, 5, got.Importable, "Importable counts Resources only")
		assert.Equal(t, 2, got.Unsupported, "Unsupported pass-through")
	})
}

// TestSummarizeResources_ByType_BucketsByIdentityType pins ByType bucketing.
// A regression that bucketed by Cloud or Address would surface here.
func TestSummarizeResources_ByType_BucketsByIdentityType(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_lambda_function", "us-east-1", nil),
		resWith("aws_lambda_function", "us-east-1", nil),
		resWith("aws_s3_bucket", "us-east-1", nil),
	}
	got := SummarizeResources(rs, SummaryOpts{Cloud: "aws"})
	assert.Equal(t, 3, got.ByType["aws_sqs_queue"])
	assert.Equal(t, 2, got.ByType["aws_lambda_function"])
	assert.Equal(t, 1, got.ByType["aws_s3_bucket"])
	assert.Len(t, got.ByType, 3, "no extra buckets")
}

// TestSummarizeResources_ByRegion_BucketsByIdentityRegion pins multi-region
// bucketing. A regression that bucketed by Cloud or fell back to Location
// would surface here.
func TestSummarizeResources_ByRegion_BucketsByIdentityRegion(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "eu-west-1", nil),
		resWith("aws_sqs_queue", "ap-south-1", nil),
	}
	got := SummarizeResources(rs, SummaryOpts{Cloud: "aws"})
	assert.Equal(t, 2, got.ByRegion["us-east-1"])
	assert.Equal(t, 1, got.ByRegion["eu-west-1"])
	assert.Equal(t, 1, got.ByRegion["ap-south-1"])
}

// TestSummarizeResources_ByRegion_EmptyRegionLandsInEmptyKey pins that
// the region totals match Importable when some resources have no Region.
// A regression that dropped the empty-region bucket would underflow the
// summary's by_region totals vs. Importable.
func TestSummarizeResources_ByRegion_EmptyRegionLandsInEmptyKey(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_iam_role", "", nil),
	}
	got := SummarizeResources(rs, SummaryOpts{Cloud: "aws"})
	assert.Equal(t, 1, got.ByRegion[""], "global resources land in the \"\" bucket")
	sum := 0
	for _, n := range got.ByRegion {
		sum += n
	}
	assert.Equal(t, got.Importable, sum, "ByRegion totals match Importable")
}

// TestSummarizeResources_ByTag_OneEntryPerKVPair pins that a single
// resource with N tags contributes N entries to ByTag.
func TestSummarizeResources_ByTag_OneEntryPerKVPair(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		resWith("aws_sqs_queue", "us-east-1", map[string]string{
			"env":  "prod",
			"team": "growth",
		}),
	}
	got := SummarizeResources(rs, SummaryOpts{Cloud: "aws"})
	assert.Equal(t, 1, got.ByTag["env=prod"])
	assert.Equal(t, 1, got.ByTag["team=growth"])
	assert.Len(t, got.ByTag, 2, "exactly one entry per kv pair")
}

// TestSummarizeResources_ByTag_NilTagsContributeNothing pins the #291 nil-
// vs-empty distinction: a resource with nil Tags doesn't synthesize an
// empty bucket. A regression that emitted "=" or similar for nil-tagged
// resources would surface here.
func TestSummarizeResources_ByTag_NilTagsContributeNothing(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		resWith("aws_sqs_queue", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", map[string]string{}),
	}
	got := SummarizeResources(rs, SummaryOpts{Cloud: "aws"})
	assert.Empty(t, got.ByTag, "nil/empty Tags must not contribute")
	assert.NotNil(t, got.ByTag, "ByTag must be non-nil empty map")
}

// TestSummarizeResources_ByTag_DuplicatePairsSumAcrossResources pins
// the cross-resource accumulation: two resources tagged env=prod
// produce one entry with count 2.
func TestSummarizeResources_ByTag_DuplicatePairsSumAcrossResources(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		resWith("aws_sqs_queue", "us-east-1", map[string]string{"env": "prod"}),
		resWith("aws_lambda_function", "us-east-1", map[string]string{"env": "prod"}),
		resWith("aws_s3_bucket", "us-east-1", map[string]string{"env": "staging"}),
	}
	got := SummarizeResources(rs, SummaryOpts{Cloud: "aws"})
	assert.Equal(t, 2, got.ByTag["env=prod"])
	assert.Equal(t, 1, got.ByTag["env=staging"])
}

// TestSummarizeResources_ByGroup_UsesCategoryHelper pins that ByGroup is
// derived from imported.Category. A regression that ran an inline
// type→group switch would diverge from the Category map and the wizard's
// group rendering would split.
func TestSummarizeResources_ByGroup_UsesCategoryHelper(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		// aws_sqs_queue → CategoryEvents
		resWith("aws_sqs_queue", "us-east-1", nil),
		// aws_s3_bucket → CategoryDataStorage
		resWith("aws_s3_bucket", "us-east-1", nil),
		resWith("aws_s3_bucket", "us-east-1", nil),
		// aws_lambda_function → CategoryVirtualMachines
		resWith("aws_lambda_function", "us-east-1", nil),
	}
	got := SummarizeResources(rs, SummaryOpts{Cloud: "aws"})
	assert.Equal(t, 1, got.ByGroup[CategoryEvents])
	assert.Equal(t, 2, got.ByGroup[CategoryDataStorage])
	assert.Equal(t, 1, got.ByGroup[CategoryVirtualMachines])
}

// TestSummarizeResources_ByGroup_UnmappedTypeLandsInEmptyKey pins the
// "Other" fallback contract: a Terraform type with no Category mapping
// is bucketed under "" so the wizard's "Other" bucket is populated and
// the per-group totals still match Importable.
func TestSummarizeResources_ByGroup_UnmappedTypeLandsInEmptyKey(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		resWith("aws_brand_new_thing_2099", "us-east-1", nil),
		resWith("aws_sqs_queue", "us-east-1", nil),
	}
	got := SummarizeResources(rs, SummaryOpts{Cloud: "aws"})
	assert.Equal(t, 1, got.ByGroup[""])
	assert.Equal(t, 1, got.ByGroup[CategoryEvents])
}

// TestSummarizeResources_EmptyInputProducesValidShape pins the "no
// resources discovered" wire shape: every map non-nil-empty, every slice
// non-nil-empty, JSON marshals every map as `{}` and every slice as `[]`.
// A regression that left a nil map would surface as `null` in the body.
func TestSummarizeResources_EmptyInputProducesValidShape(t *testing.T) {
	t.Parallel()
	got := SummarizeResources(nil, SummaryOpts{Cloud: "aws"})
	assert.Equal(t, 0, got.Total)
	assert.Equal(t, 0, got.Importable)
	assert.Equal(t, 0, got.Unsupported)

	require.NotNil(t, got.ByType)
	require.NotNil(t, got.ByRegion)
	require.NotNil(t, got.ByTag)
	require.NotNil(t, got.ByGroup)
	require.NotNil(t, got.ScanSummary.RegionsScanned)
	require.NotNil(t, got.ScanSummary.TagSelectors)

	body, err := json.Marshal(got)
	require.NoError(t, err)
	// Spot-check a few of the canonical empty markers; a `null` for
	// any of the four maps or two slices would slip past Empty() on
	// the in-memory struct.
	for _, want := range []string{
		`"by_type":{}`,
		`"by_region":{}`,
		`"by_tag":{}`,
		`"by_group":{}`,
		`"regions_scanned":[]`,
		`"tag_selectors":[]`,
	} {
		assert.Contains(t, string(body), want,
			"empty summary must serialize %q", want)
	}
}

// TestSummarizeResources_ScanSummaryReflectsOpts pins that the scope
// inputs (Cloud, Duration, Regions, TagSelectors) round-trip into
// ScanSummary.
func TestSummarizeResources_ScanSummaryReflectsOpts(t *testing.T) {
	t.Parallel()
	got := SummarizeResources(nil, SummaryOpts{
		Cloud:    "gcp",
		Duration: 12345 * time.Millisecond,
		Regions:  []string{"us-central1", "europe-west1"},
		TagSelectors: []SummaryTagSelector{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "growth"},
		},
	})
	assert.Equal(t, "gcp", got.ScanSummary.Cloud)
	assert.Equal(t, int64(12345), got.ScanSummary.DurationMs)
	assert.Equal(t, []string{"us-central1", "europe-west1"}, got.ScanSummary.RegionsScanned)
	// TagSelectors are sorted into the canonical key=value form so a
	// regression that left them in caller order or dropped the sort
	// surfaces here.
	assert.Equal(t, []string{"env=prod", "team=growth"}, got.ScanSummary.TagSelectors)
}

// TestSummarizeResources_RegionsSliceIsCopied pins that ScanSummary's
// RegionsScanned is a clone of opts.Regions, not the same backing array.
// A regression that aliased the caller's slice would let later
// mutations leak into a previously-emitted summary.
func TestSummarizeResources_RegionsSliceIsCopied(t *testing.T) {
	t.Parallel()
	in := []string{"us-east-1", "eu-west-1"}
	got := SummarizeResources(nil, SummaryOpts{Cloud: "aws", Regions: in})
	in[0] = "MUTATED"
	assert.Equal(t, "us-east-1", got.ScanSummary.RegionsScanned[0],
		"caller's slice mutation must not leak into the summary")
}

// TestSummarizeResources_DeterministicAcrossRuns pins byte-identical JSON
// across runs of the same input. Caller ordering and map iteration order
// must not influence the output.
func TestSummarizeResources_DeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	rs := []ImportedResource{
		resWith("aws_sqs_queue", "us-east-1", map[string]string{"env": "prod", "team": "growth"}),
		resWith("aws_lambda_function", "eu-west-1", map[string]string{"env": "staging"}),
		resWith("aws_s3_bucket", "us-east-1", map[string]string{"env": "prod"}),
	}
	opts := SummaryOpts{
		Cloud:            "aws",
		UnsupportedCount: 3,
		Duration:         500 * time.Millisecond,
		Regions:          []string{"us-east-1", "eu-west-1"},
		TagSelectors: []SummaryTagSelector{
			{Key: "env", Value: "prod"},
		},
	}
	// Marshal five times — Go's map iteration is non-deterministic, so
	// any latent reliance on map order would flake within a few iters.
	var first []byte
	for i := 0; i < 5; i++ {
		body, err := json.MarshalIndent(SummarizeResources(rs, opts), "", "  ")
		require.NoError(t, err)
		if i == 0 {
			first = body
			continue
		}
		assert.Equal(t, string(first), string(body),
			"run %d: summary JSON must be byte-identical across runs", i)
	}
}

// TestSummarizeResources_GoldenSnapshot pins the canonical fixture output
// against testdata/summary.golden. Re-seed with `UPDATE_GOLDEN=1 go test
// ./pkg/composer/imported/ -run TestSummarizeResources_GoldenSnapshot`.
func TestSummarizeResources_GoldenSnapshot(t *testing.T) {
	rs := []ImportedResource{
		// Two SQS queues across two regions, one tagged env=prod, one
		// tagged env=prod team=growth.
		{
			Identity: ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.alpha",
				ImportID: "https://example/alpha",
				Region:   "us-east-1",
				Tags:     map[string]string{"env": "prod"},
			},
		},
		{
			Identity: ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.bravo",
				ImportID: "https://example/bravo",
				Region:   "eu-west-1",
				Tags:     map[string]string{"env": "prod", "team": "growth"},
			},
		},
		// One Lambda in us-east-1, no tags (Identity.Tags=nil — the
		// discoverer didn't fetch tags for this row).
		{
			Identity: ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_lambda_function",
				Address:  "aws_lambda_function.charlie",
				ImportID: "charlie",
				Region:   "us-east-1",
			},
		},
		// One S3 bucket with empty Tags map (the discoverer fetched
		// tags but the bucket genuinely has none).
		{
			Identity: ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_s3_bucket",
				Address:  "aws_s3_bucket.delta",
				ImportID: "delta",
				Region:   "us-east-1",
				Tags:     map[string]string{},
			},
		},
	}
	opts := SummaryOpts{
		Cloud:            "aws",
		UnsupportedCount: 4,
		Duration:         12345 * time.Millisecond,
		Regions:          []string{"us-east-1", "eu-west-1"},
		TagSelectors: []SummaryTagSelector{
			{Key: "env", Value: "prod"},
		},
	}
	body, err := json.MarshalIndent(SummarizeResources(rs, opts), "", "  ")
	require.NoError(t, err)
	body = append(body, '\n')

	goldenPath := filepath.Join("testdata", "summary.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, body, 0o644))
		t.Logf("wrote golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err,
		"golden missing — run `UPDATE_GOLDEN=1 go test ./pkg/composer/imported/ -run TestSummarizeResources_GoldenSnapshot`")
	require.Equal(t, string(want), string(body),
		"summary.json wire format drifted from %s. If this is intentional, re-seed via UPDATE_GOLDEN=1.",
		goldenPath)
}
