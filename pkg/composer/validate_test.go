package composer

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateComputeExclusivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		keys            []ComponentKey
		wantErr         bool
		errMsg          string   // substring expected in error message
		errContainsKeys []string // specific key names that must appear in error
	}{
		{
			name:    "empty keys",
			keys:    []ComponentKey{},
			wantErr: false,
		},
		{
			name:    "valid: Lambda only (serverless)",
			keys:    []ComponentKey{KeyLambda, KeyAPIGateway, KeyVPC},
			wantErr: false,
		},
		{
			name:    "valid: AWS Lambda prefixed only",
			keys:    []ComponentKey{KeyAWSLambda, KeyAWSAPIGateway, KeyAWSVPC},
			wantErr: false,
		},
		{
			name:    "valid: EKS + EC2 (container)",
			keys:    []ComponentKey{KeyResource, KeyEC2, KeyVPC, KeyALB},
			wantErr: false,
		},
		{
			name:    "valid: AWS EKS + EC2 prefixed",
			keys:    []ComponentKey{KeyAWSEKS, KeyAWSEC2, KeyAWSVPC},
			wantErr: false,
		},
		{
			name:    "valid: single serverless key alone",
			keys:    []ComponentKey{KeyLambda},
			wantErr: false,
		},
		{
			name:    "valid: single container key alone",
			keys:    []ComponentKey{KeyResource},
			wantErr: false,
		},
		{
			name:    "valid: GKE only",
			keys:    []ComponentKey{KeyGCPGKE, KeyGCPVPC},
			wantErr: false,
		},
		{
			name:    "valid: GCP Cloud Run only (serverless)",
			keys:    []ComponentKey{KeyGCPCloudRun, KeyGCPVPC},
			wantErr: false,
		},
		{
			name:    "valid: GCP Cloud Functions only (serverless)",
			keys:    []ComponentKey{KeyGCPCloudFunctions, KeyGCPVPC},
			wantErr: false,
		},
		{
			name:    "valid: GCP Cloud Run + Cloud Functions (both serverless)",
			keys:    []ComponentKey{KeyGCPCloudRun, KeyGCPCloudFunctions, KeyGCPVPC},
			wantErr: false,
		},
		{
			name:    "valid: cross-cloud serverless+container not flagged",
			keys:    []ComponentKey{KeyAWSLambda, KeyGCPGKE},
			wantErr: false, // cross-cloud is rejected elsewhere; this validator checks within-cloud only
		},
		// --- Invalid AWS combinations ---
		{
			name:            "invalid: Lambda + EKS (legacy keys)",
			keys:            []ComponentKey{KeyLambda, KeyResource, KeyVPC},
			wantErr:         true,
			errMsg:          "incompatible AWS compute",
			errContainsKeys: []string{"lambda", "resource"},
		},
		{
			name:            "invalid: Lambda + EC2",
			keys:            []ComponentKey{KeyLambda, KeyEC2, KeyVPC},
			wantErr:         true,
			errMsg:          "incompatible AWS compute",
			errContainsKeys: []string{"lambda", "ec2"},
		},
		{
			name:            "invalid: AWS Lambda + AWS EKS (prefixed)",
			keys:            []ComponentKey{KeyAWSLambda, KeyAWSEKS, KeyAWSVPC},
			wantErr:         true,
			errMsg:          "incompatible AWS compute",
			errContainsKeys: []string{"aws_lambda", "aws_eks"},
		},
		{
			name:            "invalid: AWS Lambda + AWS ECS",
			keys:            []ComponentKey{KeyAWSLambda, KeyAWSECS, KeyAWSVPC},
			wantErr:         true,
			errMsg:          "incompatible AWS compute",
			errContainsKeys: []string{"aws_lambda", "aws_ecs"},
		},
		{
			name:            "invalid: AWS Lambda + AWS EC2",
			keys:            []ComponentKey{KeyAWSLambda, KeyAWSEC2, KeyAWSVPC},
			wantErr:         true,
			errMsg:          "incompatible AWS compute",
			errContainsKeys: []string{"aws_lambda", "aws_ec2"},
		},
		{
			name:            "invalid: mixed legacy Lambda + prefixed EKS",
			keys:            []ComponentKey{KeyLambda, KeyAWSEKS},
			wantErr:         true,
			errMsg:          "incompatible AWS compute",
			errContainsKeys: []string{"lambda", "aws_eks"},
		},
		{
			name:            "invalid: duplicate keys still caught",
			keys:            []ComponentKey{KeyLambda, KeyLambda, KeyResource},
			wantErr:         true,
			errMsg:          "incompatible AWS compute",
			errContainsKeys: []string{"lambda", "resource"},
		},
		// --- Invalid GCP combinations ---
		{
			name:            "invalid: Cloud Functions + GKE",
			keys:            []ComponentKey{KeyGCPCloudFunctions, KeyGCPGKE, KeyGCPVPC},
			wantErr:         true,
			errMsg:          "incompatible GCP compute",
			errContainsKeys: []string{"gcp_cloud_functions", "gcp_gke"},
		},
		{
			name:            "invalid: Cloud Run + GKE",
			keys:            []ComponentKey{KeyGCPCloudRun, KeyGCPGKE, KeyGCPVPC},
			wantErr:         true,
			errMsg:          "incompatible GCP compute",
			errContainsKeys: []string{"gcp_cloud_run", "gcp_gke"},
		},
		{
			name:            "invalid: Cloud Run + Cloud Functions + GKE",
			keys:            []ComponentKey{KeyGCPCloudRun, KeyGCPCloudFunctions, KeyGCPGKE},
			wantErr:         true,
			errMsg:          "incompatible GCP compute",
			errContainsKeys: []string{"gcp_gke"},
		},
		// --- Non-compute components only ---
		{
			name:    "valid: only storage/network components",
			keys:    []ComponentKey{KeyVPC, KeyPostgres, KeyS3, KeyElastiCache},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateComputeExclusivity(tt.keys)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
				for _, wantKey := range tt.errContainsKeys {
					require.Contains(t, err.Error(), wantKey,
						"error should mention the specific conflicting key %q", wantKey)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateComputeExclusivity_ReturnsValidationError(t *testing.T) {
	t.Parallel()

	// Direct error should be a *ValidationError
	err := ValidateComputeExclusivity([]ComponentKey{KeyAWSLambda, KeyAWSEKS})
	require.Error(t, err)

	var ve *ValidationError
	require.True(t, errors.As(err, &ve), "error should be *ValidationError")
	require.Contains(t, ve.Error(), "incompatible AWS compute")

	// Wrapped error should still match via errors.As
	wrapped := fmt.Errorf("compose stack: %w", err)
	require.True(t, errors.As(wrapped, &ve), "wrapped error should still match *ValidationError")
}

func TestValidateComputeExclusivityWithOpts_AllowsLegacyStandaloneEC2Lambda(t *testing.T) {
	t.Parallel()

	keys := []ComponentKey{KeyAWSLambda, KeyAWSEC2, KeyAWSVPC}
	err := ValidateComputeExclusivityWithOpts(keys, ComputeExclusivityOpts{
		AllowLegacyStandaloneEC2Lambda: true,
	})
	require.NoError(t, err)
}

func TestValidateComputeExclusivityWithOpts_DoesNotAllowEKSLambda(t *testing.T) {
	t.Parallel()

	keys := []ComponentKey{KeyAWSLambda, KeyAWSEKS, KeyAWSVPC}
	err := ValidateComputeExclusivityWithOpts(keys, ComputeExclusivityOpts{
		AllowLegacyStandaloneEC2Lambda: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "aws_lambda")
	require.Contains(t, err.Error(), "aws_eks")
}

func TestValidateRemovals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		removed   []ComponentKey
		remaining []ComponentKey
		wantWarn  int
		wantKeys  []string // substrings expected in FormatRemovalWarnings output
	}{
		{
			name:     "no removals",
			removed:  nil,
			wantWarn: 0,
		},
		{
			name:      "safe removal — no dependents",
			removed:   []ComponentKey{KeyAWSSQS},
			remaining: []ComponentKey{KeyAWSVPC, KeyAWSRDS},
			wantWarn:  0,
		},
		{
			name:      "remove VPC breaks ALB",
			removed:   []ComponentKey{KeyAWSVPC},
			remaining: []ComponentKey{KeyAWSALB},
			wantWarn:  1,
			wantKeys:  []string{"aws_vpc", "aws_alb"},
		},
		{
			name:      "remove VPC breaks multiple dependents",
			removed:   []ComponentKey{KeyAWSVPC},
			remaining: []ComponentKey{KeyAWSALB, KeyAWSRDS, KeyAWSElastiCache, KeyAWSEKS},
			wantWarn:  1,
			wantKeys:  []string{"aws_vpc", "aws_alb", "aws_rds", "aws_elasticache", "aws_eks"},
		},
		{
			name:      "remove ALB breaks CloudFront",
			removed:   []ComponentKey{KeyAWSALB},
			remaining: []ComponentKey{KeyAWSVPC, KeyAWSCloudfront},
			wantWarn:  1,
			wantKeys:  []string{"aws_alb", "aws_cloudfront"},
		},
		{
			name:      "remove S3 and OpenSearch breaks Bedrock",
			removed:   []ComponentKey{KeyAWSS3, KeyAWSOpenSearch},
			remaining: []ComponentKey{KeyAWSVPC, KeyAWSBedrock},
			wantWarn:  2,
			wantKeys:  []string{"aws_s3", "aws_opensearch", "aws_bedrock"},
		},
		{
			name:      "GCP: remove VPC breaks CloudSQL and GKE",
			removed:   []ComponentKey{KeyGCPVPC},
			remaining: []ComponentKey{KeyGCPCloudSQL, KeyGCPGKE},
			wantWarn:  1,
			wantKeys:  []string{"gcp_vpc", "gcp_cloudsql", "gcp_gke"},
		},
		{
			name:      "GCP: remove load balancer breaks CDN",
			removed:   []ComponentKey{KeyGCPLoadbalancer},
			remaining: []ComponentKey{KeyGCPVPC, KeyGCPCloudCDN},
			wantWarn:  1,
			wantKeys:  []string{"gcp_loadbalancer", "gcp_cloud_cdn"},
		},
		{
			name:      "remove dependent too — no warning",
			removed:   []ComponentKey{KeyAWSVPC, KeyAWSALB},
			remaining: []ComponentKey{KeyAWSSQS},
			wantWarn:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			warnings := ValidateRemovals(tt.removed, tt.remaining)
			require.Len(t, warnings, tt.wantWarn)
			if tt.wantWarn > 0 {
				formatted := FormatRemovalWarnings(warnings)
				for _, key := range tt.wantKeys {
					require.Contains(t, formatted, key,
						"formatted warning should mention %q", key)
				}
			}
		})
	}
}

func TestFormatRemovalWarnings_Empty(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", FormatRemovalWarnings(nil))
}

func TestDiffComponents_RemovalWarnings(t *testing.T) {
	t.Parallel()
	// Remove VPC while ALB and RDS remain active
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private","aws_alb":true,"aws_rds":true}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_alb":true,"aws_rds":true}`)

	diffs := DiffComponents(oldComp, newComp)
	require.Len(t, diffs, 1)
	require.Equal(t, "aws_vpc", diffs[0].Component)
	require.Equal(t, "removed", diffs[0].Action)
	require.NotEmpty(t, diffs[0].Warnings, "removing VPC with active ALB+RDS should produce warnings")

	// Warning should mention the dependents
	warning := strings.Join(diffs[0].Warnings, " ")
	require.Contains(t, warning, "aws_alb")
	require.Contains(t, warning, "aws_rds")
}

func TestDiffComponents_SafeRemovalNoWarnings(t *testing.T) {
	t.Parallel()
	// Remove SQS — nothing depends on it
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private","aws_sqs":true}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private"}`)

	diffs := DiffComponents(oldComp, newComp)
	require.Len(t, diffs, 1)
	require.Equal(t, "aws_sqs", diffs[0].Component)
	require.Empty(t, diffs[0].Warnings, "removing SQS should not produce warnings")
}

