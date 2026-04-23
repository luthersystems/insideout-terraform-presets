package composer

import (
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// shim so this test can reuse the helper defined in compose_vm_test.go
func writeOutputs(t *testing.T, files Files, dir string) {
	writeBundle(t, dir, files)
}

// assertProviderBlocksHaveDefaultTags splits providers.tf by `provider "aws" {`
// and asserts that (1) exactly wantBlocks provider "aws" blocks exist, and
// (2) each block declares a default_tags block with Project = var.project and
// managed-by = "insideout". Split-and-check proves placement per block (a
// regression dropping default_tags from the alias block would otherwise slip
// past a global strings.Count), and regex matches tolerate whitespace-only
// formatting changes from terraform fmt. Note this locks the HCL surface of
// the provider blocks — it does not prove rendered resources inherit the tag
// at terraform apply, which requires a plan-json round-trip.
func assertProviderBlocksHaveDefaultTags(t *testing.T, prov string, wantBlocks int) {
	t.Helper()
	chunks := strings.Split(prov, `provider "aws" {`)
	require.Len(t, chunks, wantBlocks+1,
		"expected %d provider \"aws\" blocks, got %d. prov:\n%s",
		wantBlocks, len(chunks)-1, prov)
	defaultTagsRe := regexp.MustCompile(`default_tags\s*\{`)
	projectRe := regexp.MustCompile(`Project\s*=\s*var\.project`)
	managedByRe := regexp.MustCompile(`managed-by\s*=\s*"insideout"`)
	for i, chunk := range chunks[1:] {
		require.Regexpf(t, defaultTagsRe, chunk,
			"provider block #%d missing default_tags. chunk:\n%s", i+1, chunk)
		require.Regexpf(t, projectRe, chunk,
			"provider block #%d missing Project = var.project. chunk:\n%s", i+1, chunk)
		require.Regexpf(t, managedByRe, chunk,
			"provider block #%d missing managed-by tag. chunk:\n%s", i+1, chunk)
	}
}

func TestVpcRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		selected map[ComponentKey]bool
		want     string
	}{
		{"aws_vpc selected", map[ComponentKey]bool{KeyAWSVPC: true}, "module.aws_vpc"},
		{"gcp_vpc selected", map[ComponentKey]bool{KeyGCPVPC: true}, "module.gcp_vpc"},
		{"nothing selected defaults to aws_vpc", map[ComponentKey]bool{}, "module.aws_vpc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := vpcRef(tt.selected)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestModuleRefHelpers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		fn       func(map[ComponentKey]bool) string
		selected map[ComponentKey]bool
		want     string
	}{
		{"alb", albRef, map[ComponentKey]bool{KeyAWSALB: true}, "module.aws_alb"},
		{"waf", wafRef, map[ComponentKey]bool{KeyAWSWAF: true}, "module.aws_waf"},
		{"bastion", bastionRef, map[ComponentKey]bool{KeyAWSBastion: true}, "module.aws_bastion"},
		{"rds", rdsRef, map[ComponentKey]bool{KeyAWSRDS: true}, "module.aws_rds"},
		{"s3", s3Ref, map[ComponentKey]bool{KeyAWSS3: true}, "module.aws_s3"},
		{"opensearch", opensearchRef, map[ComponentKey]bool{KeyAWSOpenSearch: true}, "module.aws_opensearch"},
		{"sqs", sqsRef, map[ComponentKey]bool{KeyAWSSQS: true}, "module.aws_sqs"},
		{"resource eks v2", resourceRef, map[ComponentKey]bool{KeyAWSEKS: true}, "module.aws_eks"},
		{"resource ecs v2", resourceRef, map[ComponentKey]bool{KeyAWSECS: true}, "module.aws_ecs"},
		{"resource eks+ecs prefers eks", resourceRef, map[ComponentKey]bool{KeyAWSEKS: true, KeyAWSECS: true}, "module.aws_eks"},
		{"resource neither defaults to eks", resourceRef, map[ComponentKey]bool{}, "module.aws_eks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.fn(tt.selected)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestDefaultWiring_V2Keys verifies that DefaultWiring produces correct module
// references when V2 (aws_-prefixed) keys are used instead of legacy keys.
// This is the specific bug that caused Terraform failures: CloudFront referenced
// "module.alb" when the stack only contained "module.aws_alb".
func TestDefaultWiring_V2Keys(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSVPC:                  true,
		KeyAWSALB:                  true,
		KeyAWSWAF:                  true,
		KeyAWSCloudfront:           true,
		KeyAWSBastion:              true,
		KeyAWSRDS:                  true,
		KeyAWSElastiCache:          true,
		KeyAWSSQS:                  true,
		KeyAWSS3:                   true,
		KeyAWSOpenSearch:           true,
		KeyAWSBedrock:              true,
		KeyAWSBackups:              true,
		KeyAWSCloudWatchMonitoring: true,
		KeyAWSEKS:                  true,
	}
	comps := &Components{
		AWSBackups: &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{
			EC2: ptrBool(true),
			RDS: ptrBool(true),
		},
	}

	// Assert per HCL field so a key-swap mutation (e.g. alb_dns_name
	// landing in web_acl_id, or vice-versa) fails the test. The earlier
	// "join all RawHCL values then Contains-over-the-blob" form would
	// pass such a mutation because every substring still appears
	// somewhere.
	tests := []struct {
		name      string
		key       ComponentKey
		wantIn    map[string]string // HCL field -> substring that field's value must contain
		wantNotIn []string          // substrings that must not appear in any RawHCL value
	}{
		{
			name: "cloudfront references aws_alb and aws_waf",
			key:  KeyAWSCloudfront,
			wantIn: map[string]string{
				"custom_origin_domain": "module.aws_alb.alb_dns_name",
				"web_acl_id":           "module.aws_waf.web_acl_arn",
			},
			wantNotIn: []string{"module.alb.", "module.waf."},
		},
		{
			name: "cloudwatch monitoring references v2 modules",
			key:  KeyAWSCloudWatchMonitoring,
			wantIn: map[string]string{
				"instance_ids":     "module.aws_bastion.bastion_instance_id",
				"rds_instance_ids": "module.aws_rds.instance_id",
				"alb_arn_suffixes": "module.aws_alb.alb_arn_suffix",
				"sqs_queue_arns":   "module.aws_sqs.queue_arn",
			},
			wantNotIn: []string{"module.bastion.", "module.rds.", "module.alb.", "module.sqs."},
		},
		{
			name: "bedrock references v2 s3 and opensearch",
			key:  KeyAWSBedrock,
			wantIn: map[string]string{
				"s3_bucket_arn":             "module.aws_s3.bucket_arn",
				"opensearch_collection_arn": "module.aws_opensearch.collection_arn",
			},
			wantNotIn: []string{"module.s3.", "module.opensearch.", "opensearch_arn"},
		},
		{
			name: "backups references v2 rds",
			key:  KeyAWSBackups,
			wantIn: map[string]string{
				"rds_rule": "module.aws_rds.instance_arn",
			},
			wantNotIn: []string{"module.rds."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wi := DefaultWiring(selected, tt.key, comps)
			for hclKey, want := range tt.wantIn {
				got, ok := wi.RawHCL[hclKey]
				require.True(t, ok, "key=%s: missing HCL field %q", tt.key, hclKey)
				require.Contains(t, got, want,
					"key=%s field=%s: expected %q in value %q", tt.key, hclKey, want, got)
			}
			// wantNotIn still walks every value in the map to catch legacy
			// references leaking anywhere in the wiring, not just in the
			// fields pinned above.
			for _, notWant := range tt.wantNotIn {
				for hclKey, got := range wi.RawHCL {
					require.NotContains(t, got, notWant,
						"key=%s field=%s: unexpected legacy ref %q in value %q", tt.key, hclKey, notWant, got)
				}
			}
		})
	}
}

// TestDefaultWiring_BackupsDynamoDBS3 is a regression test for a bug where the
// backups module was wired with enable_dynamodb=true / enable_s3=true but no
// dynamodb_rule / s3_rule. The preset's per-service rule variables default to
// {}, so both `resource_arns` and `selection_tags` ended up empty, and AWS
// rejected the aws_backup_selection with:
//
//	InvalidParameterValueException: Either 'ListOfTags' or 'Resources' section
//	must be non-empty.
//
// The wiring must populate dynamodb_rule / s3_rule whenever the corresponding
// enable_* flag is true.
func TestDefaultWiring_BackupsDynamoDBS3(t *testing.T) {
	t.Parallel()

	t.Run("DynamoDB and S3 in stack wire explicit ARNs", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{
			KeyAWSBackups:  true,
			KeyAWSDynamoDB: true,
			KeyAWSS3:       true,
		}
		comps := &Components{
			AWSBackups: &struct {
				EC2         *bool `json:"aws_ec2,omitempty"`
				RDS         *bool `json:"aws_rds,omitempty"`
				ElastiCache *bool `json:"aws_elasticache,omitempty"`
				DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
				S3          *bool `json:"aws_s3,omitempty"`
			}{
				DynamoDB: ptrBool(true),
				S3:       ptrBool(true),
			},
		}
		wi := DefaultWiring(selected, KeyAWSBackups, comps)

		ddb, ok := wi.RawHCL["dynamodb_rule"]
		require.True(t, ok, "dynamodb_rule must be wired when DynamoDB backups are enabled")
		require.Contains(t, ddb, "module.aws_dynamodb.table_arn")

		s3, ok := wi.RawHCL["s3_rule"]
		require.True(t, ok, "s3_rule must be wired when S3 backups are enabled")
		require.Contains(t, s3, "module.aws_s3.bucket_arn")
	})

	t.Run("DynamoDB enabled without module falls back to backup=true tag", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{KeyAWSBackups: true}
		comps := &Components{
			AWSBackups: &struct {
				EC2         *bool `json:"aws_ec2,omitempty"`
				RDS         *bool `json:"aws_rds,omitempty"`
				ElastiCache *bool `json:"aws_elasticache,omitempty"`
				DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
				S3          *bool `json:"aws_s3,omitempty"`
			}{
				DynamoDB: ptrBool(true),
			},
		}
		wi := DefaultWiring(selected, KeyAWSBackups, comps)
		ddb, ok := wi.RawHCL["dynamodb_rule"]
		require.True(t, ok, "dynamodb_rule must still be wired (tag fallback) when DynamoDB module is absent")
		require.Contains(t, ddb, `key = "backup"`)
		require.Contains(t, ddb, `value = "true"`)
	})

	t.Run("backups disabled leaves dynamodb_rule and s3_rule unset", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{KeyAWSBackups: true}
		comps := &Components{
			AWSBackups: &struct {
				EC2         *bool `json:"aws_ec2,omitempty"`
				RDS         *bool `json:"aws_rds,omitempty"`
				ElastiCache *bool `json:"aws_elasticache,omitempty"`
				DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
				S3          *bool `json:"aws_s3,omitempty"`
			}{
				EC2: ptrBool(true),
			},
		}
		wi := DefaultWiring(selected, KeyAWSBackups, comps)
		_, hasDdb := wi.RawHCL["dynamodb_rule"]
		_, hasS3 := wi.RawHCL["s3_rule"]
		require.False(t, hasDdb, "dynamodb_rule must not be wired when DynamoDB backups are disabled")
		require.False(t, hasS3, "s3_rule must not be wired when S3 backups are disabled")
	})

	t.Run("a legacy Backups session that's been Normalized wires V2 modules", func(t *testing.T) {
		t.Parallel()
		// Phase 3b collapsed both the legacy Backups-field fallback and the
		// AWS→legacy normalization switch. Legacy Components fields must
		// Normalize before reaching the composer; reliable's composeradapter
		// does this in production.
		selected := map[ComponentKey]bool{
			KeyAWSBackups:  true,
			KeyAWSDynamoDB: true,
			KeyAWSS3:       true,
		}
		comps := &Components{
			Cloud: "AWS",
			Backups: &struct {
				EC2         *bool `json:"ec2,omitempty"`
				Rds         *bool `json:"rds,omitempty"`
				ElastiCache *bool `json:"elasticache,omitempty"`
				DynamoDB    *bool `json:"dynamodb,omitempty"`
				S3          *bool `json:"s3,omitempty"`
			}{
				DynamoDB: ptrBool(true),
				S3:       ptrBool(true),
			},
		}
		comps.Normalize()
		wi := DefaultWiring(selected, KeyAWSBackups, comps)
		require.Contains(t, wi.RawHCL["dynamodb_rule"], "module.aws_dynamodb.table_arn")
		require.Contains(t, wi.RawHCL["s3_rule"], "module.aws_s3.bucket_arn")
	})

	t.Run("RDS enabled without Postgres falls back to backup=true tag", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{KeyAWSBackups: true}
		comps := &Components{
			AWSBackups: &struct {
				EC2         *bool `json:"aws_ec2,omitempty"`
				RDS         *bool `json:"aws_rds,omitempty"`
				ElastiCache *bool `json:"aws_elasticache,omitempty"`
				DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
				S3          *bool `json:"aws_s3,omitempty"`
			}{
				RDS: ptrBool(true),
			},
		}
		wi := DefaultWiring(selected, KeyAWSBackups, comps)
		rds, ok := wi.RawHCL["rds_rule"]
		require.True(t, ok, "rds_rule must still be wired (tag fallback) when RDS module is absent")
		require.Contains(t, rds, `key = "backup"`)
		require.Contains(t, rds, `value = "true"`)
		require.NotContains(t, rds, "instance_arn", "fallback must not reference an absent RDS module")
	})

	t.Run("S3 enabled without module falls back to backup=true tag", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{KeyAWSBackups: true}
		comps := &Components{
			AWSBackups: &struct {
				EC2         *bool `json:"aws_ec2,omitempty"`
				RDS         *bool `json:"aws_rds,omitempty"`
				ElastiCache *bool `json:"aws_elasticache,omitempty"`
				DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
				S3          *bool `json:"aws_s3,omitempty"`
			}{
				S3: ptrBool(true),
			},
		}
		wi := DefaultWiring(selected, KeyAWSBackups, comps)
		s3, ok := wi.RawHCL["s3_rule"]
		require.True(t, ok, "s3_rule must still be wired (tag fallback) when S3 module is absent")
		require.Contains(t, s3, `key = "backup"`)
		require.Contains(t, s3, `value = "true"`)
		require.NotContains(t, s3, "bucket_arn", "fallback must not reference an absent S3 module")
	})

	// Regression guard for the behavior change in this PR: rds_rule used to be
	// wired whenever Postgres was in-stack, regardless of whether RDS backups
	// were enabled. It's now gated on enable_rds. The preset's
	// aws_backup_selection.rds is for_each-gated on var.enable_rds, so the
	// previously-emitted rds_rule was dead — this test locks in that it is no
	// longer emitted at all when RDS backups are off.
	t.Run("RDS disabled with Postgres in-stack leaves rds_rule unset", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{
			KeyAWSBackups: true,
			KeyAWSRDS:     true,
		}
		comps := &Components{
			AWSBackups: &struct {
				EC2         *bool `json:"aws_ec2,omitempty"`
				RDS         *bool `json:"aws_rds,omitempty"`
				ElastiCache *bool `json:"aws_elasticache,omitempty"`
				DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
				S3          *bool `json:"aws_s3,omitempty"`
			}{
				EC2: ptrBool(true),
			},
		}
		wi := DefaultWiring(selected, KeyAWSBackups, comps)
		_, hasRds := wi.RawHCL["rds_rule"]
		require.False(t, hasRds, "rds_rule must not be wired when RDS backups are disabled, even with Postgres in stack")
	})
}

