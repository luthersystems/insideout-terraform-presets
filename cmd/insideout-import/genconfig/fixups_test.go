package genconfig

import (
	"regexp"
	"strings"
	"testing"
)

// TestFixupLambda_NullSourceAttrsTreatedAsMissing pins the real-world
// shape live AWS produces: terraform plan -generate-config-out emits
// `filename = null`, `image_uri = null`, `s3_bucket = null` for an
// imported Lambda (the attrs exist in the schema but carry no value at
// import time). The fixup must treat null-valued attributes as missing
// and inject a placeholder anyway. A naive `body.GetAttribute(name) != nil`
// check passes here even though no usable source is present — so this
// test is the one that pins the difference.
func TestFixupLambda_NullSourceAttrsTreatedAsMissing(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "fn" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = null
  image_uri     = null
  s3_bucket     = null
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*filename\s*=\s*"lambda_placeholder\.zip"`).MatchString(got) {
		t.Errorf("null-valued source attrs must be treated as missing; placeholder must be injected\n--- got ---\n%s", got)
	}
}

// TestFixupLambda_NoSourceInjectsPlaceholderAndIgnore pins the contract:
// when generate-config-out produced a Lambda block missing all three
// AtLeastOneOf source attrs, the fixup injects `filename =
// "lambda_placeholder.zip"` and a `lifecycle { ignore_changes = [...] }`
// block covering every source-shaped attribute. Without both halves of
// this fix, terraform validate fails for every imported Lambda — the
// real-world live-smoke regression that motivated this code.
func TestFixupLambda_NoSourceInjectsPlaceholderAndIgnore(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "fn" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*filename\s*=\s*"lambda_placeholder\.zip"`).MatchString(got) {
		t.Errorf("placeholder filename not injected\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "lifecycle") || !strings.Contains(got, "ignore_changes") {
		t.Errorf("lifecycle.ignore_changes block not added\n--- got ---\n%s", got)
	}
	for _, want := range lambdaIgnoreChanges {
		if !strings.Contains(got, want) {
			t.Errorf("ignore_changes missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFixupLambda_ExistingFilenameNotOverwritten pins a friendly-fire
// guard: if the operator (or a future generate-config-out) does emit
// `filename`, the fixup must not clobber it — only the ignore_changes
// pin gets added. Otherwise an apply against the stack would re-upload
// whatever the placeholder points at, defeating the purpose.
func TestFixupLambda_ExistingFilenameNotOverwritten(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "fn" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = "real_code.zip"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*filename\s*=\s*"real_code\.zip"`).MatchString(got) {
		t.Errorf("operator-supplied filename was clobbered\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "lambda_placeholder.zip") {
		t.Errorf("placeholder injected over existing filename\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "ignore_changes") {
		t.Errorf("ignore_changes pin missing\n--- got ---\n%s", got)
	}
}

// TestFixupLambda_ImageURIAlsoSatisfiesSource pins symmetry with
// container-Lambda: the AtLeastOneOf gate is satisfied by any of
// {filename, image_uri, s3_bucket}, so a Lambda already declaring
// image_uri must NOT have a placeholder filename injected.
func TestFixupLambda_ImageURIAlsoSatisfiesSource(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "fn" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  package_type  = "Image"
  image_uri     = "123.dkr.ecr.us-east-1.amazonaws.com/foo:latest"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "lambda_placeholder.zip") {
		t.Errorf("image_uri Lambda must not get a filename placeholder\n--- got ---\n%s", out)
	}
}

// TestFixupLambda_NonLambdaResourceUntouched pins isolation: the fixup
// table is keyed by resource type, so an unrelated resource block must
// pass through unchanged. A mutation that broadened the fixup to "every
// resource type" would corrupt these blocks.
func TestFixupLambda_NonLambdaResourceUntouched(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" { name = "alpha" }
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "lifecycle") {
		t.Errorf("non-Lambda resource must not get a lifecycle block from Lambda fixup\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "lambda_placeholder.zip") {
		t.Errorf("non-Lambda resource must not get a Lambda placeholder\n--- got ---\n%s", got)
	}
}

// TestFixupKMS_RotationPeriodZeroDropped pins the LocalStack 4.x
// fidelity workaround for #272: DescribeKey returns
// rotation_period_in_days=0 for keys without rotation enabled, but the
// AWS provider's validator rejects 0 (range 90-2560). Real AWS leaves
// the field absent, so the fixup normalizes LocalStack output to the
// AWS-shaped output that schema cleanup is built around.
func TestFixupKMS_RotationPeriodZeroDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_kms_key" "main" {
  description             = "x"
  rotation_period_in_days = 0
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "rotation_period_in_days") {
		t.Errorf("rotation_period_in_days = 0 must be dropped\n--- got ---\n%s", got)
	}
}

