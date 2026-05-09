package genconfig

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// applyResourceTypeFixups runs after schema cleanup. It plugs the gaps
// where `terraform plan -generate-config-out` produces output that the
// schema by itself can't describe to the cleaner. Today the only such
// gap is aws_lambda_function: the AtLeastOneOf source attributes
// (filename / image_uri / s3_bucket) are validate-required, but
// generate-config-out can't emit them for an existing function (the code
// lives in AWS, not on disk). The fixup injects a placeholder filename
// plus a lifecycle.ignore_changes pin so subsequent `terraform apply`
// passes won't try to re-upload code the operator doesn't hold.
//
// Each fixup is keyed by Terraform resource type so adding new ones (e.g.
// aws_ecs_task_definition's container_definitions JSON, aws_apigateway
// methods that need re-emission) is a one-line addition to the table
// rather than a refactor.
func applyResourceTypeFixups(raw []byte) ([]byte, error) {
	f, diags := hclwrite.ParseConfig(raw, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse for fixups: %s", diags.Error())
	}
	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		fix, ok := resourceTypeFixups[labels[0]]
		if !ok {
			continue
		}
		fix(blk)
	}
	return f.Bytes(), nil
}

// resourceTypeFixups maps a Terraform resource type to its post-cleanup
// fixup closure. Closures may add or remove attributes and sub-blocks.
// They must NOT depend on schema metadata — that's already been applied
// by cleanGenerated.
var resourceTypeFixups = map[string]func(*hclwrite.Block){
	"aws_lambda_function": fixupLambdaSource,
	"aws_kms_key":         fixupKMSRotationPeriodZero,
	"aws_dynamodb_table":  fixupDynamoDBPITRRecoveryPeriodZero,
	"aws_vpc":             fixupVPCIPv6NetmaskOrphan,
	"aws_lb":              fixupLBSubnetMappingConflict,
	"aws_subnet":          fixupSubnetProviderQuirks,
	"aws_route_table":     fixupRouteTableEmptyRouteFields,
}

// lambdaPlaceholderFile is what we set `filename` to so the block
// validates without holding actual code. terraform validate doesn't open
// the file (that happens at apply / build time), so the path can point
// at a nonexistent file. A neutral, unmistakable name keeps the
// generated.tf self-documenting.
const lambdaPlaceholderFile = "lambda_placeholder.zip"

// lambdaIgnoreChanges is the set of attributes lifecycle.ignore_changes
// must cover so a real `terraform apply` against this stack will not try
// to re-upload code or churn checksum-derived attrs the operator never
// edits.
var lambdaIgnoreChanges = []string{
	"filename",
	"image_uri",
	"s3_bucket",
	"s3_key",
	"s3_object_version",
	"source_code_hash",
}

// fixupLambdaSource is the per-block implementation of the Lambda
// post-cleanup contract documented on applyResourceTypeFixups. The block
// is mutated in place.
func fixupLambdaSource(blk *hclwrite.Block) {
	body := blk.Body()
	hasSource := hasUsableValue(body, "filename") ||
		hasUsableValue(body, "image_uri") ||
		hasUsableValue(body, "s3_bucket")
	if !hasSource {
		// SetAttributeValue overwrites the existing `... = null` line;
		// we don't need to RemoveAttribute first.
		body.SetAttributeValue("filename", cty.StringVal(lambdaPlaceholderFile))
	}

	// Reuse the cleanup-side helpers so ignore_changes shape stays
	// consistent across Sensitive-driven and fixup-driven entries.
	for _, sub := range body.Blocks() {
		if sub.Type() == "lifecycle" {
			mergeIgnoreChanges(sub, lambdaIgnoreChanges)
			return
		}
	}
	lc := body.AppendNewBlock("lifecycle", nil)
	lc.Body().SetAttributeRaw("ignore_changes", ignoreChangesTokens(lambdaIgnoreChanges))
}