// TestComposeStack_V2KitchenSink is the V2 equivalent of TestComposeStack_KitchenSink.
// It uses aws_-prefixed keys exclusively and verifies that all cross-module
// references use the correct V2 module names (e.g., module.aws_alb, not module.alb).
func TestComposeStack_V2KitchenSink(t *testing.T) {
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEKS,
		KeyAWSBastion,
		KeyAWSALB,
		KeyAWSRDS,
		KeyAWSElastiCache,
		KeyAWSWAF,
		KeyAWSCloudfront,
		KeyAWSCloudWatchLogs,
		KeyAWSSQS,
		KeyAWSCloudWatchMonitoring,
		KeyAWSGitHubActions,
	}

	comps := awsKitchenSinkCompsV2()
	cfg := awsKitchenSinkCfgV2()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        comps,
		Cfg:          cfg,
		Project:      "demo",
		Region:       "us-west-2",
	})
	require.NoError(t, err, "ComposeStack with V2 keys should succeed")

	mainTF := string(out["/main.tf"])

	// All module blocks should use V2 names
	re := func(p string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^\s*source\s*=\s*"` + regexp.QuoteMeta(p) + `"\s*$`)
	}
	require.Regexp(t, re("./modules/vpc"), mainTF)
	require.Regexp(t, re("./modules/eks"), mainTF)
	require.Regexp(t, re("./modules/bastion"), mainTF)
	require.Regexp(t, re("./modules/alb"), mainTF)
	require.Regexp(t, re("./modules/rds"), mainTF)
	require.Regexp(t, re("./modules/cloudfront"), mainTF)

	// Cross-module wiring must use V2 module names
	// EKS ← VPC
	require.Contains(t, mainTF, "module.aws_vpc.vpc_id")
	require.Contains(t, mainTF, "module.aws_vpc.private_subnet_ids")
	require.Contains(t, mainTF, "module.aws_vpc.public_subnet_ids")

	// ALB ← VPC
	require.Contains(t, mainTF, "module.aws_vpc.vpc_id")

	// CloudFront ← ALB + WAF (the original bug)
	require.Contains(t, mainTF, "module.aws_alb.alb_dns_name")
	require.Contains(t, mainTF, "module.aws_waf.web_acl_arn")

	// WAF wiring constants (must fire even with V2 key)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*scope\s*=\s*"CLOUDFRONT"\s*$`), mainTF)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*region\s*=\s*"us-east-1"\s*$`), mainTF)
	// WAF providers override (us_east_1 alias required for CloudFront-scoped WAF)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*providers\s+=\s+\{`), mainTF)
	require.Contains(t, mainTF, "aws = aws")
	require.Contains(t, mainTF, "aws.us_east_1 = aws.us_east_1")

	// Providers file should declare default + us_east_1 alias
	require.Contains(t, out, "/providers.tf")
	prov := string(out["/providers.tf"])
	require.Contains(t, prov, `terraform {`)
	require.Contains(t, prov, `required_providers`)
	require.Contains(t, prov, `provider "aws" {`)
	require.Contains(t, prov, `alias  = "us_east_1"`)
	require.Contains(t, prov, `region = "us-east-1"`)
	// Verify each provider block independently carries default_tags with
	// Project = var.project — the #1112 safety net. Split-per-block proves
	// placement (a regression dropping default_tags from just one block
	// would otherwise pass a global substring count).
	assertProviderBlocksHaveDefaultTags(t, prov, 2)

	// Monitoring ← bastion, RDS, ALB, SQS
	require.Contains(t, mainTF, "module.aws_bastion.bastion_instance_id")
	require.Contains(t, mainTF, "module.aws_rds.instance_id")
	require.Contains(t, mainTF, "module.aws_alb.alb_arn_suffix")
	require.Contains(t, mainTF, "module.aws_sqs.queue_arn")

	// Must NOT contain legacy module references
	require.NotContains(t, mainTF, "module.alb.")
	require.NotContains(t, mainTF, "module.waf.")
	require.NotContains(t, mainTF, "module.bastion.")
	require.NotContains(t, mainTF, "module.rds.")
	require.NotContains(t, mainTF, "module.sqs.")
	require.NotContains(t, mainTF, "module.vpc.")
}