// TestFixupKMS_RotationPeriodNonZeroPreserved pins conservative scope:
// real AWS returning a meaningful 365-day rotation must NOT have its
// value silently dropped. Only the literal 0 from LocalStack triggers
// the fixup.
//
// Table-driven so the carve-outs documented on isAttrLiteralZero
// ("does NOT match `0.0`, `00`, or any computed expression") are
// pinned by tests, not just docstrings. A mutation broadening the
// trigger to `strings.HasPrefix(s, "0")` or `== "00"` would now fail
// these cases.
func TestFixupKMS_RotationPeriodNonZeroPreserved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
	}{
		{name: "real AWS value", value: "365"},
		{name: "minimum valid", value: "90"},
		{name: "maximum valid", value: "2560"},
		{name: "leading-zero literal (carve-out: not the LocalStack shape)", value: "00"},
		{name: "float-zero literal (carve-out: not the LocalStack shape)", value: "0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := []byte(`resource "aws_kms_key" "main" {
  description             = "x"
  rotation_period_in_days = ` + tc.value + `
}
`)
			out, err := applyResourceTypeFixups(in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			pat := `rotation_period_in_days\s*=\s*` + regexp.QuoteMeta(tc.value)
			if !regexp.MustCompile(pat).MatchString(got) {
				t.Errorf("value %q must be preserved (only literal `0` is dropped)\n--- got ---\n%s", tc.value, got)
			}
		})
	}
}

// TestFixupDynamoDB_PITRRecoveryPeriodZeroDropped is the DynamoDB twin
// of the KMS rotation fixup — same LocalStack 4.x quirk, different
// resource type. Validator range is 1-35; LocalStack returns 0 when
// PITR is disabled.
func TestFixupDynamoDB_PITRRecoveryPeriodZeroDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_dynamodb_table" "main" {
  name = "x"
  point_in_time_recovery {
    enabled                 = false
    recovery_period_in_days = 0
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "recovery_period_in_days") {
		t.Errorf("recovery_period_in_days = 0 must be dropped from point_in_time_recovery block\n--- got ---\n%s", got)
	}
	// The enclosing block must remain so other PITR fields (enabled)
	// stay intact.
	if !strings.Contains(got, "point_in_time_recovery {") {
		t.Errorf("point_in_time_recovery block must not be removed wholesale\n--- got ---\n%s", got)
	}
}

// TestFixupDynamoDB_PITRRecoveryPeriodNonZeroPreserved is the symmetric
// non-zero case — a real PITR window must reach the emitted HCL
// untouched. Table-driven to also pin the literal-zero carve-outs
// documented on isAttrLiteralZero.
func TestFixupDynamoDB_PITRRecoveryPeriodNonZeroPreserved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
	}{
		{name: "real AWS value", value: "14"},
		{name: "minimum valid", value: "1"},
		{name: "maximum valid", value: "35"},
		{name: "leading-zero literal (carve-out)", value: "00"},
		{name: "float-zero literal (carve-out)", value: "0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := []byte(`resource "aws_dynamodb_table" "main" {
  name = "x"
  point_in_time_recovery {
    enabled                 = true
    recovery_period_in_days = ` + tc.value + `
  }
}
`)
			out, err := applyResourceTypeFixups(in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			pat := `recovery_period_in_days\s*=\s*` + regexp.QuoteMeta(tc.value)
			if !regexp.MustCompile(pat).MatchString(got) {
				t.Errorf("value %q must be preserved (only literal `0` is dropped)\n--- got ---\n%s", tc.value, got)
			}
		})
	}
}

// TestFixupDynamoDB_NoPITRBlockNoOp pins the canonical real-AWS shape:
// when point_in_time_recovery isn't even present, the fixup must be a
// pure no-op. A mutation that "helpfully" injected a PITR block or
// touched other sub-blocks would fail this.
func TestFixupDynamoDB_NoPITRBlockNoOp(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_dynamodb_table" "main" {
  name     = "x"
  hash_key = "id"

  attribute {
    name = "id"
    type = "S"
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("absent point_in_time_recovery must yield identical output\n--- in ---\n%s\n--- out ---\n%s", in, out)
	}
}

// TestFixupDynamoDB_PITRBlockPresentAttrAbsentNoOp pins that a PITR
// block carrying only `enabled = false` (no recovery_period_in_days)
// is also a no-op. A mutation that always added or removed
// recovery_period_in_days regardless of presence would fail.
func TestFixupDynamoDB_PITRBlockPresentAttrAbsentNoOp(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_dynamodb_table" "main" {
  name = "x"
  point_in_time_recovery {
    enabled = false
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*enabled\s*=\s*false`).MatchString(got) {
		t.Errorf("enabled=false must be preserved\n--- got ---\n%s", got)
	}
	if regexp.MustCompile(`recovery_period_in_days`).MatchString(got) {
		t.Errorf("absent recovery_period_in_days must NOT appear after fixup\n--- got ---\n%s", got)
	}
}

