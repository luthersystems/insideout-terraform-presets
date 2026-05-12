package composer

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
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
			// #285: when per-component consumers are selected alongside
			// the aggregator, the back-edge wiring is suppressed (would
			// otherwise close a 2-cycle with each consumer) and the
			// disable flag flips so the legacy aggregator-side alarms
			// retire — per-component observability.tf in each consumer
			// owns the equivalent alarms. wantNotIn pins both the legacy
			// module name shape (regression of #283-class drift) and the
			// V2-prefixed back-edge references (regression of #285).
			name: "cloudwatch monitoring disables legacy alarms when per-component consumers are present",
			key:  KeyAWSCloudWatchMonitoring,
			wantIn: map[string]string{
				"disable_legacy_per_component_alarms": "true",
			},
			wantNotIn: []string{
				"module.bastion.", "module.rds.", "module.alb.", "module.sqs.",
				"module.aws_bastion.bastion_instance_id",
				"module.aws_rds.instance_id",
				"module.aws_alb.alb_arn_suffix",
				"module.aws_sqs.queue_arn",
			},
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

	// Monitoring: per-component observability is active (consumers selected
	// alongside cwm), so the legacy aggregator-side back-edges are dropped
	// and disable_legacy_per_component_alarms flips on (#285). Per-component
	// alarms still notify via the cwm SNS topic via the post-switch
	// alarm_topic_arn forward-edge.
	require.Regexp(t,
		regexp.MustCompile(`(?m)^\s*disable_legacy_per_component_alarms\s*=\s*true\s*$`),
		mainTF,
		"cwm must disable legacy alarms when per-component consumers are present (#285)")
	require.NotContains(t, mainTF, "module.aws_bastion.bastion_instance_id",
		"back-edge from cwm to bastion must not render (#285)")
	require.NotContains(t, mainTF, "module.aws_rds.instance_id",
		"back-edge from cwm to rds must not render (#285)")
	require.NotContains(t, mainTF, "module.aws_alb.alb_arn_suffix",
		"back-edge from cwm to alb must not render (#285)")
	require.NotContains(t, mainTF, "module.aws_sqs.queue_arn",
		"back-edge from cwm to sqs must not render (#285)")
	require.Contains(t, mainTF, "module.aws_cloudwatch_monitoring.sns_topic_arn",
		"forward-edge alarm_topic_arn must still render so per-component alarms notify")

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
	// plus the polymorphic KeyAWSEKSControlPlane (EKS control plane) and KeyAWSEKSNodeGroup (EKS
	// managed node group) — those stay until Phase 4 renames them to
	// unambiguous `KeyAWSEKSControlPlane` / `KeyAWSEKSNodeGroup`.
	selected := []ComponentKey{
		KeyAWSVPC,
		KeyAWSEKS,          // EKS control plane
		KeyAWSEKSNodeGroup, // EKS node group (polymorphic; KeyAWSEKSNodeGroup lands in Phase 4)
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
		// nodegroup block is padded to enable_observability (20 chars;
		// #204 — KeyAWSEKSNodeGroup added to PricingDependencies so the
		// per-component EKS alarm gets its alarm_topic_arn wired).
		require.Contains(t, mainTF, `cluster_name         = module.aws_eks.cluster_name`)
		require.Contains(t, mainTF, `subnet_ids           = module.aws_vpc.private_subnet_ids`)
		require.Contains(t, mainTF, `alarm_topic_arn      = module.aws_cloudwatch_monitoring.sns_topic_arn`)
		require.Contains(t, mainTF, `enable_observability = true`)
	})

	t.Run("wiring/alb", func(t *testing.T) {
		// ALB block is padded to enable_observability (20 chars; #204).
		require.Contains(t, mainTF, `vpc_id               = module.aws_vpc.vpc_id`)
		require.Contains(t, mainTF, `public_subnet_ids    = module.aws_vpc.public_subnet_ids`)
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
		// ElastiCache block is padded to enable_observability (20 chars; #204).
		require.Contains(t, mainTF, `vpc_id               = module.aws_vpc.vpc_id`)
		require.Contains(t, mainTF, `cache_subnet_ids     = module.aws_vpc.private_subnet_ids`)
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
		// #285: when per-component observability consumers are in the
		// stack, the cwm aggregator drops its back-edge wiring (which
		// would otherwise close a 2-cycle with each consumer) and flips
		// disable_legacy_per_component_alarms = true so the legacy
		// aggregator-side alarms retire — per-component observability.tf
		// in each consumer module owns the equivalent alarms.
		require.Regexp(t,
			regexp.MustCompile(`(?m)^\s*disable_legacy_per_component_alarms\s*=\s*true\s*$`),
			mainTF,
			"cwm must disable legacy alarms when per-component observability consumers are in the stack")
		// Bare-RHS substrings: whitespace-independent so a renderer
		// alignment change can't make these vacuously pass.
		require.NotContains(t, mainTF, "module.aws_bastion.bastion_instance_id",
			"back-edge from cwm to bastion must not render once per-component observability is wired (#285)")
		require.NotContains(t, mainTF, "module.aws_rds.instance_id",
			"back-edge from cwm to rds must not render (#285)")
		require.NotContains(t, mainTF, "module.aws_alb.alb_arn_suffix",
			"back-edge from cwm to alb must not render (#285)")
		require.NotContains(t, mainTF, "module.aws_sqs.queue_arn",
			"back-edge from cwm to sqs must not render (#285)")
		// Forward-edge: per-component alarms still notify via the cwm
		// SNS topic (silent-paging-failure regression net — a mutation
		// that drops the post-switch wiring is caught here).
		require.Contains(t, mainTF, "alarm_topic_arn      = module.aws_cloudwatch_monitoring.sns_topic_arn",
			"forward-edge alarm_topic_arn must still render so per-component alarms notify")
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
		KeyAWSEKSNodeGroup, // EKS node group (polymorphic)
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
		KeyAWSEKSNodeGroup, // EKS node group (polymorphic)
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
		AWSRDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: "db.m7i.large", StorageSize: "20"},
		AWSSQS: &struct {
			Type              string `json:"type,omitempty"`
			VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
		}{Type: "FIFO"},
		AWSCloudWatchLogs: &struct {
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
		KeyAWSEKSNodeGroup, // EKS node group (polymorphic)
		KeyAWSRDS,
		KeyAWSS3,
		KeyAWSCloudWatchLogs,
	}

	comps := &Components{}
	cfg := &Config{
		Region: "us-east-1",
		AWSRDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: "db.m7i.large", StorageSize: "20"},
		AWSCloudWatchLogs: &struct {
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
			keys:   []ComponentKey{KeyAWSLambda, KeyAWSEKSNodeGroup, KeyAWSVPC},
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
		KeyAWSEKSControlPlane, // EKS control plane (polymorphic)
		KeyAWSEKSNodeGroup,    // EKS node group (polymorphic)
		KeyAWSRDS,
		KeyAWSS3,
	}

	cfg := &Config{
		Region: "us-east-1",
		AWSRDS: &struct {
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

// TestComposeStack_ServerlessLambdaNoDuplicateVPC verifies that a serverless
// stack with aws_vpc + aws_lambda produces exactly ONE VPC module block.
// Regression guard against ImplicitDependencies leaking a legacy VPC key.
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
		AWSVPC:    "Public VPC",
		AWSLambda: &trueVal,
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
		GCPProjectID: "test-project-12345",
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

	// GCP default_labels safety net: parity with the AWS default_tags shape.
	// #111 deferred this; the deliberate feature change landed via #215 as
	// a belt-and-suspenders for the phantom-`+ labels = {}` drift fix
	// (every preset module is supposed to render
	// `labels = merge({ project = var.project }, var.labels)`, but a missed
	// site silently leaves the resource without the project label that
	// the InsideOut inspector filters on). default_labels at the
	// provider level guarantees the project label reaches every label-capable
	// resource regardless of whether the preset wires labels itself.
	//
	// Requires google provider 5.16+ (default_labels was introduced there);
	// generateProvidersTF pins the GCP required_providers to ">= 5.16".
	require.Contains(t, provStr, "default_labels",
		"GCP provider block should declare default_labels as the project-label safety net (#215)")
	require.Contains(t, provStr, "project    = var.project",
		"default_labels should bind project = var.project so the InsideOut inspector can filter by project")
	require.Contains(t, provStr, `managed-by = "insideout"`,
		"default_labels should also carry managed-by = \"insideout\" mirroring the AWS default_tags shape")
	require.NotContains(t, provStr, "default_tags",
		"GCP provider block should not borrow AWS-shaped default_tags")
}

// TestComposeStack_DiscoveredProvidersReachRoot exercises the end-to-end path
// where a child module's non-AWS `required_providers` declaration (e.g. ALB
// declaring hashicorp/random) is discovered via InspectPreset and merged
// into the root providers.tf. Unit tests on InspectPreset alone don't cover
// the merge in generateProvidersTF.
func TestComposeStack_DiscoveredProvidersReachRoot(t *testing.T) {
	c := newTestClient()

	// Precondition: the ALB preset really does declare hashicorp/random in
	// its required_providers. If this test silently becomes a no-op because
	// the ALB module dropped the provider, the precondition fails first —
	// much clearer diagnostic than a passing assertion on an absent string.
	albMod, err := InspectPreset("aws/alb")
	require.NoError(t, err)
	require.Contains(t, albMod.RequiredProviders, "random",
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

// TestGCPPresets_AllSuffixedResourceNames pins the issue #159 contract
// across every GCP module that creates a named cloud resource: each must
// (a) declare hashicorp/random in required_providers, (b) declare a
// resource "random_id" "suffix" block, and (c) interpolate
// ${random_id.suffix.hex} as a real HCL expression somewhere in the
// preset's source.
//
// Why a table sweep, not a single-instance smoke test:
// the customer incident chain that motivated #159 (the InsideOut backend #1167 → #1168)
// hit *three* dead-ends — the KMS keyring (undeletable), the Firestore
// (default) database (singleton), and a GCS bucket (7-day soft-delete name
// reservation). A KMS-only assertion would let a future edit silently
// revert gcp/firestore/main.tf back to "(default)" with green CI and
// reproduce the original incident. Enumerating every in-scope module here
// makes "remove the suffix from any one preset" fail loudly.
//
// The skip allowlist captures the GCP modules the PR intentionally
// left untouched: cloud_monitoring (only a non-unique displayName
// JSON), identity_platform (singleton per project, no name field),
// vertex_ai (display_name is non-unique server-side; the resource ID
// is server-assigned). New GCP modules must either land on the suffix
// list or be added to the skip allowlist with a justification.
func TestGCPPresets_AllSuffixedResourceNames(t *testing.T) {
	// In-scope modules — every named cloud resource must carry the suffix.
	suffixedPresets := []string{
		"gcp/api_gateway",
		"gcp/backups",
		"gcp/bastion",
		"gcp/cloud_armor",
		"gcp/cloud_build",
		"gcp/cloud_functions",
		"gcp/cloud_logging",
		"gcp/cloud_run",
		"gcp/cloudsql",
		"gcp/compute",
		"gcp/firestore",
		"gcp/gcs",
		"gcp/gke",
		"gcp/kms",
		"gcp/loadbalancer",
		"gcp/memorystore",
		"gcp/pubsub",
		"gcp/secretmanager",
		"gcp/vpc",
	}

	// Pre-anchored regexes — declared once so test-loop hot path stays cheap.
	// randomIDBlockRe: a real `resource "random_id" "suffix" { ... }` block
	//   (not just the substring random_id.suffix in a comment or string).
	// suffixInterpRe: ${random_id.suffix.hex} as an HCL interpolation —
	//   the dollar+brace anchors it to a real expression. Matches both
	//   the bare form and the substr() slice form (vpc connector / bastion
	//   service account budget cases) since both contain
	//   `random_id.suffix.hex` inside a `${...}` interpolation.
	randomIDBlockRe := regexp.MustCompile(`(?m)^\s*resource\s+"random_id"\s+"suffix"\s*\{`)
	suffixInterpRe := regexp.MustCompile(`\$\{[^}]*random_id\.suffix\.hex[^}]*\}`)

	for _, preset := range suffixedPresets {
		t.Run(preset, func(t *testing.T) {
			// (a) The preset declares the random required_provider. Use
			//     InspectPreset (not raw string match) so this fails with
			//     a clear "missing provider" diagnostic if versions.tf is
			//     malformed.
			mod, err := InspectPreset(preset)
			require.NoError(t, err, "InspectPreset(%q) failed", preset)
			require.Contains(t, mod.RequiredProviders, "random",
				"%s must declare a random = {...} required_provider (issue #159)", preset)

			// (b)+(c): read every .tf file in the preset and assert at
			// least one carries the random_id "suffix" block, and at least
			// one carries a ${random_id.suffix.hex} HCL interpolation.
			// Splitting (b)/(c) across files is fine — most modules keep
			// the resource block in main.tf; some keep providers in
			// versions.tf.
			entries, err := terraformpresets.FS.ReadDir(preset)
			require.NoError(t, err, "ReadDir(%q) failed", preset)

			var concatenated []byte
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
					continue
				}
				b, readErr := terraformpresets.FS.ReadFile(preset + "/" + e.Name())
				require.NoError(t, readErr, "ReadFile(%q/%q) failed", preset, e.Name())
				concatenated = append(concatenated, b...)
				concatenated = append(concatenated, '\n')
			}

			require.Regexp(t, randomIDBlockRe, string(concatenated),
				`%s must declare a top-level resource "random_id" "suffix" {} block (issue #159 — without it, retries after state loss 409 on the existing resource name)`,
				preset)
			require.Regexp(t, suffixInterpRe, string(concatenated),
				`%s must interpolate ${random_id.suffix.hex} into at least one HCL expression (issue #159 — a comment-only reference does not propagate the suffix to a resource name and would silently allow the original customer incident to recur)`,
				preset)
		})
	}
}

// TestGCPPresets_NoSharedRootRandomID pins the per-module-suffix design
// decision from issue #159: each GCP module declares its OWN random_id
// "suffix", rather than the composer hoisting a single shared random_id
// into the composed root. This matches the existing AWS convention (each
// AWS module that needs uniqueness declares its own random_id "suffix")
// and avoids requiring composer changes.
//
// If a future change hoists random_id into the composed root, suffix
// collisions and rotation semantics change in ways callers may not
// expect. That's a deliberate decision worth a separate review, not a
// drive-by edit.
func TestGCPPresets_NoSharedRootRandomID(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "gcp",
		// VPC + KMS + Firestore — three of the four customer-incident
		// modules in one composition (GCS isn't selectable as a default
		// component the same way; the per-module assertion in the sister
		// test above already covers it).
		SelectedKeys: []ComponentKey{KeyGCPVPC, KeyGCPCloudKMS, KeyGCPFirestore},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "suffix-test",
		GCPProjectID: "suffix-test-12345",
		Region:       "us-central1",
	})
	require.NoError(t, err)

	rootMain := string(out["/main.tf"])
	require.NotEmpty(t, rootMain, "composed root main.tf must exist")
	require.NotRegexp(t, regexp.MustCompile(`(?m)^\s*resource\s+"random_id"`), rootMain,
		"composed root main.tf must NOT declare a top-level random_id resource (issue #159: per-module suffix, not shared root). Each preset declares its own resource \"random_id\" \"suffix\" block; hoisting one to the root changes collision and rotation semantics.")
}