func TestComposeStack_KitchenSink(t *testing.T) {
	// Select a broad set of modules to exercise wiring. Uses prefixed keys
	// plus the polymorphic KeyResource (EKS control plane) and KeyEC2 (EKS
	// managed node group) — those stay until Phase 4 renames them to
	// unambiguous `KeyAWSEKSControlPlane` / `KeyAWSEKSNodeGroup`.
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEKS, // EKS control plane
		KeyEC2,    // EKS node group (polymorphic; KeyAWSEKSNodeGroup lands in Phase 4)
		KeyAWSBastion,
		KeyAWSALB,
		KeyAWSRDS,
		KeyAWSElastiCache,
		KeyAWSWAF,
		KeyAWSCloudfront,
		KeyAWSBackups,
		KeyAWSCloudWatchLogs,
		KeyAWSSQS,
		KeyAWSCloudWatchMonitoring,
		KeyAWSGitHubActions,
	}

	// Enable backups for EC2/EBS + RDS to trigger wiring. Cfg sets
	// RDS.ReadReplicas="2" to exercise the read-replicas mapper branch
	// that the V2 kitchen-sink leaves unset.
	comps := awsKitchenSinkCompsV2()
	cfg := awsKitchenSinkCfgWithReadReplicas()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        comps,
		Cfg:          cfg,
		Project:      "demo",
		Region:       "us-west-2",
	})
	require.NoError(t, err, "ComposeStack should succeed")

	// Save generated files if requested (reuses helper from compose_vm_test.go).
	if writeOutDir != "" {
		writeOutputs(t, out, writeOutDir)
	} else if dir := os.Getenv("SAVE_OUTPUT_DIR"); dir != "" {
		writeOutputs(t, out, dir)
	}

	// Split assertions into subtests grouped by wiring family. Subtests
	// share the single ComposeStack output via closure and MUST NOT call
	// t.Parallel() — they read `out` without coordination and re-running
	// ComposeStack per subtest would multiply runtime. The split localises
	// failures: a regression in one wiring family no longer masks the rest.
	mainTF := string(out["/main.tf"])

	t.Run("root_files", func(t *testing.T) {
		require.Contains(t, out, "/main.tf")
		require.Contains(t, out, "/variables.tf")
		require.Contains(t, out, "/.terraform-version")
	})

	t.Run("module_sources", func(t *testing.T) {
		re := func(p string) *regexp.Regexp {
			return regexp.MustCompile(`(?m)^\s*source\s*=\s*"` + regexp.QuoteMeta(p) + `"\s*$`)
		}
		for _, src := range []string{
			"./modules/vpc", "./modules/eks", "./modules/eks_nodegroup",
			"./modules/bastion", "./modules/alb", "./modules/rds",
			"./modules/elasticache", "./modules/waf", "./modules/cloudfront",
			"./modules/backups", "./modules/cloudwatchlogs", "./modules/sqs",
			"./modules/cloudwatchmonitoring", "./modules/githubactions",
		} {
			require.Regexp(t, re(src), mainTF, "missing module source %q", src)
		}
	})

	t.Run("wiring/eks", func(t *testing.T) {
		require.Contains(t, mainTF, `vpc_id                    = module.aws_vpc.vpc_id`)
		require.Contains(t, mainTF, `private_subnet_ids        = module.aws_vpc.private_subnet_ids`)
		require.Contains(t, mainTF, `public_subnet_ids         = module.aws_vpc.public_subnet_ids`)
		require.Contains(t, mainTF, `cluster_enabled_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]`)
	})

	t.Run("wiring/nodegroup", func(t *testing.T) {
		require.Contains(t, mainTF, `cluster_name   = module.aws_eks.cluster_name`)
		require.Contains(t, mainTF, `subnet_ids     = module.aws_vpc.private_subnet_ids`)
	})

	t.Run("wiring/alb", func(t *testing.T) {
		require.Contains(t, mainTF, `vpc_id            = module.aws_vpc.vpc_id`)
		require.Contains(t, mainTF, `public_subnet_ids = module.aws_vpc.public_subnet_ids`)
	})

	t.Run("wiring/bastion", func(t *testing.T) {
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*subnet_id\s+=\s+module\.aws_vpc\.public_subnet_ids\[0\]\s*$`), mainTF)
	})

	t.Run("wiring/postgres", func(t *testing.T) {
		require.Contains(t, mainTF, `vpc_id                  = module.aws_vpc.vpc_id`)
		require.Contains(t, mainTF, `subnet_ids              = module.aws_vpc.private_subnet_ids`)
		require.Contains(t, mainTF, `enable_cloudwatch_logs  = true`)
		require.Contains(t, mainTF, `cloudwatch_logs_exports = ["postgresql", "upgrade"]`)
		require.Contains(t, mainTF, `skip_final_snapshot     = true`)
		require.Contains(t, mainTF, `apply_immediately       = true`)
	})

	t.Run("wiring/elasticache", func(t *testing.T) {
		require.Contains(t, mainTF, `vpc_id           = module.aws_vpc.vpc_id`)
		require.Contains(t, mainTF, `cache_subnet_ids = module.aws_vpc.private_subnet_ids`)
	})

	t.Run("wiring/cloudfront", func(t *testing.T) {
		require.Contains(t, mainTF, `origin_type          = "http"`)
		require.Contains(t, mainTF, `custom_origin_domain = module.aws_alb.alb_dns_name`)
		require.Contains(t, mainTF, `web_acl_id           = module.aws_waf.web_acl_arn`)
	})

	t.Run("wiring/waf_providers", func(t *testing.T) {
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*scope\s*=\s*"CLOUDFRONT"\s*$`), mainTF)
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*region\s*=\s*"us-east-1"\s*$`), mainTF)
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*providers\s+=\s+\{`), mainTF)
		require.Contains(t, mainTF, "aws = aws")
		require.Contains(t, mainTF, "aws.us_east_1 = aws.us_east_1")
	})

	t.Run("wiring/monitoring", func(t *testing.T) {
		require.Contains(t, mainTF, `instance_ids     = [module.aws_bastion.bastion_instance_id]`)
		require.Contains(t, mainTF, `rds_instance_ids = [module.aws_rds.instance_id]`)
		require.Contains(t, mainTF, `alb_arn_suffixes = [module.aws_alb.alb_arn_suffix]`)
		require.Contains(t, mainTF, `sqs_queue_arns   = [module.aws_sqs.queue_arn]`)
	})

	t.Run("wiring/backups", func(t *testing.T) {
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*enable_ec2_ebs\s*=\s*true\s*$`), mainTF)
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*enable_rds\s*=\s*true\s*$`), mainTF)
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*enable_dynamodb\s*=\s*false\s*$`), mainTF)
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*enable_s3\s*=\s*false\s*$`), mainTF)
		require.Contains(t, mainTF, `selection_tags = [{ type = "STRINGEQUALS", key = "backup", value = "true" }]`)
		require.Contains(t, mainTF, `resource_arns = [module.aws_rds.instance_arn]`)
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*default_rule\s*=\s*var\.aws_backups_default_rule\s*$`), mainTF)
	})

	t.Run("tfvars/ec2_namespacing", func(t *testing.T) {
		require.Contains(t, out, "/ec2.auto.tfvars")
		ec2Tf := string(out["/ec2.auto.tfvars"])
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*ec2_project\s*=\s*"demo"\s*$`), ec2Tf)
		require.Regexp(t, regexp.MustCompile(`(?m)^\s*ec2_region\s*=\s*"us-west-2"\s*$`), ec2Tf)
	})

	t.Run("variables_tf", func(t *testing.T) {
		varsTF := string(out["/variables.tf"])
		require.Contains(t, varsTF, `variable "ec2_project"`)
		require.Contains(t, varsTF, `variable "ec2_region"`)
		require.Contains(t, varsTF, `variable "project"`)
		require.Contains(t, varsTF, `variable "region"`)
	})

	t.Run("providers_tf", func(t *testing.T) {
		require.Contains(t, out, "/providers.tf")
		prov := string(out["/providers.tf"])
		require.Contains(t, prov, `terraform {`)
		require.Contains(t, prov, `required_providers`)
		require.Contains(t, prov, `provider "aws" {`)
		require.Contains(t, prov, `alias  = "us_east_1"`)
		require.Contains(t, prov, `region = "us-east-1"`)
		// WAF is selected here so both default + us_east_1 blocks render;
		// each must independently carry the #1112 default_tags safety net.
		assertProviderBlocksHaveDefaultTags(t, prov, 2)
	})
}

func ptrBool(b bool) *bool       { return &b }
func ptrString(s string) *string { return &s }

func TestDefaultWiring_LambdaPublicVPC(t *testing.T) {
	t.Run("Lambda skips VPC wiring when VPC is Public", func(t *testing.T) {
		selected := map[ComponentKey]bool{
			KeyAWSVPC:    true,
			KeyAWSLambda: true,
		}
		comps := &Components{AWSVPC: "Public VPC", AWSLambda: ptrBool(true)}
		wi := DefaultWiring(selected, KeyAWSLambda, comps)
		_, hasEnableVPC := wi.RawHCL["enable_vpc"]
		_, hasSubnetIDs := wi.RawHCL["subnet_ids"]
		require.False(t, hasEnableVPC, "Public VPC: Lambda should not have enable_vpc")
		require.False(t, hasSubnetIDs, "Public VPC: Lambda should not have subnet_ids")
	})

	t.Run("Lambda skips VPC wiring for a legacy session that's been Normalized", func(t *testing.T) {
		// Legacy Components struct fields must be Normalize()d before reaching
		// the composer; reliable's composeradapter does this for us in
		// production. After Normalize, Lambda/VPC live on AWSLambda/AWSVPC
		// and the selected map uses prefixed keys.
		selected := map[ComponentKey]bool{
			KeyAWSVPC:    true,
			KeyAWSLambda: true,
		}
		comps := &Components{Cloud: "AWS", VPC: "Public VPC", Lambda: ptrBool(true)}
		comps.Normalize()
		// Pin that Normalize actually promoted the legacy fields — without
		// these the assertion below passes vacuously (AWSLambda stays nil →
		// the KeyAWSLambda wiring arm short-circuits before ever looking at
		// the VPC).
		require.Equal(t, "Public VPC", comps.AWSVPC, "Normalize must promote legacy VPC to AWSVPC")
		require.NotNil(t, comps.AWSLambda, "Normalize must promote legacy Lambda to AWSLambda")
		wi := DefaultWiring(selected, KeyAWSLambda, comps)
		_, hasEnableVPC := wi.RawHCL["enable_vpc"]
		require.False(t, hasEnableVPC, "Legacy Public VPC (after Normalize): Lambda should not have enable_vpc")
	})

	t.Run("Lambda wires VPC when VPC is Private", func(t *testing.T) {
		selected := map[ComponentKey]bool{
			KeyAWSVPC:    true,
			KeyAWSLambda: true,
		}
		comps := &Components{AWSVPC: "Private VPC", AWSLambda: ptrBool(true)}
		wi := DefaultWiring(selected, KeyAWSLambda, comps)
		require.Equal(t, "true", wi.RawHCL["enable_vpc"])
		require.Contains(t, wi.RawHCL["subnet_ids"], "private_subnet_ids")
	})

	t.Run("Lambda wires VPC when VPC type is empty (defaults to Private)", func(t *testing.T) {
		selected := map[ComponentKey]bool{
			KeyAWSVPC:    true,
			KeyAWSLambda: true,
		}
		comps := &Components{AWSVPC: "", AWSLambda: ptrBool(true)}
		wi := DefaultWiring(selected, KeyAWSLambda, comps)
		require.Equal(t, "true", wi.RawHCL["enable_vpc"])
		require.Contains(t, wi.RawHCL["subnet_ids"], "private_subnet_ids")
	})
}