// fixupKMSRotationPeriodZero drops aws_kms_key.rotation_period_in_days
// when its emitted value is the literal `0`. Real AWS DescribeKey leaves
// the field absent when key rotation isn't enabled (the provider's
// validator pins it to 90-2560), so generate-config-out wouldn't emit
// the line in the first place. LocalStack 4.x returns 0 instead of
// leaving the field unset, which makes the import bundle fail
// `terraform validate` with `expected rotation_period_in_days to be in
// the range (90 - 2560), got 0`.
//
// The fixup is conservative — it only touches the literal `0`, so a
// real value coming back from AWS (e.g. 365) is preserved. No-op
// against real AWS.
func fixupKMSRotationPeriodZero(blk *hclwrite.Block) {
	if isAttrLiteralZero(blk.Body(), "rotation_period_in_days") {
		blk.Body().RemoveAttribute("rotation_period_in_days")
	}
}

// fixupDynamoDBPITRRecoveryPeriodZero drops the analogous LocalStack 0
// for aws_dynamodb_table.point_in_time_recovery.recovery_period_in_days
// (validator: 1-35). Same conservative shape as fixupKMSRotationPeriodZero;
// only the literal `0` is removed.
func fixupDynamoDBPITRRecoveryPeriodZero(blk *hclwrite.Block) {
	for _, sub := range blk.Body().Blocks() {
		if sub.Type() != "point_in_time_recovery" {
			continue
		}
		if isAttrLiteralZero(sub.Body(), "recovery_period_in_days") {
			sub.Body().RemoveAttribute("recovery_period_in_days")
		}
	}
}

// fixupVPCIPv6NetmaskOrphan drops aws_vpc.ipv6_netmask_length when its
// emitted value is the literal `0` AND ipv6_ipam_pool_id has no usable
// value. The provider schema marks the pair as mutually-required: if one
// is specified, the other must be too. `terraform plan
// -generate-config-out` always emits both attributes regardless of
// whether the running VPC was provisioned from an IPAM pool, so for the
// common non-IPAM case generate-config-out produces
// `ipv6_ipam_pool_id = null` + `ipv6_netmask_length = 0`, which fails
// `terraform validate` with `"ipv6_netmask_length": all of
// `ipv6_ipam_pool_id,ipv6_netmask_length` must be specified`.
//
// Conservative shape: only the orphan pairing (no pool + literal 0
// netmask) triggers the drop. A real IPAM-pinned VPC carrying both
// values is preserved untouched. Issue #337.
func fixupVPCIPv6NetmaskOrphan(blk *hclwrite.Block) {
	body := blk.Body()
	if hasUsableValue(body, "ipv6_ipam_pool_id") {
		return
	}
	if !isAttrLiteralZero(body, "ipv6_netmask_length") {
		return
	}
	body.RemoveAttribute("ipv6_netmask_length")
}

// fixupLBSubnetMappingConflict resolves the aws_lb subnet_mapping vs
// subnets mutual-exclusion conflict. The provider schema marks the pair
// as mutually exclusive (`only one of `subnet_mapping,subnets` can be
// specified`), but `terraform plan -generate-config-out` always emits
// both: `subnets` as a string list and `subnet_mapping` as one block per
// subnet attachment.
//
// Heuristic: subnet_mapping is only meaningful when the operator has
// pinned static IPs on a load-balancer interface (Network LB EIP
// allocation, private IPv4 pin, or IPv6 pin). If any sub-block carries
// `allocation_id`, `private_ipv4_address`, or `ipv6_address` with a
// usable value, drop `subnets` and keep the subnet_mapping blocks.
// Otherwise the subnet_mapping blocks contribute no information beyond
// what `subnets` already conveys, so drop them all and keep `subnets`
// (the canonical ALB shape). Issue #338.
func fixupLBSubnetMappingConflict(blk *hclwrite.Block) {
	body := blk.Body()

	// Find the subnet_mapping sub-blocks (may be zero, one, or many).
	var mappings []*hclwrite.Block
	for _, sub := range body.Blocks() {
		if sub.Type() == "subnet_mapping" {
			mappings = append(mappings, sub)
		}
	}
	hasSubnets := body.GetAttribute("subnets") != nil

	// If neither side is present, nothing to reconcile.
	if len(mappings) == 0 && !hasSubnets {
		return
	}
	// If only one side is present, no conflict to resolve.
	if len(mappings) == 0 || !hasSubnets {
		return
	}

	// Both sides present. Decide which to keep based on whether any
	// sub-block carries an operator-supplied static IP pin.
	pinned := false
	for _, m := range mappings {
		mb := m.Body()
		if hasUsableValue(mb, "allocation_id") ||
			hasUsableValue(mb, "private_ipv4_address") ||
			hasUsableValue(mb, "ipv6_address") {
			pinned = true
			break
		}
	}

	if pinned {
		body.RemoveAttribute("subnets")
		return
	}
	for _, m := range mappings {
		body.RemoveBlock(m)
	}
}