// TestComposeStack_GCPRandomIDSuffix smoke-tests the end-to-end provider
// merge path for the GCP/random case: composing a GCP stack with KMS
// must lift hashicorp/random into the composed root providers.tf so a
// `terraform init` on the result succeeds. The wider per-module contract
// is enforced by TestGCPPresets_AllSuffixedResourceNames; this test only
// covers the composer's role in stitching the modules together.
func TestComposeStack_GCPRandomIDSuffix(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPVPC, KeyGCPCloudKMS},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "suffix-test",
		GCPProjectID: "suffix-test-12345",
		Region:       "us-central1",
	})
	require.NoError(t, err, "ComposeStack(gcp) with KMS+VPC should succeed")

	// Root providers.tf must include the random provider so terraform init
	// can resolve it. Lock the merge location: hashicorp/random must
	// appear inside a `random = {...}` entry, not as a stray substring in
	// a comment or module name.
	prov := string(out["/providers.tf"])
	require.Contains(t, prov, "hashicorp/random",
		"root providers.tf should include the random provider lifted from the kms preset's required_providers")
	require.Regexp(t, regexp.MustCompile(`(?s)random\s*=\s*\{[^}]*hashicorp/random`), prov,
		"hashicorp/random should be attached to a random = {...} entry, not just appear as a substring")
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