func TestComposeStack_LambdaPublicVPC(t *testing.T) {
	// Replicate the exact stack from prod session sess_v2_fm8xQVfcLAYA
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSLambda,
		KeyAWSS3,
		KeyAWSCloudWatchLogs,
		KeyAWSCloudWatchMonitoring,
	}
	comps := &Components{
		Cloud:        "AWS",
		Architecture: "Serverless",
		AWSVPC:       "Public VPC",
		AWSLambda:    ptrBool(true),
		AWSS3:        ptrBool(true),
	}
	cfg := &Config{Region: "us-west-2"}

	client := newTestClient()
	files, err := client.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        comps,
		Cfg:          cfg,
		Project:      "test-lambda-public-vpc",
		Region:       "us-west-2",
	})
	require.NoError(t, err)

	mainTF := string(files["/main.tf"])
	varsTF := string(files["/variables.tf"])
	lambdaTfvars := string(files["/aws_lambda.auto.tfvars"])

	// Root variables.tf should NOT declare vpc_id for Lambda in Public VPC
	require.NotContains(t, varsTF, "aws_lambda_vpc_id",
		"should not declare aws_lambda_vpc_id variable in Public VPC")
	require.NotContains(t, varsTF, "aws_lambda_subnet_ids",
		"should not declare aws_lambda_subnet_ids variable in Public VPC")

	// Lambda tfvars should NOT contain vpc_id
	require.NotContains(t, lambdaTfvars, "vpc_id",
		"Lambda tfvars should not contain vpc_id in Public VPC")

	// Lambda module should NOT have enable_vpc or subnet_ids in Public VPC
	require.NotContains(t, mainTF, "enable_vpc", "Lambda should not wire VPC in Public VPC")
	require.NotContains(t, mainTF, "private_subnet_ids", "Lambda should not reference private subnets in Public VPC")

	// VPC module should still exist
	require.Contains(t, mainTF, `module "aws_vpc"`)
	// Lambda module should exist
	require.Contains(t, mainTF, `module "aws_lambda"`)
}

// TestComposeStack_AWS_ValidHCL validates that all generated terraform files are valid HCL.
// This is a lighter-weight check than running `terraform validate` which requires network access.
func TestComposeStack_AWS_ValidHCL(t *testing.T) {
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyEC2, // EKS node group (polymorphic)
		KeyAWSRDS,
		KeyAWSS3,
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack(aws) should succeed")

	// Validate all .tf files parse as valid HCL
	for name, content := range out {
		if strings.HasSuffix(name, ".tf") {
			err := parseHCL(name, content)
			require.NoError(t, err, "AWS composed file %s should be valid HCL", name)
		}
	}
}

// TestComposeStack_TFVarsMatchVariables verifies that every key in .auto.tfvars files
// has a corresponding declaration in variables.tf. This test catches naming mismatches
// like writing "project" in .auto.tfvars when variables.tf declares "ec2_project".
func TestComposeStack_TFVarsMatchVariables(t *testing.T) {
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEKS,
		KeyEC2, // EKS node group (polymorphic)
		KeyAWSBastion,
		KeyAWSALB,
		KeyAWSRDS,
		KeyAWSElastiCache,
		KeyAWSS3,
		KeyAWSCloudWatchLogs,
		KeyAWSSQS,
	}

	comps := &Components{
		AWSElastiCache: ptrBool(true),
	}
	cfg := &Config{
		Region: "us-west-2",
		RDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: "db.m7i.large", StorageSize: "20"},
		SQS: &struct {
			Type              string `json:"type,omitempty"`
			VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
		}{Type: "FIFO"},
		CloudWatchLogs: &struct {
			RetentionDays int `json:"retentionDays,omitempty"`
		}{RetentionDays: 90},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        comps,
		Cfg:          cfg,
		Project:      "test",
		Region:       "us-west-2",
	})
	require.NoError(t, err, "ComposeStack should succeed")

	// Extract declared variable names from variables.tf
	varsTF := string(out["/variables.tf"])
	declaredVars := map[string]bool{}
	varDeclRe := regexp.MustCompile(`variable\s+"([^"]+)"`)
	for _, match := range varDeclRe.FindAllStringSubmatch(varsTF, -1) {
		declaredVars[match[1]] = true
	}

	// Check all .auto.tfvars files
	tfvarAssignRe := regexp.MustCompile(`(?m)^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*=`)
	for name, content := range out {
		if !strings.HasSuffix(name, ".auto.tfvars") {
			continue
		}
		for _, match := range tfvarAssignRe.FindAllStringSubmatch(string(content), -1) {
			varName := match[1]
			require.True(t, declaredVars[varName],
				"tfvars file %s sets %q but no matching variable declaration in variables.tf (declared: %v)",
				name, varName, sortedKeys(declaredVars))
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestComposeStack_TerraformInit writes the composed stack to a temp directory
// and runs `terraform init -backend=false` on it. This verifies that:
// - All .tf files are syntactically valid
// - Variable declarations are consistent
// - Provider configurations are correct
// Skipped when terraform CLI is not available.
//
// Note: We don't run `terraform validate` because the preset modules may have
// validation condition bugs (e.g., contains() with null values) that are
// separate from the variable naming concerns tested here. The
// TestComposeStack_TFVarsMatchVariables test catches naming mismatches directly.
func TestComposeStack_TerraformInit(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform CLI not available, skipping validation test")
	}

	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEKS,
		KeyEC2, // EKS node group (polymorphic)
		KeyAWSRDS,
		KeyAWSS3,
		KeyAWSCloudWatchLogs,
	}

	comps := &Components{}
	cfg := &Config{
		Region: "us-east-1",
		RDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: "db.m7i.large", StorageSize: "20"},
		CloudWatchLogs: &struct {
			RetentionDays int `json:"retentionDays,omitempty"`
		}{RetentionDays: 90},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        comps,
		Cfg:          cfg,
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack should succeed")

	// Write to temp dir
	dir := t.TempDir()
	writeBundle(t, dir, out)

	// terraform init -backend=false (no remote state needed)
	initCmd := exec.Command("terraform", "init", "-backend=false", "-no-color")
	initCmd.Dir = dir
	initOutput, err := initCmd.CombinedOutput()
	require.NoError(t, err, "terraform init should succeed:\n%s", string(initOutput))

	t.Logf("terraform init passed in %s", dir)
}

// TestComposeStack_ConflictingCompute verifies that ComposeStack returns an error
// when incompatible compute components (e.g., Lambda + EKS) are selected.
func TestComposeStack_ConflictingCompute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cloud  string
		keys   []ComponentKey
		errMsg string // cloud-specific error substring
	}{
		{
			name:   "AWS Lambda + AWS EKS (prefixed)",
			cloud:  "aws",
			keys:   []ComponentKey{KeyAWSLambda, KeyAWSEKS, KeyAWSVPC},
			errMsg: "incompatible AWS compute",
		},
		{
			name:   "AWS Lambda + EC2 node group (implicit EKS dependency)",
			cloud:  "aws",
			keys:   []ComponentKey{KeyAWSLambda, KeyEC2, KeyAWSVPC},
			errMsg: "incompatible AWS compute",
		},
		{
			name:   "GCP Cloud Functions + GKE",
			cloud:  "gcp",
			keys:   []ComponentKey{KeyGCPCloudFunctions, KeyGCPGKE, KeyGCPVPC},
			errMsg: "incompatible GCP compute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient()
			_, err := c.ComposeStack(ComposeStackOpts{
				Cloud:        tt.cloud,
				SelectedKeys: tt.keys,
				Comps:        &Components{},
				Cfg:          &Config{Region: "us-east-1"},
				Project:      "test",
				Region:       "us-east-1",
			})
			require.Error(t, err, "ComposeStack should reject conflicting compute keys")
			require.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

// TestComposeStack_LambdaIncludesPlaceholderZip verifies that a Lambda stack includes
// placeholder.zip in the composed output, fixing the "no such file or directory" error.
func TestComposeStack_LambdaIncludesPlaceholderZip(t *testing.T) {
	t.Parallel()

	trueVal := true
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSLambda},
		Comps:        &Components{AWSLambda: &trueVal},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack with Lambda should succeed")

	// placeholder.zip must exist in the Lambda module directory
	var found bool
	for path := range out {
		if strings.HasSuffix(path, "placeholder.zip") {
			found = true
			require.NotEmpty(t, out[path], "placeholder.zip should not be empty")
			break
		}
	}
	require.True(t, found, "placeholder.zip should be in composed Lambda output, got files: %v", fileKeys(out))
}

func fileKeys(f Files) []string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	return keys
}