// fixupSubnetProviderQuirks resolves three terraform-provider-aws schema
// constraints that `terraform plan -generate-config-out` emits in
// violation of:
//
//   - availability_zone vs availability_zone_id (mutually exclusive). The
//     provider rejects both being specified. generate-config-out always
//     emits both for any subnet that has an AZ assignment. Drop the ID
//     in favor of the human-readable AZ. Issue #343.
//   - enable_lni_at_device_index = 0 (provider rejects literal 0; the
//     attribute's documented domain starts at 1). generate-config-out
//     emits 0 for any subnet not configured for Local Network Interfaces.
//     Issue #344.
//   - map_customer_owned_ip_on_launch orphan (mutually-required trio with
//     customer_owned_ipv4_pool and outpost_arn). generate-config-out emits
//     `map_customer_owned_ip_on_launch = false` standalone for every
//     non-Outpost subnet, breaking the all-of constraint. Drop the orphan
//     when neither sibling carries a usable value. Issue #344.
//
// Conservative shape: each transform fires only on its specific orphan
// pattern. A real Outpost-pinned subnet (outpost_arn set) preserves the
// trio; a real LNI-configured subnet (index >=1) preserves the index; an
// AZ-id-only subnet preserves the ID.
func fixupSubnetProviderQuirks(blk *hclwrite.Block) {
	body := blk.Body()
	// #343 — AZ vs AZ-ID mutual exclusion.
	if hasUsableValue(body, "availability_zone") && hasUsableValue(body, "availability_zone_id") {
		body.RemoveAttribute("availability_zone_id")
	}
	// #344a — enable_lni_at_device_index = 0 (provider rejects literal 0).
	if isAttrLiteralZero(body, "enable_lni_at_device_index") {
		body.RemoveAttribute("enable_lni_at_device_index")
	}
	// #344b — map_customer_owned_ip_on_launch orphan trio.
	if !hasUsableValue(body, "customer_owned_ipv4_pool") && !hasUsableValue(body, "outpost_arn") {
		body.RemoveAttribute("map_customer_owned_ip_on_launch")
	}
}

