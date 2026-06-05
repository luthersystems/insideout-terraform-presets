package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
)

func TestParseBenchConfig(t *testing.T) {
	tests := []struct {
		name              string
		provider          string
		regions           string
		resourceTypes     string
		enrichConcurrency int
		maxConcurrency    int
		wantErr           string
		wantRegions       []string
		wantTypes         []string
		wantEnrichConc    int
		wantMaxConc       int
	}{
		{
			name:              "happy path single region",
			provider:          "aws",
			regions:           "us-east-1",
			enrichConcurrency: 16,
			maxConcurrency:    10,
			wantRegions:       []string{"us-east-1"},
			wantTypes:         nil,
			wantEnrichConc:    16,
			wantMaxConc:       10,
		},
		{
			name:              "multi region + type subset trims whitespace",
			provider:          "aws",
			regions:           "us-east-1, us-west-2 ",
			resourceTypes:     "aws_s3_bucket, aws_dynamodb_table",
			enrichConcurrency: 8,
			maxConcurrency:    4,
			wantRegions:       []string{"us-east-1", "us-west-2"},
			wantTypes:         []string{"aws_s3_bucket", "aws_dynamodb_table"},
			wantEnrichConc:    8,
			wantMaxConc:       4,
		},
		{
			name:              "enrich concurrency 0 means package default (allowed)",
			provider:          "aws",
			regions:           "us-east-1",
			enrichConcurrency: 0,
			maxConcurrency:    10,
			wantRegions:       []string{"us-east-1"},
			wantEnrichConc:    0,
			wantMaxConc:       10,
		},
		{
			name:              "empty provider is fatal",
			provider:          "",
			regions:           "us-east-1",
			enrichConcurrency: 16,
			maxConcurrency:    10,
			wantErr:           "--provider is required",
		},
		{
			name:              "gcp not yet supported",
			provider:          "gcp",
			regions:           "europe-west1",
			enrichConcurrency: 16,
			maxConcurrency:    10,
			wantErr:           "not yet supported",
		},
		{
			name:              "unknown provider is fatal",
			provider:          "azure",
			regions:           "us-east-1",
			enrichConcurrency: 16,
			maxConcurrency:    10,
			wantErr:           "unknown --provider",
		},
		{
			name:              "missing regions is fatal for aws",
			provider:          "aws",
			regions:           "",
			enrichConcurrency: 16,
			maxConcurrency:    10,
			wantErr:           "--regions is required",
		},
		{
			name:              "negative enrich concurrency is fatal",
			provider:          "aws",
			regions:           "us-east-1",
			enrichConcurrency: -1,
			maxConcurrency:    10,
			wantErr:           "--enrich-concurrency must be >= 0",
		},
		{
			name:              "non-positive max concurrency is fatal",
			provider:          "aws",
			regions:           "us-east-1",
			enrichConcurrency: 16,
			maxConcurrency:    0,
			wantErr:           "--max-concurrency must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseBenchConfig(tt.provider, tt.regions, tt.resourceTypes, tt.enrichConcurrency, tt.maxConcurrency)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantRegions, cfg.regions)
			assert.Equal(t, tt.wantTypes, cfg.resourceTypes)
			assert.Equal(t, tt.wantEnrichConc, cfg.enrichConcurrency)
			assert.Equal(t, tt.wantMaxConc, cfg.maxConcurrency)
		})
	}
}

func TestParseBenchConfigExpandsAllRegions(t *testing.T) {
	cfg, err := parseBenchConfig("aws", "all", "", 16, 10)
	require.NoError(t, err)
	assert.Greater(t, len(cfg.regions), 1, "expected `all` to expand to the InsideOut-supported region set, got %v", cfg.regions)
}

func TestBenchResultSummaryLines(t *testing.T) {
	r := benchResult{
		provider:         "aws",
		discoverConc:     10,
		enrichConc:       16,
		resourceCount:    512,
		typeCount:        23,
		enrichedCount:    500,
		enrichErrorCount: 12,
		discoverDuration: 18*time.Second + 400*time.Millisecond,
		enrichDuration:   92*time.Second + 300*time.Millisecond,
		totalDuration:    110*time.Second + 700*time.Millisecond,
	}
	got := r.summaryLines()
	want := []string{
		"bench: phase=discover concurrency=10 resources=512 types=23 duration=18.4s",
		"bench: phase=enrich concurrency=16 resources=512 enriched=500 errors=12 duration=1m32.3s",
		"bench: total provider=aws discover_concurrency=10 enrich_concurrency=16 resources=512 duration=1m50.7s",
	}
	assert.Equal(t, want, got)
}

func TestCountDistinctTypes(t *testing.T) {
	irs := []composerimported.ImportedResource{
		{Identity: composerimported.ResourceIdentity{Type: "aws_s3_bucket"}},
		{Identity: composerimported.ResourceIdentity{Type: "aws_s3_bucket"}},
		{Identity: composerimported.ResourceIdentity{Type: "aws_dynamodb_table"}},
	}
	assert.Equal(t, 2, countDistinctTypes(irs))
	assert.Equal(t, 0, countDistinctTypes(nil))
}