// TestFixupDynamoDB_MultiplePITRBlocksAllZerosDropped pins iteration:
// if a (hypothetical) DynamoDB resource has multiple
// point_in_time_recovery sub-blocks (Terraform doesn't support this in
// reality, but `terraform plan -generate-config-out` has emitted
// duplicate blocks before for other types), the fixup must process
// all of them — not break after the first match. A mutation
// substituting `break` for `continue` after the inner remove would
// survive single-block tests but fail this.
func TestFixupDynamoDB_MultiplePITRBlocksAllZerosDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_dynamodb_table" "main" {
  name = "x"
  point_in_time_recovery {
    enabled                 = false
    recovery_period_in_days = 0
  }
  point_in_time_recovery {
    enabled                 = false
    recovery_period_in_days = 0
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`recovery_period_in_days`).MatchString(got) {
		t.Errorf("all zero-valued recovery_period_in_days must be dropped, even across multiple PITR blocks\n--- got ---\n%s", got)
	}
}

// TestFixupVPC_IPv6NetmaskOrphanRemoved pins the canonical orphan shape:
// generate-config-out emits both attrs (pool=null, netmask=0) for a
// non-IPAM VPC. The fixup must drop the orphan netmask so the provider's
// `all of ...` validator stops failing. Issue #337.
func TestFixupVPC_IPv6NetmaskOrphanRemoved(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_vpc" "main" {
  cidr_block          = "10.0.0.0/16"
  ipv6_ipam_pool_id   = null
  ipv6_netmask_length = 0
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "ipv6_netmask_length") {
		t.Errorf("orphan ipv6_netmask_length must be dropped\n--- got ---\n%s", got)
	}
}

// TestFixupVPC_IPv6NetmaskPreservedWhenPoolSet pins conservative scope:
// a real IPAM-pinned VPC carrying both `ipv6_ipam_pool_id` and a non-zero
// `ipv6_netmask_length` must be left untouched. The fixup only fires on
// the orphan (no pool + zero netmask) shape.
func TestFixupVPC_IPv6NetmaskPreservedWhenPoolSet(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_vpc" "main" {
  cidr_block          = "10.0.0.0/16"
  ipv6_ipam_pool_id   = "ipam-pool-0123456789abcdef0"
  ipv6_netmask_length = 64
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`ipv6_ipam_pool_id\s*=\s*"ipam-pool-0123456789abcdef0"`).MatchString(got) {
		t.Errorf("ipv6_ipam_pool_id must be preserved when set\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`ipv6_netmask_length\s*=\s*64`).MatchString(got) {
		t.Errorf("ipv6_netmask_length=64 must be preserved when pool is set\n--- got ---\n%s", got)
	}
}

// TestFixupVPC_NoOpWhenNeitherSet pins that a VPC block emitted without
// either ipv6_* attribute (the older provider behaviour, or operator-
// hand-edited HCL) is a pure no-op. A mutation that always wrote a stub
// would fail this.
func TestFixupVPC_NoOpWhenNeitherSet(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("no ipv6_* attrs must yield identical output\n--- in ---\n%s\n--- out ---\n%s", in, out)
	}
}