// TestComposeStack_MonolithEC2 validates composition of a "monolith" architecture:
// standalone EC2 instance + VPC, no EKS/ECS container orchestration.
// This is the simplest AWS compute pattern — a single VM with cloud-init,
// custom security group ports, and SSH access.
func TestComposeStack_MonolithEC2(t *testing.T) {
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEC2,
	}

	cfg := &Config{
		Region: "us-east-1",
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{
			UserData:           "#!/bin/bash\napt-get update && apt-get install -y nodejs",
			DiskSizePerServer:  "32",
			CustomIngressPorts: []int{18789},
			SSHPublicKey:       "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest",
		},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        &Components{Architecture: "Monolith", AWSEC2: "Intel"},
		Cfg:          cfg,
		Project:      "openclaw",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack(monolith) should succeed")

	// Root files
	require.Contains(t, out, "/main.tf")
	require.Contains(t, out, "/variables.tf")
	require.Contains(t, out, "/.terraform-version")

	mainTF := string(out["/main.tf"])

	// Module sources — standalone EC2 (modules/ec2), NOT eks_nodegroup
	re := func(p string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^\s*source\s*=\s*"` + regexp.QuoteMeta(p) + `"\s*$`)
	}
	require.Regexp(t, re("./modules/vpc"), mainTF, "should have VPC module (aws_vpc uses modules/vpc path)")
	require.Regexp(t, re("./modules/ec2"), mainTF, "should have standalone EC2 module")

	// Must NOT contain EKS/ECS modules
	require.NotRegexp(t, re("./modules/eks_nodegroup"), mainTF, "monolith should not include EKS node group")
	require.NotRegexp(t, re("./modules/eks"), mainTF, "monolith should not include EKS cluster")

	// Exactly one VPC module (no duplicate)
	vpcModuleCount := regexp.MustCompile(`(?m)^\s*module\s+"[^"]*vpc[^"]*"\s*\{`).FindAllStringIndex(mainTF, -1)
	require.Len(t, vpcModuleCount, 1, "should have exactly one VPC module, not two")

	// Cross-module wiring: EC2 ← aws_vpc (whitespace-agnostic)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*vpc_id\s*=\s*module\.aws_vpc\.vpc_id\s*$`), mainTF,
		"EC2 should wire vpc_id from aws_vpc")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*subnet_id\s*=\s*module\.aws_vpc\.private_subnet_ids\[0\]\s*$`), mainTF,
		"EC2 with non-Public VPC should wire subnet_id to private_subnet_ids")
	require.NotContains(t, mainTF, "associate_public_ip",
		"EC2 with non-Public VPC should NOT set associate_public_ip")

	// Tfvars should contain EC2-specific config
	require.Contains(t, out, "/aws_ec2.auto.tfvars")
	ec2Tfvars := string(out["/aws_ec2.auto.tfvars"])
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*aws_ec2_project\s*=\s*"openclaw"\s*$`), ec2Tfvars,
		"should contain namespaced project")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*aws_ec2_region\s*=\s*"us-east-1"\s*$`), ec2Tfvars,
		"should contain namespaced region")
	require.Contains(t, ec2Tfvars, "apt-get update",
		"user_data should contain the provided cloud-init script")
	require.Contains(t, ec2Tfvars, "18789",
		"custom_ingress_ports should contain port 18789")
	require.Contains(t, ec2Tfvars, "ssh-ed25519",
		"ssh_public_key should contain the provided key")

	// Standalone EC2 must NOT wire cluster_name (that's for EKS node groups)
	require.NotContains(t, mainTF, "cluster_name",
		"standalone EC2 should not wire cluster_name (that's EKS node group)")

	// Variables.tf should declare namespaced entries
	varsTF := string(out["/variables.tf"])
	require.Contains(t, varsTF, `variable "aws_ec2_project"`)
	require.Contains(t, varsTF, `variable "aws_ec2_region"`)

	// NoWAF path: only the default provider "aws" block should render,
	// and it must carry the #1112 default_tags safety net. A regression
	// dropping default_tags from the default block would pass the V2/legacy
	// WAF tests if inadvertently doubled on the alias — only this NoWAF
	// assertion catches that directly.
	require.Contains(t, out, "/providers.tf")
	assertProviderBlocksHaveDefaultTags(t, string(out["/providers.tf"]), 1)

	// Validate all .tf files parse as valid HCL
	for name, content := range out {
		if strings.HasSuffix(name, ".tf") {
			err := parseHCL(name, content)
			require.NoError(t, err, "monolith composed file %s should be valid HCL", name)
		}
	}

	// Save generated files if requested
	if writeOutDir != "" {
		writeOutputs(t, out, writeOutDir)
	} else if dir := os.Getenv("SAVE_OUTPUT_DIR"); dir != "" {
		writeOutputs(t, out, dir)
	}
}

// TestComposeStack_MonolithEC2_PublicVPC validates that Public VPC sets
// associate_public_ip = true and uses public_subnet_ids.
func TestComposeStack_MonolithEC2_PublicVPC(t *testing.T) {
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEC2,
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        &Components{Architecture: "Monolith", AWSEC2: "Intel", AWSVPC: "Public VPC"},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack(monolith-public) should succeed")

	mainTF := string(out["/main.tf"])

	// Public VPC: public subnet + associate_public_ip
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*subnet_id\s*=\s*module\.aws_vpc\.public_subnet_ids\[0\]\s*$`), mainTF,
		"Public VPC should use public_subnet_ids")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*associate_public_ip\s*=\s*true\s*$`), mainTF,
		"Public VPC should set associate_public_ip = true")
	require.NotRegexp(t, regexp.MustCompile(`(?m)^\s*subnet_id\s*=\s*module\.aws_vpc\.private_subnet_ids`), mainTF,
		"Public VPC should NOT reference private_subnet_ids for subnet_id")

	// Exactly one VPC module
	vpcModuleCount := regexp.MustCompile(`(?m)^\s*module\s+"[^"]*vpc[^"]*"\s*\{`).FindAllStringIndex(mainTF, -1)
	require.Len(t, vpcModuleCount, 1, "should have exactly one VPC module")

	// Validate HCL syntax
	for name, content := range out {
		if strings.HasSuffix(name, ".tf") {
			err := parseHCL(name, content)
			require.NoError(t, err, "public VPC composed file %s should be valid HCL", name)
		}
	}
}

func TestComposeStack_LegacyStandaloneEC2Lambda(t *testing.T) {
	trueVal := true
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEC2,
		KeyAWSLambda,
		KeyAWSAPIGateway,
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:                   "aws",
		SelectedKeys:            selected,
		AllowLegacyMixedCompute: true,
		Comps:                   &Components{AWSVPC: "Private VPC", AWSEC2: "Intel", AWSLambda: &trueVal},
		Cfg:                     &Config{Region: "us-east-1"},
		Project:                 "legacy-mixed",
		Region:                  "us-east-1",
	})
	require.NoError(t, err, "ComposeStack(legacy standalone ec2 + lambda) should succeed")

	mainTF := string(out["/main.tf"])
	require.Contains(t, mainTF, `module "aws_ec2"`, "should include standalone EC2 module")
	require.Contains(t, mainTF, `module "aws_lambda"`, "should include lambda module")
	require.Contains(t, mainTF, `module "aws_apigateway"`, "should include API Gateway module")
	require.NotContains(t, mainTF, `module "aws_eks"`, "should not include EKS module")
	require.NotContains(t, mainTF, `module "resource"`, "should not include legacy resource module")

	for name, content := range out {
		if strings.HasSuffix(name, ".tf") {
			err := parseHCL(name, content)
			require.NoError(t, err, "legacy mixed composed file %s should be valid HCL", name)
		}
	}
}

// TestComposeStack_OpenClawDemo verifies the composition engine output for the
// OpenClaw demo: a Monolith EC2 with cloud-init, SSH access, and NO exposed ports.
// The Gateway binds to loopback (default) and is accessed via SSH port forwarding.
// Note: this tests the composition engine given specific inputs, not LLM output.
func TestComposeStack_OpenClawDemo(t *testing.T) {
	t.Parallel()
	cloudInitScript := `#!/bin/bash
set -euo pipefail
apt-get update -y
apt-get install -y nodejs docker.io
curl -fsSL https://openclaw.ai/install.sh | bash
cat > /etc/systemd/system/openclaw-gateway.service <<'UNIT'
[Unit]
Description=OpenClaw Gateway
[Service]
ExecStart=/home/openclaw/.openclaw/bin/openclaw-gateway
[Install]
WantedBy=multi-user.target
UNIT
systemctl enable --now openclaw-gateway.service
touch /var/log/openclaw-init-complete`

	sshKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForOpenClawDemo user@demo"

	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEC2,
	}

	cfg := &Config{
		Region: "us-east-1",
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{
			NumServers:         "1",
			UserData:           cloudInitScript,
			CustomIngressPorts: []int{22},
			SSHPublicKey:       sshKey,
		},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        &Components{Architecture: "Monolith", AWSEC2: "Intel", AWSVPC: "Public VPC"},
		Cfg:          cfg,
		Project:      "openclaw-demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack(openclaw-demo) should succeed")

	// --- Root files ---
	require.Contains(t, out, "/main.tf")
	require.Contains(t, out, "/variables.tf")
	require.Contains(t, out, "/.terraform-version")

	mainTF := string(out["/main.tf"])

	// --- Module sources ---
	re := func(p string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^\s*source\s*=\s*"` + regexp.QuoteMeta(p) + `"\s*$`)
	}
	require.Regexp(t, re("./modules/vpc"), mainTF, "should have VPC module (aws_vpc uses modules/vpc path)")
	require.Regexp(t, re("./modules/ec2"), mainTF, "should have standalone EC2 module")
	require.NotRegexp(t, re("./modules/eks"), mainTF, "should NOT have EKS module")
	require.NotRegexp(t, re("./modules/eks_nodegroup"), mainTF, "should NOT have EKS node group")

	// --- Exactly one VPC module ---
	vpcModuleCount := regexp.MustCompile(`(?m)^\s*module\s+"[^"]*vpc[^"]*"\s*\{`).FindAllStringIndex(mainTF, -1)
	require.Len(t, vpcModuleCount, 1, "should have exactly one VPC module, not two")

	// --- Cross-module wiring: EC2 ← aws_vpc ---
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*vpc_id\s*=\s*module\.aws_vpc\.vpc_id\s*$`), mainTF)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*subnet_id\s*=\s*module\.aws_vpc\.public_subnet_ids\[0\]\s*$`), mainTF)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*associate_public_ip\s*=\s*true\s*$`), mainTF,
		"Public VPC EC2 should set associate_public_ip = true")

	// --- Tfvars: cloud-init script ---
	require.Contains(t, out, "/aws_ec2.auto.tfvars")
	ec2Tfvars := string(out["/aws_ec2.auto.tfvars"])

	require.Contains(t, ec2Tfvars, "openclaw.ai/install.sh",
		"user_data should contain the OpenClaw install URL")
	require.Contains(t, ec2Tfvars, "openclaw-gateway.service",
		"user_data should contain the systemd unit name")
	require.NotContains(t, ec2Tfvars, "OPENCLAW_GATEWAY_BIND",
		"user_data should NOT set OPENCLAW_GATEWAY_BIND (keep default loopback for security)")
	require.NotContains(t, ec2Tfvars, "0.0.0.0",
		"cloud-init must not bind to all interfaces")

	// --- Tfvars: only SSH port open (port forwarding for OpenClaw) ---
	require.Contains(t, ec2Tfvars, "custom_ingress_ports",
		"custom_ingress_ports should be emitted with SSH port 22")
	require.Contains(t, ec2Tfvars, "22",
		"custom_ingress_ports should include SSH port 22 (needed for port forwarding)")
	require.NotContains(t, ec2Tfvars, "18789",
		"custom_ingress_ports should NOT include 18789 (use SSH port forwarding instead)")

	// --- Tfvars: SSH public key ---
	require.Contains(t, ec2Tfvars, sshKey,
		"ssh_public_key should contain the provided key")

	// --- Tfvars: project name ---
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*aws_ec2_project\s*=\s*"openclaw-demo"\s*$`), ec2Tfvars)

	// --- Variables.tf should declare the namespaced entries ---
	varsTF := string(out["/variables.tf"])
	require.Contains(t, varsTF, `variable "aws_ec2_project"`)
	require.Contains(t, varsTF, `variable "aws_ec2_region"`)

	// --- Preset module files should be included ---
	hasMainTF := false
	hasVarsTF := false
	hasOutputsTF := false
	for path := range out {
		if strings.Contains(path, "/modules/ec2/") {
			if strings.HasSuffix(path, "main.tf") {
				hasMainTF = true
			}
			if strings.HasSuffix(path, "variables.tf") {
				hasVarsTF = true
			}
			if strings.HasSuffix(path, "outputs.tf") {
				hasOutputsTF = true
			}
		}
	}
	require.True(t, hasMainTF, "EC2 preset should include main.tf")
	require.True(t, hasVarsTF, "EC2 preset should include variables.tf")
	require.True(t, hasOutputsTF, "EC2 preset should include outputs.tf")

	// --- Validate all .tf files as HCL ---
	for name, content := range out {
		if strings.HasSuffix(name, ".tf") {
			err := parseHCL(name, content)
			require.NoError(t, err, "openclaw-demo composed file %s should be valid HCL", name)
		}
	}

	// --- Save output if requested ---
	if writeOutDir != "" {
		writeOutputs(t, out, writeOutDir)
	} else if dir := os.Getenv("SAVE_OUTPUT_DIR"); dir != "" {
		writeOutputs(t, out, dir)
	}
}