// TestComposeStack_PolymorphicKeyPullsInPrefixedVPC is a regression test for
// the implicit-dependency leak where ResolveDependencies would have expanded
// a polymorphic key to a legacy VPC sibling. A direct Go caller passing only
// [KeyAWSEKSControlPlane] must still produce a prefixed-only stack.
//
// Post-#206: the comps-aware resolver also auto-includes the worker node group
// (module "ec2") when comps is non-Lambda, so the assertion set covers that.
func TestComposeStack_PolymorphicKeyPullsInPrefixedVPC(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSEKSControlPlane}, // polymorphic, pulls VPC via ImplicitDependencies
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack with polymorphic KeyAWSEKSControlPlane should succeed")

	mainTF := string(out["/main.tf"])
	require.Contains(t, mainTF, `module "aws_vpc"`,
		"implicit dep must render aws_vpc")
	require.Contains(t, mainTF, `module "resource"`,
		"polymorphic KeyAWSEKSControlPlane preserves its string value and renders as module.resource")
	require.Contains(t, mainTF, `module "ec2"`,
		"non-Lambda EKS must auto-include the worker node group (issue #206)")
}

// TestComposeStack_EKSAutoIncludesNodeGroup is the issue #206 regression: a
// caller selecting only KeyAWSEKS (or KeyAWSEKSControlPlane) on a non-Lambda
// architecture must end up with the worker node group module composed,
// otherwise EKS addons (coredns, kube-proxy, aws-ebs-csi-driver) have nowhere
// to schedule and `terraform apply` times out at 20 min in DEGRADED state.
//
// The expected cluster-module name depends on which cluster key the caller
// selected: KeyAWSEKS ("aws_eks") composes module "aws_eks", and
// KeyAWSEKSControlPlane ("resource") composes module "resource". The
// auto-include must emit *exactly one* cluster module — not both — so that
// the prefix-aware key path doesn't drag the legacy polymorphic key in via
// the node group's static dep.
func TestComposeStack_EKSAutoIncludesNodeGroup(t *testing.T) {
	t.Parallel()
	awsEKSEnabled := true
	cases := []struct {
		name        string
		selected    []ComponentKey
		comps       *Components
		wantCluster string // module name expected for the EKS control plane
		notCluster  string // module name that must NOT be present
	}{
		{"OnlyEKS_AutoAddsNodeGroup", []ComponentKey{KeyAWSEKS}, &Components{}, `module "aws_eks"`, `module "resource"`},
		{"OnlyControlPlane_AutoAddsNodeGroup", []ComponentKey{KeyAWSEKSControlPlane}, &Components{}, `module "resource"`, `module "aws_eks"`},
		{"EKSPlusVPC_AutoAddsNodeGroup", []ComponentKey{KeyAWSEKS, KeyAWSVPC}, &Components{}, `module "aws_eks"`, `module "resource"`},
		// Non-empty non-Lambda comps confirms isLambda returns false on a
		// populated Components{} where AWSLambda is unset — guards against a
		// regression where isLambda might check non-nil instead of the field.
		{"EKSWithEnabledFlag_NonEmptyComps", []ComponentKey{KeyAWSEKS}, &Components{AWSEKS: &awsEKSEnabled}, `module "aws_eks"`, `module "resource"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient()
			out, err := c.ComposeStack(ComposeStackOpts{
				Cloud:        "aws",
				SelectedKeys: tc.selected,
				Comps:        tc.comps,
				Cfg:          &Config{Region: "us-east-1"},
				Project:      "eks-autoinclude",
				Region:       "us-east-1",
			})
			require.NoError(t, err, "ComposeStack should succeed")
			mainTF := string(out["/main.tf"])
			require.Contains(t, mainTF, `module "aws_vpc"`)
			require.Contains(t, mainTF, tc.wantCluster,
				"EKS control plane module must compose")
			require.NotContains(t, mainTF, tc.notCluster,
				"only one cluster module must compose — the auto-include must not pull in the other polymorphic alias")
			// Match the block-opening shape, not just a substring, so a
			// rename of the node-group module wouldn't accidentally credit
			// (e.g.) a "module \"ec2_legacy_workers\"" occurrence. Pins the
			// composed name "ec2" and asserts the source path resolves to
			// the eks_nodegroup preset, which is what actually keeps EKS
			// schedulable per #206.
			require.Regexp(t, regexp.MustCompile(`(?m)^module "ec2"\s*\{`), mainTF,
				"EKS worker node group (polymorphic key 'ec2') must auto-include for non-Lambda EKS (issue #206)")
			ec2Blocks := regexp.MustCompile(`(?m)^module "ec2"\s*\{`).FindAllStringIndex(mainTF, -1)
			require.Len(t, ec2Blocks, 1,
				"node group must compose exactly once — auto-include must not duplicate when user pre-selects KeyAWSEKSNodeGroup")
			require.Regexp(t, regexp.MustCompile(`source\s*=\s*"\./modules/eks_nodegroup"`), mainTF,
				"node group module's source must resolve to ./modules/eks_nodegroup")
		})
	}
}

// TestComposeStack_EKSAutoIncludeIdempotent pins ResolveDependenciesForCompose's
// hasNodeGroup short-circuit: when the caller already selected
// KeyAWSEKSNodeGroup explicitly, the auto-include must not append a second
// copy. A mutation removing the hasNodeGroup check would emit two
// `module "ec2"` blocks, which terraform would reject at init.
//
// Note: pre-existing static-map behavior emits two cluster modules
// (`module "aws_eks"` AND `module "resource"`) when both KeyAWSEKS and
// KeyAWSEKSNodeGroup are selected explicitly, because the static map declares
// KeyAWSEKSNodeGroup's dep as KeyAWSEKSControlPlane (the legacy polymorphic
// alias). That's a separate latent issue outside #206's scope and is not
// asserted here — this test pins only the node-group-not-doubled invariant.
func TestComposeStack_EKSAutoIncludeIdempotent(t *testing.T) {
	t.Parallel()
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSEKS, KeyAWSEKSNodeGroup},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "eks-idempotent",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack should succeed")
	mainTF := string(out["/main.tf"])
	ec2Blocks := regexp.MustCompile(`(?m)^module "ec2"\s*\{`).FindAllStringIndex(mainTF, -1)
	require.Len(t, ec2Blocks, 1,
		"node group must compose exactly once even when caller pre-selects KeyAWSEKSNodeGroup — pins the hasNodeGroup short-circuit in ResolveDependenciesForCompose")
}

// TestComposeStack_EKSControlPlaneLambdaSkipsNodeGroup pins the Lambda gate:
// when comps.AWSLambda is set, KeyAWSEKSControlPlane routes to the Lambda
// runtime preset (not EKS), so the auto-include of the worker node group
// must NOT fire.
func TestComposeStack_EKSControlPlaneLambdaSkipsNodeGroup(t *testing.T) {
	t.Parallel()
	trueVal := true
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSEKSControlPlane},
		Comps:        &Components{AWSLambda: &trueVal}, // Lambda architecture
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "lambda-no-eks",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack(Lambda) should succeed")
	mainTF := string(out["/main.tf"])
	require.Contains(t, mainTF, `module "resource"`,
		"polymorphic KeyAWSEKSControlPlane still composes as module 'resource'")
	require.NotContains(t, mainTF, `module "ec2"`,
		"Lambda architecture must NOT auto-include the EKS worker node group (issue #206)")
}

// TestDefaultWiring_GCPSubnetSelfLinkUsesTryGuard pins the issue #178 fix:
// every GCP module that consumes the VPC's subnets_self_links output must do
// so via try(module.gcp_vpc.subnet_self_links[0], null), not the raw [0]
// index. The raw form errors with "Invalid index ... empty tuple" on first
// terraform plan against an empty state and surfaces as a stage_error in
// Oracle's custom-stack-provision pipeline.
//
// This test exercises every module that consumes subnet_self_link as a
// scalar wiring input; if a future caller is added without the try() guard
// the test fails before the regression reaches a customer.
func TestDefaultWiring_GCPSubnetSelfLinkUsesTryGuard(t *testing.T) {
	t.Parallel()
	const wantExpr = "try(module.gcp_vpc.subnet_self_links[0], null)"
	const rawForm = "module.gcp_vpc.subnet_self_links[0]"

	selected := map[ComponentKey]bool{
		KeyGCPVPC:          true,
		KeyGCPGKE:          true,
		KeyGCPLoadbalancer: true,
		KeyGCPCompute:      true,
		KeyGCPBastion:      true,
	}
	consumers := []ComponentKey{
		KeyGCPGKE, KeyGCPLoadbalancer, KeyGCPCompute, KeyGCPBastion,
	}
	for _, key := range consumers {
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()
			wi := DefaultWiring(selected, key, &Components{})
			got, ok := wi.RawHCL["subnet_self_link"]
			require.True(t, ok, "%s must wire subnet_self_link from gcp_vpc", key)
			require.Equal(t, wantExpr, got,
				"%s subnet_self_link must use try() guard (issue #178); raw [0] errors on first plan with empty state", key)
			require.NotContains(t, got, rawForm+"\n",
				"%s must not emit unguarded [0] index", key)
		})
	}
}

// TestComposeStack_GCPSubnetSelfLinkInGeneratedHCL is the integration check:
// a real ComposeStack run against each GCP+VPC consumer combo must emit the
// try() expression in the composed main.tf and never the raw [0] form
// outside that try(). Targets issue #178 acceptance criteria: composed HCL
// against an empty state must not contain unguarded [0] indices on the
// VPC's subnets_self_links output.
func TestComposeStack_GCPSubnetSelfLinkInGeneratedHCL(t *testing.T) {
	// Match `try(module.gcp_vpc.subnet_self_links[0], null)` exactly.
	guardedRE := regexp.MustCompile(`try\(\s*module\.gcp_vpc\.subnet_self_links\[0\]\s*,\s*null\s*\)`)
	// Match the unguarded substring (with bracketed [0]). Used to count
	// raw occurrences and subtract guarded matches; any leftover is an
	// unguarded bug.
	rawSubstr := "module.gcp_vpc.subnet_self_links[0]"

	consumerCombos := []struct {
		name string
		keys []ComponentKey
	}{
		{"gke", []ComponentKey{KeyGCPVPC, KeyGCPGKE}},
		{"loadbalancer", []ComponentKey{KeyGCPVPC, KeyGCPLoadbalancer}},
		{"compute", []ComponentKey{KeyGCPVPC, KeyGCPCompute}},
		{"bastion", []ComponentKey{KeyGCPVPC, KeyGCPBastion}},
	}
	for _, combo := range consumerCombos {
		t.Run(combo.name, func(t *testing.T) {
			c := newTestClient()
			out, err := c.ComposeStack(ComposeStackOpts{
				Cloud:        "gcp",
				SelectedKeys: combo.keys,
				Comps:        &Components{},
				Cfg:          &Config{Region: "us-central1"},
				Project:      "test",
				Region:       "us-central1",
				GCPProjectID: "test-project-12345",
			})
			require.NoError(t, err)

			mainTF := string(out["/main.tf"])
			require.True(t, guardedRE.MatchString(mainTF),
				"%s: composed main.tf must contain try(module.gcp_vpc.subnet_self_links[0], null) (issue #178)", combo.name)

			for name, body := range out {
				if !strings.HasSuffix(name, ".tf") {
					continue
				}
				s := string(body)
				rawCount := strings.Count(s, rawSubstr)
				guardedCount := len(guardedRE.FindAllString(s, -1))
				require.Equal(t, rawCount, guardedCount,
					"%s/%s: every occurrence of %q must be inside try(..., null); raw=%d guarded=%d (issue #178)",
					combo.name, name, rawSubstr, rawCount, guardedCount)
			}
		})
	}
}

// TestComposeStack_GCPRouterAndConnectorOutputs_TerraformValidate runs
// `terraform validate` on freshly composed GCP+VPC stacks (multiple
// consumer combos) to formally close issue #178's acceptance criterion:
// composed HCL must validate against an empty state with no
// "Invalid index" / "empty tuple" errors.
//
// Behavior on init failure differs by environment:
//
//   - In CI (env CI=true, set by GitHub Actions): require the test runs.
//     A `terraform init` failure is a hard test failure so a transient
//     registry blip can never silently skip the gate that's the formal
//     closure for #178.
//   - Local dev (no CI env): skip on init failure so a developer offline
//     can still run `go test ./...` without the gate forcing a network
//     round-trip for every test invocation.
//
// Use `go test -short` to skip the test entirely (e.g. in pre-commit
// hooks where the multi-second init+validate is too costly).
func TestComposeStack_GCPRouterAndConnectorOutputs_TerraformValidate(t *testing.T) {
	if testing.Short() {
		t.Skip("-short skips multi-second terraform init+validate")
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform binary not on PATH; skipping integration validate")
	}

	inCI := os.Getenv("CI") == "true"

	// Multiple subset combos catch a regression that only manifests
	// outside the kitchen-sink stack — e.g. a wiring rule that fires
	// only when one consumer is paired with the VPC. The subset combos
	// also exercise enable_cloud_nat / enable_serverless_connector
	// independently across stacks.
	combos := []struct {
		name string
		keys []ComponentKey
	}{
		{"vpc+compute alone", []ComponentKey{KeyGCPVPC, KeyGCPCompute}},
		{"vpc+gke+loadbalancer+compute+bastion", []ComponentKey{KeyGCPVPC, KeyGCPGKE, KeyGCPLoadbalancer, KeyGCPCompute, KeyGCPBastion}},
	}

	for _, combo := range combos {
		t.Run(combo.name, func(t *testing.T) {
			c := newTestClient()
			out, err := c.ComposeStack(ComposeStackOpts{
				Cloud:        "gcp",
				SelectedKeys: combo.keys,
				Comps:        &Components{},
				Cfg:          &Config{Region: "us-central1"},
				Project:      "test",
				Region:       "us-central1",
				GCPProjectID: "test-project-12345",
			})
			require.NoError(t, err)

			dir := t.TempDir()
			writeOutputs(t, out, dir)

			initCmd := exec.Command("terraform", "init", "-backend=false", "-input=false", "-no-color")
			initCmd.Dir = dir
			initOut, err := initCmd.CombinedOutput()
			if err != nil {
				if inCI {
					require.NoError(t, err,
						"terraform init must succeed in CI; this gate is the formal closure for issue #178 and must not silently skip on transient registry failures:\n%s", initOut)
				}
				t.Skipf("terraform init unavailable (network/cache) in local dev: %s\n%s", err, initOut)
			}
			validateCmd := exec.Command("terraform", "validate", "-no-color")
			validateCmd.Dir = dir
			validateOut, err := validateCmd.CombinedOutput()
			require.NoError(t, err, "terraform validate must succeed on composed stack (issue #178):\n%s", validateOut)
			require.NotContains(t, string(validateOut), "Invalid index",
				"terraform validate surfaced 'Invalid index' (issue #178 regression)")
			require.NotContains(t, string(validateOut), "empty tuple",
				"terraform validate surfaced 'empty tuple' (issue #178 regression)")
		})
	}
}

// TestComposeStack_AWS_PublicVPC_TerraformValidate is the AWS analogue of
// the GCP issue-#178 closure above. It composes representative AWS stacks
// — including the #389 bug shape (Public VPC + no private-subnet consumers
// + stale cfg.AWSVPC.EnableNATGateway=true) — and runs `terraform init &&
// terraform validate` on the composed root. Closes the parity gap that
// let #389 reach apply time: prior to this test, AWS-side composer output
// was only HCL-parsed by compose_stack_test.go:780, never validated.
//
// The bug shape's expected behavior here is "validates clean": Layer 1a
// in the mapper coerces enable_nat_gateway=false in the emitted tfvars,
// so even though the caller's cfg is stale, the validate gate sees the
// coerced inputs and passes. To reproduce the #389 apply failure in
// validate, revert just the mapper coercion (validate will surface the
// "element() on empty list" / precondition error).
//
// Behavior on init failure mirrors the GCP test: hard-fail in CI, skip
// locally so offline `go test ./...` still runs.
func TestComposeStack_AWS_PublicVPC_TerraformValidate(t *testing.T) {
	if testing.Short() {
		t.Skip("-short skips multi-second terraform init+validate")
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform binary not on PATH; skipping integration validate")
	}

	inCI := os.Getenv("CI") == "true"
	boolPtr := func(v bool) *bool { return &v }
	awsVPCCfg := func(enable *bool) *Config {
		c := &Config{Region: "us-east-1"}
		c.AWSVPC = &struct {
			SingleNATGateway *bool `json:"singleNatGateway,omitempty"`
			EnableNATGateway *bool `json:"enableNatGateway,omitempty"`
			AZCount          *int  `json:"azCount,omitempty"`
		}{EnableNATGateway: enable}
		return c
	}

	combos := []struct {
		name  string
		keys  []ComponentKey
		comps *Components
		cfg   *Config
	}{
		{
			// The #389 minimal repro. Without the Layer 1a mapper coercion,
			// `terraform validate` would surface either the precondition
			// fail (Layer 2 backstop) or the upstream's empty-tuple error.
			name: "Public VPC with stale EnableNATGateway=true (#389 bug shape)",
			keys: []ComponentKey{KeyAWSVPC, KeyAWSS3, KeyAWSKMS, KeyAWSLambda, KeyAWSSecretsManager, KeyAWSCloudWatchLogs},
			comps: &Components{
				Cloud:             "AWS",
				AWSS3:             boolPtr(true),
				AWSKMS:            boolPtr(true),
				AWSVPC:            "Public VPC",
				AWSLambda:         boolPtr(true),
				Architecture:      "Serverless",
				AWSSecretsManager: boolPtr(true),
				AWSCloudWatchLogs: boolPtr(true),
			},
			cfg: awsVPCCfg(boolPtr(true)),
		},
		{
			// Symmetry: Public VPC with EKS (private subnets still needed)
			// must validate clean — the override stays legitimate.
			name: "Public VPC + EKS keeps private subnets + NAT",
			keys: []ComponentKey{KeyAWSVPC, KeyAWSEKS},
			comps: &Components{
				Cloud:        "AWS",
				AWSVPC:       "Public VPC",
				AWSEKS:       boolPtr(true),
				Architecture: "Kubernetes",
			},
			cfg: awsVPCCfg(boolPtr(true)),
		},
		{
			// Baseline: Private VPC with defaults (the "happy path" any
			// future regression would also have to keep passing).
			name: "Private VPC with defaults",
			keys: []ComponentKey{KeyAWSVPC, KeyAWSS3, KeyAWSLambda},
			comps: &Components{
				Cloud:        "AWS",
				AWSVPC:       "Private VPC",
				AWSS3:        boolPtr(true),
				AWSLambda:    boolPtr(true),
				Architecture: "Serverless",
			},
			cfg: &Config{Region: "us-east-1"},
		},
	}

	for _, combo := range combos {
		t.Run(combo.name, func(t *testing.T) {
			c := newTestClient()
			out, err := c.ComposeStack(ComposeStackOpts{
				Cloud:        "aws",
				SelectedKeys: combo.keys,
				Comps:        combo.comps,
				Cfg:          combo.cfg,
				Project:      "test-389",
				Region:       "us-east-1",
			})
			require.NoError(t, err)

			dir := t.TempDir()
			writeOutputs(t, out, dir)

			initCmd := exec.Command("terraform", "init", "-backend=false", "-input=false", "-no-color")
			initCmd.Dir = dir
			initOut, err := initCmd.CombinedOutput()
			if err != nil {
				if inCI {
					require.NoError(t, err,
						"terraform init must succeed in CI; this gate is the AWS analogue of issue #178/closure for #389 and must not silently skip on transient registry failures:\n%s", initOut)
				}
				t.Skipf("terraform init unavailable (network/cache) in local dev: %s\n%s", err, initOut)
			}
			validateCmd := exec.Command("terraform", "validate", "-no-color")
			validateCmd.Dir = dir
			validateOut, err := validateCmd.CombinedOutput()
			require.NoError(t, err, "terraform validate must succeed on composed stack (issue #389):\n%s", validateOut)
			// The #389 deploy-time symptom — surface here so a future
			// mapper regression that drops the coercion is named
			// precisely instead of just "validate failed".
			require.NotContains(t, string(validateOut), "empty tuple",
				"terraform validate surfaced 'empty tuple' — #389 regression: composer emitted enable_nat_gateway=true with private subnets disabled")
			require.NotContains(t, string(validateOut), "element function",
				"terraform validate surfaced 'element function' — #389 regression: NAT route attached to empty private route table")
			require.NotContains(t, string(validateOut), "Resource precondition failed",
				"terraform validate surfaced a precondition failure — Layer 2 backstop fired, meaning the Layer 1a mapper coercion regressed")
		})
	}
}

// TestComposeStack_GCPCloudKMS_TerraformPlan is the formal closure
// for issue #182's acceptance criterion: a fresh GCP session that
// selects gcp/kms with default config (and with non-default
// iam_bindings) must **plan** cleanly on first run with empty state.
// PR #181 surgically wrapped the upstream's broken slice() in try()
// to protect default consumers but left a hole when iam_bindings is
// non-empty; #182 replaced the upstream entirely with direct
// google_kms_* resources keyed by for_each, closing the hole.
//
// `terraform plan -refresh=false` (NOT `terraform validate`) is the
// right gate here: the upstream's failure mode is a `slice()` end-
// index error during expression evaluation, which `validate` does
// NOT exercise — `validate` only checks HCL syntax + type
// conformance + attribute existence, not expression evaluation. A
// regression that re-vendors the upstream module would slip past a
// validate-only gate but be caught by plan. (The sibling
// TestComposeStack_GCPRouterAndConnectorOutputs_TerraformValidate
// uses validate because issue #178's failure mode is `Invalid
// index` on tuple access, which validate DOES surface — different
// failure mode, different gate.)
//
// `-refresh=false` skips the credential-requiring provider refresh
// (this test runs offline against an empty state with no real GCP
// project) — the failure mode we're guarding against fires during
// expression evaluation in plan, well before any provider call.
//
// CI vs local skip rules match the router/connector test:
//   - In CI (CI=true): terraform init failure is a hard failure (the
//     gate must not silently skip on transient registry blips).
//   - Local dev: skip on init failure so offline `go test ./...` works.
//
// Use `go test -short` to skip entirely (e.g. in pre-commit hooks).
func TestComposeStack_GCPCloudKMS_TerraformPlan(t *testing.T) {
	if testing.Short() {
		t.Skip("-short skips multi-second terraform init+plan")
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform binary not on PATH; skipping integration plan")
	}

	inCI := os.Getenv("CI") == "true"

	// Two combos: kms alone (the leaf default), and kms with a
	// non-empty iam_bindings shape that the composer surfaces. The
	// composer mapper does not currently surface var.iam_bindings as
	// a Component-driven knob, so the second combo writes an extra
	// kms_iam_bindings.auto.tfvars after composition to inject the
	// binding — faithful to what a InsideOut-inspector-side mapper would emit
	// and to what a real customer's stack would look like. The
	// plan-time resolution of that binding is exactly the case PR
	// #181 could not protect.
	combos := []struct {
		name        string
		extraTFVars string
	}{
		{"kms alone with default config", ""},
		{
			"kms with non-empty iam_bindings",
			`kms_iam_bindings = [
  {
    key_name = "default"
    role     = "roles/cloudkms.cryptoKeyEncrypter"
    members  = ["serviceAccount:test@test-project-12345.iam.gserviceaccount.com"]
  },
]
`,
		},
	}

	for _, combo := range combos {
		t.Run(combo.name, func(t *testing.T) {
			c := newTestClient()
			out, err := c.ComposeStack(ComposeStackOpts{
				Cloud:        "gcp",
				SelectedKeys: []ComponentKey{KeyGCPCloudKMS},
				Comps:        &Components{},
				Cfg:          &Config{Region: "us-central1"},
				Project:      "test",
				Region:       "us-central1",
				GCPProjectID: "test-project-12345",
			})
			require.NoError(t, err)

			dir := t.TempDir()
			writeOutputs(t, out, dir)

			if combo.extraTFVars != "" {
				require.NoError(t,
					os.WriteFile(filepath.Join(dir, "kms_iam_bindings.auto.tfvars"), []byte(combo.extraTFVars), 0o600),
					"write extra tfvars")
			}

			initCmd := exec.Command("terraform", "init", "-backend=false", "-input=false", "-no-color")
			initCmd.Dir = dir
			initOut, err := initCmd.CombinedOutput()
			if err != nil {
				if inCI {
					require.NoError(t, err,
						"terraform init must succeed in CI; this gate is the formal closure for issue #182 and must not silently skip on transient registry failures:\n%s", initOut)
				}
				t.Skipf("terraform init unavailable (network/cache) in local dev: %s\n%s", err, initOut)
			}
			planCmd := exec.Command("terraform", "plan", "-refresh=false", "-input=false", "-no-color")
			planCmd.Dir = dir
			// The google provider's Configure() step tries to load
			// Application Default Credentials even when -refresh=false
			// suppresses live API calls. Inject a fake OAuth token so
			// the provider initializes cleanly in CI (where ADC is
			// unavailable). The test never reaches a real GCP call.
			planCmd.Env = append(os.Environ(), "GOOGLE_OAUTH_ACCESS_TOKEN=ya29.test-token-not-real")
			planOut, err := planCmd.CombinedOutput()
			require.NoError(t, err, "terraform plan must succeed on composed gcp/kms stack (issue #182 — formal closure for the slice end-index failure):\n%s", planOut)
			// Belt-and-braces: even if plan exits 0, surface the
			// upstream's specific error string in case a future
			// terraform version downgrades the failure to a
			// warning instead of an error.
			require.NotContains(t, string(planOut), "slice end_index",
				"terraform plan surfaced 'slice end_index' (issue #182 regression — upstream module re-introduced)")
			require.NotContains(t, string(planOut), "Invalid index",
				"terraform plan surfaced 'Invalid index' (related #178 regression)")
		})
	}
}

// TestComposeStack_GCPCloudKMS_MovedBlocksRebindUpstreamState exercises
// the issue #182 `moved {}` blocks against a synthetic state file
// that mimics what an existing default-config customer's state looks
// like AFTER the old upstream-vendoring config was applied — i.e. the
// state contains `module.gcp_cloud_kms.module.kms.google_kms_key_ring.key_ring`
// and `.google_kms_crypto_key.key[0]`. The composer wraps the kms
// preset as `module "gcp_cloud_kms"` (named after the ComponentKey),
// and inside the wrapper preset the old upstream vendoring was
// `module "kms" { source = "terraform-google-modules/kms/google" }` —
// hence the doubled `module.gcp_cloud_kms.module.kms.` prefix in
// real customer state. Without the moved {} blocks, terraform would
// see those addresses as orphans (wrapper module no longer declares
// the inner `module "kms"`) and plan to destroy them.
//
// The test composes the stack, writes the state file as
// `terraform.tfstate`, runs `terraform init`, then `terraform plan
// -refresh=false` and asserts:
//
//   - plan succeeds (the moved blocks parse and the source addresses
//     in state are recognized).
//   - plan output explicitly acknowledges the moved-block rebinding
//     ("has moved from" is terraform's standard phrasing — a moved
//     block whose source address is absent from state silently
//     becomes a no-op, so the acknowledgment string is the proof
//     that the rebind actually fired).
//   - plan output does NOT contain a destroy plan for the old
//     upstream addresses (which would mean the rebind failed and
//     terraform sees the old resources as orphaned).
//
// `-refresh=false` skips the credential-requiring provider refresh
// — the synthetic state has bare-bones attributes that wouldn't
// match a real GCP read.
//
// Limitations: the synthetic state intentionally omits
// `random_id.suffix` and uses a baked keyring name that won't match
// a fresh re-deploy's `random_id.suffix.hex`. As a result, `plan`
// will also propose creating a NEW `random_id.suffix` (and possibly
// re-creating the keyring under the new name). This is fine for
// what the test asserts — the moved-block acknowledgment fires
// before any attribute-drift comparison. A test that asserts "0
// resources to destroy" would require synthesizing the random_id
// state too AND interpolating its hex into the keyring name; the
// test's contract is "moved blocks rebind correctly," not "default
// config produces a no-op upgrade plan" (the latter is the manual
// sandbox-apply gate documented in the PR).
func TestComposeStack_GCPCloudKMS_MovedBlocksRebindUpstreamState(t *testing.T) {
	if testing.Short() {
		t.Skip("-short skips multi-second terraform init+plan")
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform binary not on PATH; skipping integration plan")
	}

	inCI := os.Getenv("CI") == "true"

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPCloudKMS},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "test",
		Region:       "us-central1",
		GCPProjectID: "test-project-12345",
	})
	require.NoError(t, err)

	dir := t.TempDir()
	writeOutputs(t, out, dir)

	// Synthetic state: mimics an existing customer's default-config
	// stack as it was last applied with the pre-#182 upstream
	// vendoring. Includes:
	//   - random_id.suffix (in the wrapper at module.gcp_cloud_kms)
	//     with a known hex value of "deadbeef" so the keyring name
	//     `${var.project}-${var.keyring_name}-${random_id.suffix.hex}`
	//     matches what's already in state; without this the plan
	//     would regenerate the random_id and trigger a force-new
	//     on the keyring name (then chain to a prevent_destroy
	//     error on the crypto_key).
	//   - google_kms_key_ring at the doubly-prefixed
	//     module.gcp_cloud_kms.module.kms.* address.
	//   - google_kms_crypto_key.key[0] (count-indexed in the
	//     upstream — `each` field is omitted since count-indexed
	//     resources don't have it).
	//
	// Refresh=false skips the live read so any drift from real GCP
	// schemas is irrelevant.
	state := `{
  "version": 4,
  "terraform_version": "1.5.0",
  "serial": 1,
  "lineage": "00000000-0000-0000-0000-000000000182",
  "outputs": {},
  "resources": [
    {
      "module": "module.gcp_cloud_kms",
      "mode": "managed",
      "type": "random_id",
      "name": "suffix",
      "provider": "provider[\"registry.terraform.io/hashicorp/random\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "b64_std": "3q2+7w==",
            "b64_url": "3q2-7w",
            "byte_length": 4,
            "dec": "3735928559",
            "hex": "deadbeef",
            "id": "3q2-7w",
            "keepers": null,
            "prefix": null
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "module": "module.gcp_cloud_kms.module.kms",
      "mode": "managed",
      "type": "google_kms_key_ring",
      "name": "key_ring",
      "provider": "provider[\"registry.terraform.io/hashicorp/google\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "id": "projects/test-project-12345/locations/us-central1/keyRings/test-main-keyring-deadbeef",
            "location": "us-central1",
            "name": "test-main-keyring-deadbeef",
            "project": "test-project-12345",
            "timeouts": null
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "module": "module.gcp_cloud_kms.module.kms",
      "mode": "managed",
      "type": "google_kms_crypto_key",
      "name": "key",
      "provider": "provider[\"registry.terraform.io/hashicorp/google\"]",
      "instances": [
        {
          "index_key": 0,
          "schema_version": 1,
          "attributes": {
            "id": "projects/test-project-12345/locations/us-central1/keyRings/test-main-keyring-deadbeef/cryptoKeys/default",
            "key_ring": "projects/test-project-12345/locations/us-central1/keyRings/test-main-keyring-deadbeef",
            "name": "default",
            "purpose": "ENCRYPT_DECRYPT",
            "rotation_period": "7776000s",
            "labels": {},
            "import_only": false,
            "skip_initial_version_creation": false,
            "destroy_scheduled_duration": "86400s",
            "version_template": [
              {
                "algorithm": "GOOGLE_SYMMETRIC_ENCRYPTION",
                "protection_level": "SOFTWARE"
              }
            ],
            "timeouts": null
          },
          "sensitive_attributes": []
        }
      ]
    }
  ],
  "check_results": null
}
`
	require.NoError(t,
		os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(state), 0o600),
		"write synthetic state")

	initCmd := exec.Command("terraform", "init", "-backend=false", "-input=false", "-no-color")
	initCmd.Dir = dir
	initOut, err := initCmd.CombinedOutput()
	if err != nil {
		if inCI {
			require.NoError(t, err,
				"terraform init must succeed in CI; this gate is the moved-blocks pin for #182:\n%s", initOut)
		}
		t.Skipf("terraform init unavailable (network/cache) in local dev: %s\n%s", err, initOut)
	}

	planCmd := exec.Command("terraform", "plan", "-refresh=false", "-input=false", "-no-color")
	planCmd.Dir = dir
	// See sibling TestComposeStack_GCPCloudKMS_TerraformPlan: the
	// google provider's Configure() requires SOME credential source,
	// even with -refresh=false. Inject a fake token so CI (no ADC)
	// can initialize the provider; the test never makes a real call.
	planCmd.Env = append(os.Environ(), "GOOGLE_OAUTH_ACCESS_TOKEN=ya29.test-token-not-real")
	planOut, err := planCmd.CombinedOutput()
	require.NoError(t, err, "terraform plan must succeed against synthetic upstream-state (issue #182 moved-blocks gate):\n%s", planOut)

	// Each moved block's source address must trigger terraform's
	// rebind acknowledgment. A moved block whose source address is
	// absent from state silently becomes a no-op, so this string
	// is the proof that the rebind actually fired.
	planStr := string(planOut)
	for _, want := range []string{
		`module.gcp_cloud_kms.module.kms.google_kms_key_ring.key_ring`,
		`module.gcp_cloud_kms.module.kms.google_kms_crypto_key.key[0]`,
	} {
		require.Contains(t, planStr, want,
			"terraform plan output must reference the old upstream address `%s` as the moved-block source — a missing reference means the moved block silently became a no-op and customers' state will NOT migrate (issue #182):\n%s",
			want, planStr)
	}
	// Terraform uses two interchangeable phrasings for moved-block
	// acknowledgment in plan output:
	//   - `has moved to <new_address>` (when the resource has no
	//     other changes — pure address rebind).
	//   - `(moved from <old_address>)` (when the resource has other
	//     changes too — e.g. computed attributes added by a newer
	//     provider version).
	// Either is proof the moved block fired. Two moved sources in
	// state → at least two acknowledgments combined.
	movedCount := strings.Count(planStr, "has moved to") + strings.Count(planStr, "moved from")
	require.GreaterOrEqual(t, movedCount, 2,
		"terraform plan output must contain at least 2 moved-block acknowledgments combined (`has moved to` + `moved from`); got %d. Fewer means moved blocks aren't rebinding (issue #182):\n%s", movedCount, planStr)

	// The strongest assertion: 0 destroys. If the moved blocks fully
	// rebound the synthetic state, terraform should not plan to
	// destroy anything. (Updates-in-place are fine — they reflect
	// computed-attribute additions in the new provider version, not
	// a re-creation of the keyring or crypto_key.)
	require.Contains(t, planStr, "0 to destroy",
		"terraform plan must show 0 destroys after moved-block rebind — a non-zero destroy count means the moved block didn't catch the source address and customers' state would lose the resource on apply (issue #182):\n%s", planStr)

	// Belt-and-braces: the slice() failure mode must not surface
	// even with state present (a regression that re-vendored the
	// upstream would fire here too, possibly with attribute-shape
	// errors layered on top).
	require.NotContains(t, planStr, "slice end_index",
		"terraform plan surfaced 'slice end_index' (issue #182 regression):\n%s", planStr)
}