// TestFixupVPC_OtherAttrsUntouched pins isolation within the VPC block:
// the fixup only touches the orphan netmask attribute. Other attrs
// (cidr_block, instance_tenancy, enable_dns_hostnames) flow through
// untouched.
func TestFixupVPC_OtherAttrsUntouched(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/16"
  instance_tenancy     = "default"
  enable_dns_hostnames = true
  ipv6_ipam_pool_id    = null
  ipv6_netmask_length  = 0
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`cidr_block\s*=\s*"10\.0\.0\.0/16"`).MatchString(got) {
		t.Errorf("cidr_block must be preserved\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`instance_tenancy\s*=\s*"default"`).MatchString(got) {
		t.Errorf("instance_tenancy must be preserved\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`enable_dns_hostnames\s*=\s*true`).MatchString(got) {
		t.Errorf("enable_dns_hostnames must be preserved\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "ipv6_netmask_length") {
		t.Errorf("orphan ipv6_netmask_length must still be dropped\n--- got ---\n%s", got)
	}
}

// TestFixupVPC_OnlyAffectsAWSVPCBlocks pins resource-type isolation: a
// sibling aws_subnet block carrying its own (unrelated) ipv6_*
// attributes must NOT be touched by the VPC fixup. The fixup table is
// keyed by resource type, so a mutation broadening it to "any resource
// with these attrs" would corrupt the subnet block.
func TestFixupVPC_OnlyAffectsAWSVPCBlocks(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_vpc" "main" {
  cidr_block          = "10.0.0.0/16"
  ipv6_ipam_pool_id   = null
  ipv6_netmask_length = 0
}

resource "aws_subnet" "sub" {
  vpc_id              = "vpc-123"
  cidr_block          = "10.0.1.0/24"
  ipv6_netmask_length = 0
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// VPC's orphan should be dropped...
	if strings.Count(got, "ipv6_netmask_length") != 1 {
		t.Errorf("expected exactly one ipv6_netmask_length remaining (the subnet's), got %d\n--- got ---\n%s",
			strings.Count(got, "ipv6_netmask_length"), got)
	}
	// ...but the subnet block must keep its own ipv6_netmask_length.
	if !regexp.MustCompile(`(?s)resource "aws_subnet"[^}]*ipv6_netmask_length`).MatchString(got) {
		t.Errorf("aws_subnet's ipv6_netmask_length must be preserved (fixup is keyed by aws_vpc)\n--- got ---\n%s", got)
	}
}

// TestFixupLB_DropsSubnetMappingWhenNoIPPinned pins the common ALB
// shape: generate-config-out emits both subnet_mapping (one block per
// subnet) and subnets (the canonical list). When no sub-block carries a
// static IP pin, drop the subnet_mapping blocks. Issue #338.
func TestFixupLB_DropsSubnetMappingWhenNoIPPinned(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lb" "main" {
  name     = "alpha"
  internal = false
  subnets  = ["subnet-aaa", "subnet-bbb"]
  subnet_mapping {
    subnet_id = "subnet-aaa"
  }
  subnet_mapping {
    subnet_id = "subnet-bbb"
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`(?m)^\s*subnet_mapping\s*\{`).MatchString(got) {
		t.Errorf("subnet_mapping blocks must be dropped when no static IP pin present\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`subnets\s*=\s*\["subnet-aaa",\s*"subnet-bbb"\]`).MatchString(got) {
		t.Errorf("subnets list must be preserved\n--- got ---\n%s", got)
	}
}

// TestFixupLB_PreservesSubnetMappingWhenAllocationIDSet pins the NLB-EIP
// case: an operator pinning an Elastic IP via allocation_id is
// expressing static-IP intent that subnet_mapping carries and `subnets`
// does not. Drop `subnets`, keep the mapping blocks.
func TestFixupLB_PreservesSubnetMappingWhenAllocationIDSet(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lb" "main" {
  name               = "nlb"
  load_balancer_type = "network"
  subnets            = ["subnet-aaa", "subnet-bbb"]
  subnet_mapping {
    subnet_id     = "subnet-aaa"
    allocation_id = "eipalloc-0123456789abcdef0"
  }
  subnet_mapping {
    subnet_id = "subnet-bbb"
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`(?m)^\s*subnets\s*=`).MatchString(got) {
		t.Errorf("subnets attribute must be dropped when subnet_mapping carries allocation_id\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "subnet_mapping") {
		t.Errorf("subnet_mapping blocks must be preserved when allocation_id present\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, `allocation_id = "eipalloc-0123456789abcdef0"`) {
		t.Errorf("allocation_id value must be preserved\n--- got ---\n%s", got)
	}
}

// TestFixupLB_PreservesSubnetMappingWhenPrivateIPv4Set pins the
// internal-LB private-IP case: a sub-block carrying private_ipv4_address
// also expresses static-IP intent. Same outcome as the allocation_id
// case — drop `subnets`, keep mappings.
func TestFixupLB_PreservesSubnetMappingWhenPrivateIPv4Set(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lb" "main" {
  name     = "internal"
  internal = true
  subnets  = ["subnet-aaa"]
  subnet_mapping {
    subnet_id            = "subnet-aaa"
    private_ipv4_address = "10.0.1.42"
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`(?m)^\s*subnets\s*=`).MatchString(got) {
		t.Errorf("subnets attribute must be dropped when subnet_mapping carries private_ipv4_address\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, `private_ipv4_address = "10.0.1.42"`) {
		t.Errorf("private_ipv4_address value must be preserved\n--- got ---\n%s", got)
	}
}

// TestFixupLB_NoOpWhenNeitherPresent pins the operator-hand-edited /
// minimal-LB case: no subnets attribute and no subnet_mapping blocks.
// The fixup must not invent either, and other attrs flow through.
func TestFixupLB_NoOpWhenNeitherPresent(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lb" "main" {
  name     = "alpha"
  internal = true
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("no subnets/subnet_mapping must yield identical output\n--- in ---\n%s\n--- out ---\n%s", in, out)
	}
}

// TestFixupLB_OnlyAffectsAWSLBBlocks pins resource-type isolation: a
// sibling aws_lb_target_group block (which has no subnet_mapping/subnets
// schema) must not be perturbed by the LB fixup. The fixup table is
// keyed by aws_lb so any block with a different resource type passes
// through untouched.
func TestFixupLB_OnlyAffectsAWSLBBlocks(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lb" "main" {
  name    = "alpha"
  subnets = ["subnet-aaa"]
  subnet_mapping {
    subnet_id = "subnet-aaa"
  }
}

resource "aws_lb_target_group" "tg" {
  name     = "alpha-tg"
  port     = 80
  protocol = "HTTP"
  vpc_id   = "vpc-123"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// LB had no IP pin → subnet_mapping dropped; subnets kept.
	if strings.Contains(got, "subnet_mapping") {
		t.Errorf("aws_lb's subnet_mapping must be dropped\n--- got ---\n%s", got)
	}
	// Target group block must remain intact.
	if !regexp.MustCompile(`(?s)resource "aws_lb_target_group" "tg" \{[^}]*name\s*=\s*"alpha-tg"`).MatchString(got) {
		t.Errorf("aws_lb_target_group must pass through untouched\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`(?s)resource "aws_lb_target_group" "tg" \{[^}]*port\s*=\s*80`).MatchString(got) {
		t.Errorf("aws_lb_target_group port must be preserved\n--- got ---\n%s", got)
	}
}

// TestFixupLambda_MultipleLambdasBothFixed pins iteration: two Lambda
// blocks in input order both get the placeholder + ignore_changes
// treatment. A mutation that exited after the first block would survive
// single-resource tests but fail this one.
func TestFixupLambda_MultipleLambdasBothFixed(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "alpha" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
}

resource "aws_lambda_function" "bravo" {
  function_name = "bravo"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "lambda_placeholder.zip") != 2 {
		t.Errorf("expected 2 placeholder injections, got %d\n--- got ---\n%s", strings.Count(got, "lambda_placeholder.zip"), got)
	}
	if strings.Count(got, "ignore_changes") != 2 {
		t.Errorf("expected 2 ignore_changes injections, got %d", strings.Count(got, "ignore_changes"))
	}
}

// TestFixupVPC_IPv6NetmaskNonLiteralZeroPreserved pins isAttrLiteralZero's
// carve-out spec: only the literal `0` triggers the orphan drop. Variants
// like `00`, `0.0`, or any computed expression are preserved untouched. A
// mutation that loosened the trim to numeric coercion would survive the
// canonical-orphan test but fail this one.
func TestFixupVPC_IPv6NetmaskNonLiteralZeroPreserved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		val  string
	}{
		{"double_zero", `00`},
		{"zero_dot_zero", `0.0`},
		{"computed_var", `var.netmask`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := []byte(`resource "aws_vpc" "main" {
  cidr_block          = "10.0.0.0/16"
  ipv6_ipam_pool_id   = null
  ipv6_netmask_length = ` + tc.val + `
}
`)
			out, err := applyResourceTypeFixups(in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if !strings.Contains(got, "ipv6_netmask_length") {
				t.Errorf("ipv6_netmask_length=%s must NOT be dropped (only literal 0 triggers the fixup)\n--- got ---\n%s", tc.val, got)
			}
		})
	}
}

// TestFixupLB_PreservesSubnetMappingWhenIPv6AddressSet pins the third
// static-IP-pin attribute (ipv6_address) — the production code checks
// allocation_id, private_ipv4_address, AND ipv6_address. A mutation that
// dropped the third disjunct or replaced || with && would survive the
// other LB pin tests but fail this one.
func TestFixupLB_PreservesSubnetMappingWhenIPv6AddressSet(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lb" "main" {
  name               = "dual-stack-nlb"
  load_balancer_type = "network"
  subnets            = ["subnet-aaa"]
  subnet_mapping {
    subnet_id    = "subnet-aaa"
    ipv6_address = "2001:db8::1"
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`(?m)^\s*subnets\s*=`).MatchString(got) {
		t.Errorf("subnets attribute must be dropped when subnet_mapping carries ipv6_address\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^\s*subnet_mapping\s*\{`).MatchString(got) {
		t.Errorf("subnet_mapping blocks must be preserved when ipv6_address present\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, `ipv6_address = "2001:db8::1"`) {
		t.Errorf("ipv6_address value must be preserved\n--- got ---\n%s", got)
	}
}

// TestFixupSubnet_AZIdDroppedWhenAZSet pins #343: when both
// availability_zone and availability_zone_id are present (the
// generate-config-out default), drop the ID and keep the human-readable
// AZ.
func TestFixupSubnet_AZIdDroppedWhenAZSet(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_subnet" "main" {
  vpc_id               = "vpc-123"
  cidr_block           = "10.0.1.0/24"
  availability_zone    = "us-east-1a"
  availability_zone_id = "use1-az6"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`(?m)^\s*availability_zone_id\s*=`).MatchString(got) {
		t.Errorf("availability_zone_id must be dropped when availability_zone is set\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^\s*availability_zone\s*=\s*"us-east-1a"`).MatchString(got) {
		t.Errorf("availability_zone must be preserved\n--- got ---\n%s", got)
	}
}

// TestFixupSubnet_AZAttrsPreservedWhenOnlyOneSet pins the carve-out:
// the fixup only fires when BOTH AZ attrs are present. A subnet with
// only availability_zone (or only availability_zone_id) flows through
// untouched.
func TestFixupSubnet_AZAttrsPreservedWhenOnlyOneSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		in          string
		wantPresent string
		wantAbsent  string
	}{
		{"only_az", `availability_zone = "us-east-1a"`, `availability_zone\s*=\s*"us-east-1a"`, `(?m)^\s*availability_zone_id\s*=`},
		{"only_az_id", `availability_zone_id = "use1-az6"`, `availability_zone_id\s*=\s*"use1-az6"`, `(?m)^\s*availability_zone\s*=`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := []byte(`resource "aws_subnet" "main" {
  vpc_id     = "vpc-123"
  cidr_block = "10.0.1.0/24"
  ` + tc.in + `
}
`)
			out, err := applyResourceTypeFixups(in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if !regexp.MustCompile(tc.wantPresent).MatchString(got) {
				t.Errorf("expected %q to match\n--- got ---\n%s", tc.wantPresent, got)
			}
			// Negative assertion: the fixup must not inject the absent
			// sibling. Pins against a mutation that always emits a
			// default AZ when only one of the pair is present.
			if regexp.MustCompile(tc.wantAbsent).MatchString(got) {
				t.Errorf("fixup must not inject %q when only one AZ attr is present\n--- got ---\n%s", tc.wantAbsent, got)
			}
		})
	}
}

// TestFixupSubnet_NullLiteralAZIDPreserved pins the null-literal branch
// of hasUsableValue: generate-config-out emits availability_zone = null
// for any subnet whose AZ string is null at read time. The fixup must
// recognize null-literal as "no usable value" and preserve the ID. A
// mutation that replaced hasUsableValue with `GetAttribute(name) != nil`
// would survive the existing "only one set" carve-out (which tested
// attribute absence) but fail here.
func TestFixupSubnet_NullLiteralAZIDPreserved(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_subnet" "main" {
  vpc_id               = "vpc-123"
  cidr_block           = "10.0.1.0/24"
  availability_zone    = null
  availability_zone_id = "use1-az6"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*availability_zone_id\s*=\s*"use1-az6"`).MatchString(got) {
		t.Errorf("availability_zone_id must be preserved when availability_zone is null\n--- got ---\n%s", got)
	}
}

// TestFixupSubnet_NullLiteralOutpostTrioStillDropsOrphan pins the
// null-literal branch on the trio: generate-config-out emits both
// customer_owned_ipv4_pool = null and outpost_arn = null for any
// non-Outpost subnet. The fixup must still drop the orphan
// map_customer_owned_ip_on_launch. A mutation that treated null as
// "present" would survive the existing absent-sibling test but fail
// here.
func TestFixupSubnet_NullLiteralOutpostTrioStillDropsOrphan(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_subnet" "main" {
  vpc_id                          = "vpc-123"
  cidr_block                      = "10.0.1.0/24"
  customer_owned_ipv4_pool        = null
  outpost_arn                     = null
  map_customer_owned_ip_on_launch = false
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`(?m)^\s*map_customer_owned_ip_on_launch\s*=`).MatchString(got) {
		t.Errorf("orphan map_customer_owned_ip_on_launch must be dropped when both siblings are null\n--- got ---\n%s", got)
	}
}

// TestFixupSubnet_LniAtDeviceIndexZeroDropped pins #344a: literal 0
// fails provider validation (`enable_lni_at_device_index must not be
// zero, got 0`). Drop the attribute.
func TestFixupSubnet_LniAtDeviceIndexZeroDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_subnet" "main" {
  vpc_id                     = "vpc-123"
  cidr_block                 = "10.0.1.0/24"
  enable_lni_at_device_index = 0
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "enable_lni_at_device_index") {
		t.Errorf("enable_lni_at_device_index = 0 must be dropped\n--- got ---\n%s", got)
	}
}

// TestFixupSubnet_LniAtDeviceIndexNonZeroPreserved pins the carve-out:
// non-zero values are valid (provider domain starts at 1) and must be
// preserved. Table-driven over realistic values.
func TestFixupSubnet_LniAtDeviceIndexNonZeroPreserved(t *testing.T) {
	t.Parallel()
	cases := []string{"1", "2", "7"}
	for _, v := range cases {
		v := v
		t.Run("idx_"+v, func(t *testing.T) {
			t.Parallel()
			in := []byte(`resource "aws_subnet" "main" {
  vpc_id                     = "vpc-123"
  cidr_block                 = "10.0.1.0/24"
  enable_lni_at_device_index = ` + v + `
}
`)
			out, err := applyResourceTypeFixups(in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if !regexp.MustCompile(`enable_lni_at_device_index\s*=\s*` + v).MatchString(got) {
				t.Errorf("non-zero enable_lni_at_device_index=%s must be preserved\n--- got ---\n%s", v, got)
			}
		})
	}
}

// TestFixupSubnet_CustomerOwnedIPOrphanDropped pins #344b: drop
// map_customer_owned_ip_on_launch when both customer_owned_ipv4_pool
// AND outpost_arn are absent. The trio is mutually-required by the
// provider schema; orphan presence fails validate.
func TestFixupSubnet_CustomerOwnedIPOrphanDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_subnet" "main" {
  vpc_id                          = "vpc-123"
  cidr_block                      = "10.0.1.0/24"
  map_customer_owned_ip_on_launch = false
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "map_customer_owned_ip_on_launch") {
		t.Errorf("orphan map_customer_owned_ip_on_launch must be dropped\n--- got ---\n%s", got)
	}
}

// TestFixupSubnet_CustomerOwnedIPPreservedWhenOutpostSet pins the
// carve-out: a real Outpost subnet carrying outpost_arn (or
// customer_owned_ipv4_pool) preserves the full trio. Table-driven over
// each sibling.
func TestFixupSubnet_CustomerOwnedIPPreservedWhenOutpostSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
	}{
		{"outpost_arn", `outpost_arn = "arn:aws:outposts:us-east-1:123:outpost/op-abc"`},
		{"customer_owned_ipv4_pool", `customer_owned_ipv4_pool = "ipv4pool-coip-0123456789abcdef0"`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := []byte(`resource "aws_subnet" "main" {
  vpc_id                          = "vpc-123"
  cidr_block                      = "10.0.1.0/24"
  map_customer_owned_ip_on_launch = true
  ` + tc.in + `
}
`)
			out, err := applyResourceTypeFixups(in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if !strings.Contains(got, "map_customer_owned_ip_on_launch") {
				t.Errorf("map_customer_owned_ip_on_launch must be preserved when %s is set\n--- got ---\n%s", tc.name, got)
			}
		})
	}
}