// TestComposeStack_OpenClawDemo_URL verifies that when userData is a URL,
// the mapper emits user_data_url (not user_data) so the EC2 preset fetches
// and executes the script on boot.
func TestComposeStack_OpenClawDemo_URL(t *testing.T) {
	t.Parallel()
	gistURL := "https://gist.githubusercontent.com/sam-at-luther/b36742c84b7ec3e1d4789d21b8df55e3/raw/openclaw-cloud-init.sh"
	sshKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForOpenClawDemo user@demo"

	cfg := &Config{
		Region: "us-east-1",
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{
			NumServers:         "1",
			UserDataURL:        gistURL,
			CustomIngressPorts: []int{22},
			SSHPublicKey:       sshKey,
		},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSEC2},
		Comps:        &Components{Architecture: "Monolith", AWSEC2: "ARM", AWSVPC: "Public VPC"},
		Cfg:          cfg,
		Project:      "openclaw-url-test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack should succeed with URL userData")

	ec2Tfvars := string(out["/aws_ec2.auto.tfvars"])

	// URL should be emitted as user_data_url, not user_data
	require.Contains(t, ec2Tfvars, "user_data_url",
		"URL in userData should emit user_data_url variable")
	require.Contains(t, ec2Tfvars, gistURL,
		"user_data_url should contain the gist URL")
	require.NotRegexp(t, regexp.MustCompile(`(?m)^\s*aws_ec2_user_data\s*=`), ec2Tfvars,
		"should NOT emit user_data when URL is provided (user_data_url handles it)")

	// Validate HCL
	for name, content := range out {
		if strings.HasSuffix(name, ".tf") {
			err := parseHCL(name, content)
			require.NoError(t, err, "file %s should be valid HCL", name)
		}
	}
}

// TestComposeStack_EC2_InstanceConnect verifies that EnableInstanceConnect=true
// produces enable_instance_connect=true in the generated .auto.tfvars.
func TestComposeStack_EC2_InstanceConnect(t *testing.T) {
	t.Parallel()
	trueVal := true

	cfg := &Config{
		Region: "us-east-1",
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{
			NumServers:            "1",
			EnableInstanceConnect: &trueVal,
		},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSEC2},
		Comps:        &Components{Architecture: "Monolith", AWSEC2: "ARM", AWSVPC: "Public VPC"},
		Cfg:          cfg,
		Project:      "ic-test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	ec2Tfvars := string(out["/aws_ec2.auto.tfvars"])
	require.Contains(t, ec2Tfvars, "enable_instance_connect",
		"should emit enable_instance_connect when EnableInstanceConnect=true")
	require.Contains(t, ec2Tfvars, "enable_instance_connect = true",
		"enable_instance_connect should be true")

	// Validate HCL
	for name, content := range out {
		if strings.HasSuffix(name, ".tf") {
			err := parseHCL(name, content)
			require.NoError(t, err, "file %s should be valid HCL", name)
		}
	}
}

// TestComposeStack_EC2_NoInstanceConnect verifies that when EnableInstanceConnect
// is nil, enable_instance_connect does NOT appear in tfvars output.
func TestComposeStack_EC2_NoInstanceConnect(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Region: "us-east-1",
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{
			NumServers: "1",
		},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSEC2},
		Comps:        &Components{Architecture: "Monolith", AWSEC2: "ARM", AWSVPC: "Public VPC"},
		Cfg:          cfg,
		Project:      "no-ic-test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	ec2Tfvars := string(out["/aws_ec2.auto.tfvars"])
	require.NotContains(t, ec2Tfvars, "enable_instance_connect",
		"should NOT emit enable_instance_connect when nil")
}

// TestComposeStack_OutputsTF verifies that ComposeStack generates a root /outputs.tf
// that re-exports module-level outputs with namespaced names and correct value expressions.
func TestComposeStack_OutputsTF(t *testing.T) {
	t.Parallel()

	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEC2,
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        &Components{Architecture: "Monolith", AWSEC2: "Intel"},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack should succeed")

	// outputs.tf must exist and be valid HCL
	require.Contains(t, out, "/outputs.tf", "ComposeStack should generate /outputs.tf")
	require.NoError(t, parseHCL("outputs.tf", out["/outputs.tf"]))

	outputsTF := string(out["/outputs.tf"])

	// Structural assertion: verify specific output blocks map correct names to correct values.
	// This catches value-swapping bugs that loose Contains assertions would miss.
	// aws_vpc should re-export vpc_id as aws_vpc_vpc_id → module.aws_vpc.vpc_id
	require.Regexp(t, regexp.MustCompile(
		`(?s)output "aws_vpc_vpc_id"\s*\{[^}]*value\s*=\s*module\.aws_vpc\.vpc_id`),
		outputsTF, "aws_vpc_vpc_id should map to module.aws_vpc.vpc_id")

	// aws_ec2 should re-export instance_id as aws_ec2_instance_id → module.aws_ec2.instance_id
	require.Regexp(t, regexp.MustCompile(
		`(?s)output "aws_ec2_instance_id"\s*\{[^}]*value\s*=\s*module\.aws_ec2\.instance_id`),
		outputsTF, "aws_ec2_instance_id should map to module.aws_ec2.instance_id")

	// Both modules should have outputs
	require.Regexp(t, regexp.MustCompile(`output "aws_vpc_`), outputsTF)
	require.Regexp(t, regexp.MustCompile(`output "aws_ec2_`), outputsTF)
}

// TestComposeStack_OutputsTF_KitchenSink verifies outputs.tf in a large multi-module stack
// with structural assertions on known outputs.
func TestComposeStack_OutputsTF_KitchenSink(t *testing.T) {
	t.Parallel()

	selected := []ComponentKey{
		KeyAWSVPC,
		KeyResource, // EKS control plane (polymorphic)
		KeyEC2,      // EKS node group (polymorphic)
		KeyAWSRDS,
		KeyAWSS3,
	}

	cfg := &Config{
		Region: "us-east-1",
		RDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: "db.m7i.large", StorageSize: "20"},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        &Components{},
		Cfg:          cfg,
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack should succeed")

	require.Contains(t, out, "/outputs.tf")
	require.NoError(t, parseHCL("outputs.tf", out["/outputs.tf"]))

	outputsTF := string(out["/outputs.tf"])

	// Structural: verify known outputs map to correct module references
	knownOutputs := map[string]string{
		"aws_vpc_vpc_id":     "module.aws_vpc.vpc_id",
		"aws_rds_db_address": "module.aws_rds.db_address",
		"aws_s3_bucket_arn":  "module.aws_s3.bucket_arn",
	}
	for name, valueExpr := range knownOutputs {
		re := regexp.MustCompile(`(?s)output "` + regexp.QuoteMeta(name) + `"\s*\{[^}]*value\s*=\s*` + regexp.QuoteMeta(valueExpr))
		require.Regexp(t, re, outputsTF,
			"output %q should map to %s", name, valueExpr)
	}
}