// fixupRouteTableEmptyRouteFields drops empty-string fields from each
// route object literal emitted in aws_route_table.route. The provider
// validates fields like ipv6_cidr_block as a CIDR when present (even
// as ""), and rejects the empty literal with `"" is not a valid CIDR
// block`. terraform plan -generate-config-out emits "" for every
// absent string field in the route object; broad-strip is safer than
// targeted because future provider validation tightening on other
// fields surfaces the same way.
//
// Shape note: generate-config-out emits route as an attribute carrying
// a list-of-objects expression (route = [{...}, ...]), NOT as nested
// route { ... } blocks. Mutation goes through cty round-trip rather
// than block iteration: parse the attribute's expression bytes,
// evaluate to a static cty value, filter empty-string fields per
// object, re-emit via SetAttributeValue. Issue #345.
func fixupRouteTableEmptyRouteFields(blk *hclwrite.Block) {
	body := blk.Body()
	attr := body.GetAttribute("route")
	if attr == nil {
		return
	}
	exprBytes := attr.Expr().BuildTokens(nil).Bytes()
	expr, diags := hclsyntax.ParseExpression(exprBytes, "route", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return
	}
	// expr.Value(nil) fails on any variable / function reference. That's
	// the intended bail-out — generate-config-out emits literals only,
	// and we'd rather no-op than silently drop a reference. Future
	// templating that mixes refs into route would need an EvalContext.
	val, diags := expr.Value(nil)
	if diags.HasErrors() {
		return
	}
	filtered, changed := dropEmptyStringFieldsFromTuple(val)
	if !changed {
		return
	}
	// Defensive: if any element became an empty object (every field was
	// "" — degenerate but not impossible), bail rather than emit
	// `route = [{}]`, which would convert the existing CIDR-validation
	// error into a different missing-required-arg failure.
	it := filtered.ElementIterator()
	for it.Next() {
		_, elem := it.Element()
		if elem.Type().IsObjectType() && len(elem.AsValueMap()) == 0 {
			return
		}
	}
	body.SetAttributeValue("route", filtered)
}

// dropEmptyStringFieldsFromTuple walks a tuple/list of object values and
// returns a new tuple with each object's empty-string fields removed.
// The boolean reports whether any field was dropped, so callers can
// short-circuit a no-op back to the original tokens (avoids reformatting
// unchanged HCL). Non-tuple/non-list inputs and unknown/null values pass
// through unchanged.
func dropEmptyStringFieldsFromTuple(v cty.Value) (cty.Value, bool) {
	if v.IsNull() || !v.IsKnown() {
		return v, false
	}
	t := v.Type()
	if !t.IsTupleType() && !t.IsListType() {
		return v, false
	}
	if v.LengthInt() == 0 {
		return v, false
	}
	out := make([]cty.Value, 0, v.LengthInt())
	changed := false
	it := v.ElementIterator()
	for it.Next() {
		_, elem := it.Element()
		cleaned, c := dropEmptyStringFieldsFromObject(elem)
		if c {
			changed = true
		}
		out = append(out, cleaned)
	}
	if !changed {
		return v, false
	}
	return cty.TupleVal(out), true
}

// dropEmptyStringFieldsFromObject returns a new object value with
// empty-string string fields removed. Non-object inputs pass through
// unchanged. If every field is dropped the result is an empty object
// (cty.EmptyObjectVal); the caller decides whether to keep that.
func dropEmptyStringFieldsFromObject(v cty.Value) (cty.Value, bool) {
	if v.IsNull() || !v.IsKnown() {
		return v, false
	}
	if !v.Type().IsObjectType() {
		return v, false
	}
	fields := map[string]cty.Value{}
	changed := false
	for k, fv := range v.AsValueMap() {
		if fv.Type() == cty.String && !fv.IsNull() && fv.IsKnown() && fv.AsString() == "" {
			changed = true
			continue
		}
		fields[k] = fv
	}
	if !changed {
		return v, false
	}
	if len(fields) == 0 {
		return cty.EmptyObjectVal, true
	}
	return cty.ObjectVal(fields), true
}

// isAttrLiteralZero reports whether the named attribute exists and its
// expression is exactly the literal `0` (after whitespace trimming).
// It does NOT match `0.0`, `00`, or any computed expression that
// happens to evaluate to zero — only the raw literal terraform plan
// -generate-config-out would emit for an int-shaped field.
func isAttrLiteralZero(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	return strings.TrimSpace(sb.String()) == "0"
}

// hasUsableValue reports whether the named attribute is both present and
// has a non-null value. terraform plan -generate-config-out routinely
// emits `filename = null` for attributes the schema declares Optional
// but the running resource doesn't carry — those rows are present in
// the AST but contribute nothing to validate-time satisfiability of an
// AtLeastOneOf gate, so the fixup must treat them as missing.
func hasUsableValue(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	return strings.TrimSpace(sb.String()) != "null"
}