func TestCountEnrichOutcomes(t *testing.T) {
	irs := []composerimported.ImportedResource{
		{Identity: composerimported.ResourceIdentity{EnrichmentStatus: composerimported.EnrichmentStatusFull}},
		{Identity: composerimported.ResourceIdentity{EnrichmentStatus: composerimported.EnrichmentStatusPartial}},
		{Identity: composerimported.ResourceIdentity{EnrichmentStatus: composerimported.EnrichmentStatusFailed}},
		// Unknown / not-a-candidate types are excluded from both counts.
		{Identity: composerimported.ResourceIdentity{EnrichmentStatus: composerimported.EnrichmentStatusUnknown}},
		{Identity: composerimported.ResourceIdentity{}},
	}
	enriched, failed := countEnrichOutcomes(irs)
	assert.Equal(t, 2, enriched)
	assert.Equal(t, 1, failed)
}

// fakeProvider is a minimal imp.Provider that records its Discover /
// EnrichAttributes inputs so the orchestrator can be tested without AWS. It
// embeds the static zero-state Provider for the introspection half of the
// interface (SupportedTypes etc.) and overrides only the two live methods
// the benchmark drives.
type fakeProvider struct {
	imp.Provider
	supported      []string
	gotDiscoverTyp []string
	gotEnrichConc  int
	discoverResult []composerimported.ImportedResource
	enrichErr      error
	enrichStamper  func(irs []composerimported.ImportedResource)
}

func (f *fakeProvider) SupportedTypes() []string { return f.supported }

func (f *fakeProvider) Discover(_ context.Context, types []string, _ imp.Clients, _ imp.DiscoverOpts) ([]composerimported.ImportedResource, error) {
	f.gotDiscoverTyp = types
	return f.discoverResult, nil
}

func (f *fakeProvider) EnrichAttributes(_ context.Context, irs []composerimported.ImportedResource, _ imp.Clients, opts ...imp.EnrichOpts) error {
	if len(opts) > 0 {
		f.gotEnrichConc = opts[0].Concurrency
	}
	if f.enrichStamper != nil {
		f.enrichStamper(irs)
	}
	return f.enrichErr
}

func TestRunBenchAWSThreadsConcurrencyAndCounts(t *testing.T) {
	fp := &fakeProvider{
		supported: []string{"aws_s3_bucket", "aws_dynamodb_table"},
		discoverResult: []composerimported.ImportedResource{
			{Identity: composerimported.ResourceIdentity{Type: "aws_s3_bucket"}},
			{Identity: composerimported.ResourceIdentity{Type: "aws_s3_bucket"}},
			{Identity: composerimported.ResourceIdentity{Type: "aws_dynamodb_table"}},
		},
		// Stamp enrichment outcomes the way EnrichAttributes would.
		enrichStamper: func(irs []composerimported.ImportedResource) {
			irs[0].Identity.EnrichmentStatus = composerimported.EnrichmentStatusFull
			irs[1].Identity.EnrichmentStatus = composerimported.EnrichmentStatusFailed
			irs[2].Identity.EnrichmentStatus = composerimported.EnrichmentStatusFull
		},
		// A joined enrich error must NOT abort the run — it is data.
		enrichErr: errors.New("enrich: 1 resource failed"),
	}

	deps := benchAWSDeps{
		loadConfig: func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount: func(_ context.Context, _ aws.Config) (string, error) { return "123456789012", nil },
		newProvider: func(_ aws.Config, _ int, _ string) (imp.Provider, imp.Clients) {
			return fp, imp.Clients{}
		},
	}

	cfg := benchConfig{
		provider:          "aws",
		regions:           []string{"us-east-1"},
		resourceTypes:     nil, // exercise the SupportedTypes() default
		enrichConcurrency: 7,
		maxConcurrency:    5,
	}

	res, err := runBenchAWS(context.Background(), cfg, "", deps)
	require.NoError(t, err, "runBenchAWS must not abort on a non-fatal joined enrich error")
	assert.Equal(t, 7, fp.gotEnrichConc, "enrich concurrency must be threaded into EnrichOpts.Concurrency")
	assert.True(t, slices.Equal(fp.gotDiscoverTyp, fp.supported), "nil --resource-types must default to SupportedTypes()")
	assert.Equal(t, 3, res.resourceCount)
	assert.Equal(t, 2, res.typeCount)
	assert.Equal(t, 2, res.enrichedCount)
	assert.Equal(t, 1, res.enrichErrorCount)
	assert.Equal(t, 5, res.discoverConc)
	assert.Equal(t, 7, res.enrichConc)
}

func TestRunBenchAWSDiscoverErrorIsFatal(t *testing.T) {
	deps := benchAWSDeps{
		loadConfig: func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount: func(_ context.Context, _ aws.Config) (string, error) { return "123456789012", nil },
		newProvider: func(_ aws.Config, _ int, _ string) (imp.Provider, imp.Clients) {
			return &discoverErrProvider{}, imp.Clients{}
		},
	}
	cfg := benchConfig{provider: "aws", regions: []string{"us-east-1"}, enrichConcurrency: 16, maxConcurrency: 10}
	_, err := runBenchAWS(context.Background(), cfg, "", deps)
	require.Error(t, err, "discover-phase error must be fatal")
	assert.Contains(t, err.Error(), "discover phase")
}

type discoverErrProvider struct{ imp.Provider }

func (discoverErrProvider) SupportedTypes() []string { return []string{"aws_s3_bucket"} }
func (discoverErrProvider) Discover(_ context.Context, _ []string, _ imp.Clients, _ imp.DiscoverOpts) ([]composerimported.ImportedResource, error) {
	return nil, errors.New("boom")
}