// TestComposeSingle_OutputsTF verifies that ComposeSingle generates /outputs.tf
// with structurally correct output blocks for a standalone EC2 (Monolith) module.
func TestComposeSingle_OutputsTF(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeSingle(ComposeSingleOpts{
		Cloud:   "aws",
		Key:     KeyAWSEC2,
		Comps:   &Components{Architecture: "Monolith", AWSEC2: "Intel"},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err, "ComposeSingle should succeed")

	require.Contains(t, out, "/outputs.tf", "ComposeSingle should generate /outputs.tf")
	require.NoError(t, parseHCL("outputs.tf", out["/outputs.tf"]))

	outputsTF := string(out["/outputs.tf"])

	// Structural: aws_ec2_instance_id should map to module.aws_ec2.instance_id
	require.Regexp(t, regexp.MustCompile(
		`(?s)output "aws_ec2_instance_id"\s*\{[^}]*value\s*=\s*module\.aws_ec2\.instance_id`),
		outputsTF, "aws_ec2_instance_id should map to module.aws_ec2.instance_id")
}

func TestComposeSingle_WAFProviderAlias(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeSingle(ComposeSingleOpts{
		Cloud:   "aws",
		Key:     KeyAWSWAF,
		Comps:   &Components{},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-west-2",
	})
	require.NoError(t, err, "ComposeSingle with KeyAWSWAF should succeed")

	// ComposeSingle generates main.tf with the module block — verify providers override
	mainTF := string(out["/main.tf"])
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*providers\s+=\s+\{`), mainTF)
	require.Contains(t, mainTF, "aws = aws")
	require.Contains(t, mainTF, "aws.us_east_1 = aws.us_east_1")
}

// TestDeduplicateKeys verifies that legacy keys are removed when V2 equivalents are present.
func TestDeduplicateKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		keys []ComponentKey
		want []ComponentKey
	}{
		{
			name: "no duplicates",
			keys: []ComponentKey{KeyAWSVPC, KeyAWSLambda},
			want: []ComponentKey{KeyAWSVPC, KeyAWSLambda},
		},
		{
			name: "legacy only — kept",
			keys: []ComponentKey{KeyVPC, KeyLambda},
			want: []ComponentKey{KeyVPC, KeyLambda},
		},
		{
			name: "both legacy and V2 — legacy removed",
			keys: []ComponentKey{KeyVPC, KeyAWSVPC, KeyLambda, KeyAWSLambda},
			want: []ComponentKey{KeyAWSVPC, KeyAWSLambda},
		},
		{
			name: "mixed — only duplicated legacy removed",
			keys: []ComponentKey{KeyVPC, KeyAWSVPC, KeyS3},
			want: []ComponentKey{KeyAWSVPC, KeyS3},
		},
		{
			name: "GCP keys unaffected",
			keys: []ComponentKey{KeyGCPVPC, KeyGCPGKE},
			want: []ComponentKey{KeyGCPVPC, KeyGCPGKE},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DeduplicateKeys(tt.keys)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestComposeStack_ServerlessLambdaNoDuplicateVPC verifies that a serverless stack
// with aws_vpc + aws_lambda produces exactly ONE VPC module block, not two.
// Regression test for: Lambda's implicit dependency on KeyVPC would create a
// duplicate "vpc" module alongside the "aws_vpc" module from the session components.
func TestComposeStack_ServerlessLambdaNoDuplicateVPC(t *testing.T) {
	t.Parallel()

	trueVal := true
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSLambda,
		KeyAWSS3,
		KeyAWSCloudWatchLogs,
		KeyAWSCloudWatchMonitoring,
	}

	comps := &Components{
		AWSVPC: "Public VPC",
		Lambda: &trueVal,
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        comps,
		Cfg:          &Config{Region: "us-west-2"},
		Project:      "serverless-test",
		Region:       "us-west-2",
	})
	require.NoError(t, err, "ComposeStack(serverless) should succeed")

	mainTF := string(out["/main.tf"])

	// Exactly one VPC module — not two
	vpcModuleCount := regexp.MustCompile(`(?m)^\s*module\s+"[^"]*vpc[^"]*"\s*\{`).FindAllStringIndex(mainTF, -1)
	require.Len(t, vpcModuleCount, 1, "should have exactly one VPC module, got %d", len(vpcModuleCount))

	// The surviving module should be "aws_vpc", not "vpc"
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*module\s+"aws_vpc"\s*\{`), mainTF,
		"VPC module should be named aws_vpc")
	require.NotRegexp(t, regexp.MustCompile(`(?m)^\s*module\s+"vpc"\s*\{`), mainTF,
		"should NOT have a legacy 'vpc' module when aws_vpc is present")

	// Lambda should NOT wire to VPC in a Public VPC (no private subnets available).
	// Without private subnets + NAT, Lambda in VPC can't reach the internet,
	// and passing empty subnet_ids causes AWS API error.
	require.NotContains(t, mainTF, "module.aws_vpc.vpc_id",
		"Lambda should not wire vpc_id in Public VPC")
	require.NotContains(t, mainTF, "enable_vpc",
		"Lambda should not have enable_vpc in Public VPC")

	// VPC tfvars should contain Public VPC settings
	require.Contains(t, out, "/aws_vpc.auto.tfvars")
	vpcTfvars := string(out["/aws_vpc.auto.tfvars"])
	require.Contains(t, vpcTfvars, "aws_vpc_enable_private_subnets = false",
		"Public VPC should set enable_private_subnets = false")
	require.Contains(t, vpcTfvars, "aws_vpc_enable_nat_gateway",
		"Public VPC should set enable_nat_gateway")

	// Validate HCL
	for name, content := range out {
		if strings.HasSuffix(name, ".tf") {
			err := parseHCL(name, content)
			require.NoError(t, err, "serverless composed file %s should be valid HCL", name)
		}
	}
}

// TestComposeStack_GCP_Provider validates that GCP stacks generate Google provider config.
func TestComposeStack_GCP_Provider(t *testing.T) {
	selected := []ComponentKey{
		KeyGCPVPC,
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: selected,
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "test-project",
		Region:       "us-central1",
	})
	require.NoError(t, err, "ComposeStack(gcp) should succeed")

	// Check providers.tf has Google provider
	providersTF, ok := out["/providers.tf"]
	require.True(t, ok, "should have /providers.tf")

	provStr := string(providersTF)
	require.Contains(t, provStr, "hashicorp/google", "should use Google provider")
	require.Contains(t, provStr, `provider "google"`, "should have google provider block")
	require.Contains(t, provStr, "us-central1", "should use specified region")

	// GCP intentionally has no default_tags / default_labels safety net: the
	// google provider has native per-session credential isolation via
	// creds.ProjectID and Reliable #1112's scope was AWS-only. Guard against
	// someone adding half-working GCP tagging by accident — if a parity
	// story ever lands it should be a deliberate feature change that updates
	// this test, not a drive-by edit.
	//
	// Scope note: provStr is the root /providers.tf only (no preset contents),
	// so a NotContains on the full string has negligible false-positive risk
	// from a module name or comment mentioning "default_tags". If the haystack
	// ever widens to include preset bodies, scope the check to the
	// `provider "google" { ... }` block.
	require.NotContains(t, provStr, "default_labels",
		"GCP provider block should not declare default_labels (see #111; any GCP tagging parity should land via a deliberate feature, not a drive-by edit)")
	require.NotContains(t, provStr, "default_tags",
		"GCP provider block should not borrow AWS-shaped default_tags")
}

// TestComposeStack_DiscoveredProvidersReachRoot exercises the end-to-end path
// where a child module's non-AWS `required_providers` declaration (e.g. ALB
// declaring hashicorp/random) is discovered via DiscoverRequiredProviders
// and merged into the root providers.tf. Unit tests on
// DiscoverRequiredProviders alone don't cover the merge in generateProvidersTF.
func TestComposeStack_DiscoveredProvidersReachRoot(t *testing.T) {
	c := newTestClient()

	// Precondition: the ALB preset really does declare hashicorp/random in
	// its required_providers. If this test silently becomes a no-op because
	// the ALB module dropped the provider, the precondition fails first —
	// much clearer diagnostic than a passing assertion on an absent string.
	albFiles, err := c.GetPresetFiles("aws/alb")
	require.NoError(t, err)
	albProvs, err := DiscoverRequiredProviders(albFiles)
	require.NoError(t, err)
	require.Contains(t, albProvs, "random",
		"precondition: aws/alb preset should declare a random = {...} required_provider; if this fails, pick a different preset to exercise the discovered-providers merge")

	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSALB},
		Comps:        &Components{AWSVPC: "Private VPC", AWSALB: ptrBool(true)},
		Cfg:          &Config{},
		Project:      "discovered-test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	prov := string(out["/providers.tf"])
	require.Contains(t, prov, "hashicorp/random",
		"root providers.tf should include the ALB module's discovered hashicorp/random required_providers entry")
	require.Contains(t, prov, "hashicorp/aws",
		"root providers.tf should keep the cloud's base required_provider entry")
	// Lock the merge location: hashicorp/random must appear inside a
	// `random = { ... }` entry (i.e. it's a keyed required_providers block,
	// not a stray substring in a comment or module name).
	require.Regexp(t, regexp.MustCompile(`(?s)random\s*=\s*\{[^}]*hashicorp/random`), prov,
		"hashicorp/random should be attached to a random = {...} entry in required_providers, not just appear as a substring")
}

// TestComposeStack_ProjectRoundTrip renders with a distinctive Project value
// and asserts that value flows through to the root variables.tf default, the
// per-module .auto.tfvars, and the provider default_tags block. Each
// assertion binds the sentinel to the specific variable/declaration it
// belongs to — a bare substring match would pass even if the sentinel landed
// in an unrelated comment or the wrong variable.
func TestComposeStack_ProjectRoundTrip(t *testing.T) {
	const sentinel = "demo-xyz-round-trip"
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{AWSVPC: "Private VPC"},
		Cfg:          &Config{},
		Project:      sentinel,
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	// Root variables.tf: the sentinel must appear as the default of the
	// `variable "project"` block, not somewhere else (a module name, a
	// comment, another variable's default).
	varsTF := string(out["/variables.tf"])
	require.Regexp(t,
		regexp.MustCompile(`(?s)variable "project"\s*\{[^}]*default\s*=\s*"`+regexp.QuoteMeta(sentinel)+`"`),
		varsTF,
		"root variables.tf should carry ComposeStackOpts.Project as the default of variable \"project\"")

	// Per-module .auto.tfvars: sentinel must be bound to aws_vpc_project
	// specifically, not aws_vpc_region or any other key.
	require.Contains(t, out, "/aws_vpc.auto.tfvars")
	vpcTf := string(out["/aws_vpc.auto.tfvars"])
	require.Regexp(t,
		regexp.MustCompile(`(?m)^\s*aws_vpc_project\s*=\s*"`+regexp.QuoteMeta(sentinel)+`"\s*$`),
		vpcTf,
		"aws_vpc.auto.tfvars should bind aws_vpc_project to the sentinel, not leak it into another key")

	// Provider default_tags: `Project = var.project` must appear (the binding
	// is what makes the round-trip work at apply time). Whitespace-tolerant
	// regex matches terraform fmt output.
	prov := string(out["/providers.tf"])
	require.Regexp(t,
		regexp.MustCompile(`Project\s*=\s*var\.project`),
		prov,
		"provider default_tags should bind Project to var.project (not a hardcoded literal)")
}