// TestFixupSubnet_OnlyAffectsAWSSubnetBlocks pins resource-type
// isolation: a sibling aws_vpc block carrying its own (unrelated)
// availability_zone_id and map_customer_owned_ip_on_launch flows
// through untouched. The fixup table is keyed by aws_subnet, so a
// mutation broadening it to "any resource with these attrs" would
// corrupt the VPC block.
func TestFixupSubnet_OnlyAffectsAWSSubnetBlocks(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_subnet" "sub" {
  vpc_id                          = "vpc-123"
  cidr_block                      = "10.0.1.0/24"
  availability_zone               = "us-east-1a"
  availability_zone_id            = "use1-az6"
  enable_lni_at_device_index      = 0
  map_customer_owned_ip_on_launch = false
}

resource "aws_vpc" "vpc" {
  cidr_block                      = "10.0.0.0/16"
  availability_zone_id            = "use1-az6"
  enable_lni_at_device_index      = 0
  map_customer_owned_ip_on_launch = false
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// VPC block must keep its (unrelated, non-schema) attributes.
	if !regexp.MustCompile(`(?s)resource "aws_vpc" "vpc"[^}]*availability_zone_id\s*=\s*"use1-az6"`).MatchString(got) {
		t.Errorf("aws_vpc.availability_zone_id must be preserved (fixup keyed by aws_subnet only)\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`(?s)resource "aws_vpc" "vpc"[^}]*enable_lni_at_device_index\s*=\s*0`).MatchString(got) {
		t.Errorf("aws_vpc.enable_lni_at_device_index must be preserved\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`(?s)resource "aws_vpc" "vpc"[^}]*map_customer_owned_ip_on_launch\s*=\s*false`).MatchString(got) {
		t.Errorf("aws_vpc.map_customer_owned_ip_on_launch must be preserved\n--- got ---\n%s", got)
	}
	// Subnet block must have all three transforms applied.
	if regexp.MustCompile(`(?s)resource "aws_subnet" "sub"[^}]*availability_zone_id\s*=`).MatchString(got) {
		t.Errorf("aws_subnet.availability_zone_id must be dropped\n--- got ---\n%s", got)
	}
	if regexp.MustCompile(`(?s)resource "aws_subnet" "sub"[^}]*enable_lni_at_device_index\s*=`).MatchString(got) {
		t.Errorf("aws_subnet.enable_lni_at_device_index = 0 must be dropped\n--- got ---\n%s", got)
	}
	if regexp.MustCompile(`(?s)resource "aws_subnet" "sub"[^}]*map_customer_owned_ip_on_launch\s*=`).MatchString(got) {
		t.Errorf("aws_subnet.map_customer_owned_ip_on_launch must be dropped\n--- got ---\n%s", got)
	}
}