// TestValidateNoLegacyKeys locks in the Phase 3b contract that the composer's
// public surface rejects legacy ComponentKeys. Adapters (reliable
// composeradapter) must upgrade session JSON before handing it off.
func TestValidateNoLegacyKeys(t *testing.T) {
	t.Run("prefixed-only selection passes", func(t *testing.T) {
		require.NoError(t, ValidateNoLegacyKeys([]ComponentKey{KeyAWSVPC, KeyAWSRDS, KeyAWSS3}))
	})

	t.Run("polymorphic keys pass", func(t *testing.T) {
		// KeyResource / KeyEC2 are polymorphic, not renamed in LegacyToV2Key.
		// Phase 4 renames them to unambiguous prefixed names; until then
		// they remain valid.
		require.NoError(t, ValidateNoLegacyKeys([]ComponentKey{KeyAWSVPC, KeyResource, KeyEC2}))
	})

	t.Run("third-party toggles pass", func(t *testing.T) {
		// Splunk / Datadog have no AWS-prefixed siblings.
		require.NoError(t, ValidateNoLegacyKeys([]ComponentKey{KeySplunk, KeyDatadog}))
	})

	t.Run("empty selection passes", func(t *testing.T) {
		require.NoError(t, ValidateNoLegacyKeys(nil))
		require.NoError(t, ValidateNoLegacyKeys([]ComponentKey{}))
	})

	t.Run("single legacy key fails with actionable pair", func(t *testing.T) {
		err := ValidateNoLegacyKeys([]ComponentKey{KeyVPC})
		require.Error(t, err)
		var ve *ValidationError
		require.ErrorAs(t, err, &ve, "must return a ValidationError so handlers map to HTTP 400")
		require.Contains(t, err.Error(), "vpc → aws_vpc")
		require.Contains(t, err.Error(), "composeradapter",
			"error must point callers at the upgrade path")
	})

	t.Run("multiple legacy keys all reported", func(t *testing.T) {
		err := ValidateNoLegacyKeys([]ComponentKey{KeyVPC, KeyAWSRDS, KeyALB, KeyBackups})
		require.Error(t, err)
		msg := err.Error()
		require.Contains(t, msg, "vpc → aws_vpc")
		require.Contains(t, msg, "alb → aws_alb")
		require.Contains(t, msg, "backups → aws_backups")
	})
}