func TestDefaultWiring_AWSECS(t *testing.T) {
	t.Parallel()

	t.Run("ECS with VPC wires vpc_id and subnet_ids", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{
			KeyAWSVPC: true,
			KeyAWSECS: true,
		}
		wi := DefaultWiring(selected, KeyAWSECS, nil)

		require.Contains(t, wi.RawHCL, "vpc_id")
		require.Contains(t, wi.RawHCL, "private_subnet_ids")
		require.Contains(t, wi.RawHCL, "public_subnet_ids")

		require.Contains(t, wi.RawHCL["vpc_id"], "module.aws_vpc.vpc_id")
		require.Contains(t, wi.RawHCL["private_subnet_ids"], "module.aws_vpc.private_subnet_ids")
	})

	t.Run("ECS without VPC has no wiring", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{
			KeyAWSECS: true,
		}
		wi := DefaultWiring(selected, KeyAWSECS, nil)
		require.Empty(t, wi.RawHCL)
		require.Empty(t, wi.Names)
	})

	t.Run("ECS does not get cluster_enabled_log_types", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{
			KeyAWSVPC: true,
			KeyAWSECS: true,
		}
		wi := DefaultWiring(selected, KeyAWSECS, nil)
		_, hasLogTypes := wi.RawHCL["cluster_enabled_log_types"]
		require.False(t, hasLogTypes, "ECS must not have cluster_enabled_log_types")
	})
}

func TestDefaultWiring_GCPVPCServerlessConnector(t *testing.T) {
	t.Parallel()

	t.Run("VPC enables connector when Cloud Run is selected", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{
			KeyGCPVPC:      true,
			KeyGCPCloudRun: true,
		}
		wi := DefaultWiring(selected, KeyGCPVPC, nil)
		require.Equal(t, "true", wi.RawHCL["enable_serverless_connector"])
		require.Equal(t, "\"vpc\"", wi.RawHCL["network_name"], "base VPC wiring must be preserved")
	})

	t.Run("VPC enables connector when Cloud Functions is selected", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{
			KeyGCPVPC:            true,
			KeyGCPCloudFunctions: true,
		}
		wi := DefaultWiring(selected, KeyGCPVPC, nil)
		require.Equal(t, "true", wi.RawHCL["enable_serverless_connector"])
	})

	t.Run("VPC does not enable connector without serverless", func(t *testing.T) {
		t.Parallel()
		selected := map[ComponentKey]bool{
			KeyGCPVPC: true,
			KeyGCPGKE: true,
		}
		wi := DefaultWiring(selected, KeyGCPVPC, nil)
		_, hasConnector := wi.RawHCL["enable_serverless_connector"]
		require.False(t, hasConnector, "VPC should not enable connector without Cloud Run/Functions")
	})
}

func TestComposeStack_AWSECS(t *testing.T) {
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSECS,
	}

	comps := &Components{AWSECS: ptrBool(true)}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: selected,
		Comps:        comps,
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack with ECS should succeed")

	mainTF := string(out["/main.tf"])

	// ECS module source must be ./modules/ecs, NOT ./modules/eks
	re := regexp.MustCompile(`(?m)^\s*source\s*=\s*"` + regexp.QuoteMeta("./modules/ecs") + `"\s*$`)
	require.Regexp(t, re, mainTF, "ECS module source should be ./modules/ecs")

	reEKS := regexp.MustCompile(`(?m)^\s*source\s*=\s*"` + regexp.QuoteMeta("./modules/eks") + `"\s*$`)
	require.NotRegexp(t, reEKS, mainTF, "should not contain EKS module source")

	// Cross-module wiring: ECS ← VPC
	require.Contains(t, mainTF, "module.aws_vpc.vpc_id")
	require.Contains(t, mainTF, "module.aws_vpc.private_subnet_ids")

	// Must NOT contain EKS-specific wiring
	require.NotContains(t, mainTF, "cluster_enabled_log_types")

	// Verify ECS tfvars file is generated with namespaced variables
	ecsTfvars, ok := out["/aws_ecs.auto.tfvars"]
	require.True(t, ok, "should have /aws_ecs.auto.tfvars")
	ecsTfStr := string(ecsTfvars)
	require.Contains(t, ecsTfStr, "aws_ecs_project")
	require.Contains(t, ecsTfStr, "aws_ecs_region")
}

// TestComposeStack_RejectsLegacyKeys is the parity test issue #118 calls out:
// a single end-to-end signal that purely-legacy selections error at the
// composer entry point, not silently produce un-wired modules. This is the
// downstream-visible contract reliable's composeradapter upholds — unit
// coverage of ValidateNoLegacyKeys lives in validate_test.go.
func TestComposeStack_RejectsLegacyKeys(t *testing.T) {
	c := newTestClient()
	_, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyVPC, KeyPostgres, KeyS3},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.Error(t, err, "ComposeStack must reject purely-legacy SelectedKeys")
	var ve *ValidationError
	require.ErrorAs(t, err, &ve, "error must be a ValidationError (HTTP 400, not 500)")
	msg := err.Error()
	require.Contains(t, msg, "legacy ComponentKey",
		"error must identify legacy-keys as the cause")
	require.Contains(t, msg, "vpc → aws_vpc",
		"error must carry the upgrade pair so callers can fix the selection")
	require.Contains(t, msg, "composeradapter",
		"error must point callers at reliable's upgrade path")
}

// TestComposeSingle_RejectsLegacyKey pins the same contract for the
// single-module entry point: ComposeSingle validates its Key just like
// ComposeStack validates its SelectedKeys.
func TestComposeSingle_RejectsLegacyKey(t *testing.T) {
	c := newTestClient()
	_, err := c.ComposeSingle(ComposeSingleOpts{
		Cloud:   "aws",
		Key:     KeyVPC,
		Comps:   &Components{},
		Cfg:     &Config{},
		Project: "test",
		Region:  "us-east-1",
	})
	require.Error(t, err, "ComposeSingle must reject a legacy Key")
	var ve *ValidationError
	require.ErrorAs(t, err, &ve, "error must be a ValidationError")
	require.Contains(t, err.Error(), "vpc → aws_vpc",
		"error must carry the upgrade pair")
}

// TestComposeStack_NormalizesLegacyConfig pins the load-bearing contract
// that ComposeStack (and ComposeSingle) call Config.Normalize() at entry.
// Phase 3b dropped the mapper's legacy-field reads, so this invariant is
// what keeps direct Go callers who build a Config with legacy field names
// (e.g. `cfg.CloudWatchLogs`) working end-to-end. If someone deletes the
// Normalize() call at compose.go:98 or compose.go:249, the kitchen-sink
// tests (which populate prefixed fields directly) will still pass — but
// this test will fail loudly.
func TestComposeStack_NormalizesLegacyConfig(t *testing.T) {
	c := newTestClient()

	// Legacy Config field populated; AWSCloudWatchLogs left nil. A correct
	// ComposeStack must call Normalize first so the legacy value reaches
	// the mapper via its AWS-prefixed sibling.
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSCloudWatchLogs},
		Comps:        &Components{Cloud: "AWS", AWSCloudWatchLogs: ptrBool(true)},
		Cfg: &Config{
			Cloud:  "AWS",
			Region: "us-east-1",
			CloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{RetentionDays: 180},
		},
		Project: "test",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	tfvars := string(out["/aws_cloudwatch_logs.auto.tfvars"])
	require.Contains(t, tfvars, "aws_cloudwatch_logs_retention_in_days = 180",
		"legacy cfg.CloudWatchLogs.RetentionDays must be promoted by "+
			"Config.Normalize during ComposeStack and flow through to the mapper output")
}

// TestComposeSingle_NormalizesLegacyConfig mirrors the ComposeStack test
// above for the single-module entry point.
func TestComposeSingle_NormalizesLegacyConfig(t *testing.T) {
	c := newTestClient()

	out, err := c.ComposeSingle(ComposeSingleOpts{
		Cloud: "aws",
		Key:   KeyAWSCloudWatchLogs,
		Comps: &Components{Cloud: "AWS", AWSCloudWatchLogs: ptrBool(true)},
		Cfg: &Config{
			Cloud:  "AWS",
			Region: "us-east-1",
			CloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{RetentionDays: 180},
		},
		Project: "test",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	tfvars := string(out["/aws_cloudwatch_logs.auto.tfvars"])
	require.Contains(t, tfvars, "aws_cloudwatch_logs_retention_in_days = 180",
		"legacy cfg.CloudWatchLogs.RetentionDays must flow through via "+
			"Config.Normalize in ComposeSingle and reach the preset tfvars")
}

// TestComposeStack_PolymorphicKeyPullsInPrefixedVPC is a regression test for
// the implicit-dependency leak where ResolveDependencies expanded a
// polymorphic key to its legacy VPC sibling. A direct Go caller passing only
// [KeyResource] must still produce a prefixed-only stack: composeradapter
// upgrades session JSON keys, but in-process callers don't go through it.
func TestComposeStack_PolymorphicKeyPullsInPrefixedVPC(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyResource}, // polymorphic, pulls VPC via ImplicitDependencies
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack with polymorphic KeyResource should succeed")

	mainTF := string(out["/main.tf"])
	require.Contains(t, mainTF, `module "aws_vpc"`,
		"implicit dep must render aws_vpc, not legacy vpc")
	require.NotContains(t, mainTF, `module "vpc"`,
		"legacy module.vpc must never appear in a prefixed-only stack")
	require.Contains(t, mainTF, `module "resource"`,
		"polymorphic KeyResource still renders under its own name until Phase 4")
}
