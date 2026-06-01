# Golden import-stack fixtures

Each subdirectory here is a captured real-world reverse-import stack used by
`TestGoldenStackValidates` (`golden_stack_test.go`) as a repeatable regression
gate for invalid provider-generated HCL (issue #708).

The test replays the **post-`generate-config-out`** half of the genconfig
pipeline — schema clean → resource-type fixups → un-importable prune → orphan
prune → cross-ref → `terraform validate` — against the captured input and
asserts the result is schema-valid. Because `terraform plan
-generate-config-out` is pre-captured, the test is deterministic and needs
**no AWS credentials**: only the `terraform` binary and the AWS provider
(which `terraform init` fetches).

## Fixture files

Files carry a `.tf.golden` suffix (not `.tf`) so the repo's `tflint --recursive`
and `terraform fmt -check -recursive` do not lint this intentionally-raw,
pre-cleanup provider output. The harness writes them under their real `.tf`
names into a scratch dir at test time.

| File | What it is |
|---|---|
| `generated.tf.golden` | Raw output of `terraform plan -generate-config-out` (pre-cleanup, with all the provider over-emission the fixups exist to fix). |
| `imports.tf.golden` | The full pre-prune `import {}` block set (orphans included, so the orphan-prune step is exercised). |
| `providers.tf.golden` | The provider block genconfig emits for the stack. |

## Stacks

| Dir | Source | Notes |
|---|---|---|
| `dario/` | Prod reverse-import, AWS account `141812438321`, us-east-1 (reliable session `sess_v2_Ne5aA9A1Zumn`, job `ri-415161a7-tfpxl`). | 154 imports / 92 generated bodies. Exercises Lambda, ALB + target group, SNS `signature_version=0`, EBS `volume_initialization_rate=0`, ENI over-emission, AWS-managed `alias/aws/*` KMS aliases, and a NAT-gateway-managed ENI. Real customer identifiers are intentionally retained (internal repo). |

## Regenerating a fixture

After an AWS-provider bump or when a new quirk class appears, re-capture from a
live reverse-import run (needs AWS creds for the source account):

```bash
# 1. Run the reverse import into a scratch dir (writes <dir>/genconfig/).
insideout-import reverse --input <request>.json --output-dir /tmp/cap \
  --provider aws --region <region>

# 2. The raw generate-config-out output and the (post-prune) imports.tf are in
#    /tmp/cap/genconfig/. To capture the FULL pre-prune imports.tf, re-append
#    the orphan-skipped blocks from imports-skipped.json, then re-run
#    generate-config-out in a clean dir so generated.tf is the raw output.
#    See the capture procedure in the #708 PR description.

# 3. Copy generated.tf, imports.tf, providers.tf into testdata/golden/<stack>/
#    with a .golden suffix (generated.tf.golden, imports.tf.golden,
#    providers.tf.golden) so tflint / terraform fmt skip them.
```

## Running the gate

```bash
make test-golden-stack          # RUN_GOLDEN_HCL=1 go test … (needs terraform)
```

CI runs it in the `golden-stack-hcl` job of `.github/workflows/terraform-validate.yml`.
