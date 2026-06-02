package genconfig

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
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
	res, err := applyResourceTypeFixupsReport(raw)
	return res.HCL, err
}

// NormalizeImportedHCL applies the resource-type fixups to provider-generated
// HCL. It is the exported entry point for the reverse-import pipeline, which
// must run the same normalization over the FINAL imported.tf the composer
// emits — not just genconfig's generated.tf. The final emit is built from
// plan-backfilled attributes (BackfillImportedAttrsFromPlan), and the plan's
// refreshed state can re-introduce mutually-exclusive provider attributes that
// genconfig's pass already resolved — e.g. private_ip_list alongside
// private_ips, or subnet_mapping alongside subnets — which fail
// `terraform validate`. Running the fixups over the emitted HCL keeps the
// shipped artifact consistent with generated.tf. Issue #708.
func NormalizeImportedHCL(raw []byte) ([]byte, error) {
	return applyResourceTypeFixups(raw)
}

// fixupResult is the output of applyResourceTypeFixupsReport: the rewritten
// HCL plus the "TYPE.NAME" addresses a registered fixup ran against.
type fixupResult struct {
	// HCL is the post-fixup generated config.
	HCL []byte
	// Applied lists the addresses a registered fixup closure ran for, for
	// per-resource progress logging. It reflects fixups ATTEMPTED (a
	// registered closure ran for that block) — a useful "what did genconfig
	// touch" signal even though a given closure may be a no-op on a block.
	Applied []string
}

// applyResourceTypeFixupsReport is applyResourceTypeFixups plus the list of
// addresses touched, returned together in a fixupResult.
func applyResourceTypeFixupsReport(raw []byte) (fixupResult, error) {
	f, diags := hclwrite.ParseConfig(raw, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return fixupResult{}, fmt.Errorf("parse for fixups: %s", diags.Error())
	}
	var applied []string
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
		applied = append(applied, labels[0]+"."+labels[1])
	}
	return fixupResult{HCL: f.Bytes(), Applied: applied}, nil
}

// resourceTypeFixups maps a Terraform resource type to its post-cleanup
// fixup closure. Closures may add or remove attributes and sub-blocks.
// They must NOT depend on schema metadata — that's already been applied
// by cleanGenerated.
var resourceTypeFixups = map[string]func(*hclwrite.Block){
	"aws_lambda_function":         fixupLambdaSource,
	"aws_cognito_user_pool":       fixupCognitoVerificationMessageConflict,
	"aws_key_pair":                fixupKeyPairPublicKey,
	"aws_kms_key":                 fixupKMSRotationPeriodZero,
	"aws_dynamodb_table":          fixupDynamoDBPITRRecoveryPeriodZero,
	"aws_vpc":                     fixupVPCIPv6NetmaskOrphan,
	"aws_lb":                      fixupLBSubnetMappingConflict,
	"aws_subnet":                  fixupSubnetProviderQuirks,
	"aws_route_table":             fixupRouteTableEmptyRouteFields,
	"aws_security_group":          fixupSecurityGroupInlineRuleObjects,
	"aws_nat_gateway":             fixupNATGatewaySecondaryIPConflict,
	"aws_lb_listener":             fixupLBListenerStickinessDurationZero,
	"aws_lb_target_group":         fixupLBTargetGroupProviderQuirks,
	"aws_vpc_endpoint":            fixupVPCEndpointEmptyDNSDomains,
	"aws_db_instance":             fixupDBInstanceProviderQuirks,
	"aws_secretsmanager_secret":   fixupSecretsManagerSecretDefaults,
	"aws_sns_topic":               fixupSNSTopicSignatureVersionZero,
	"aws_ebs_volume":              fixupEBSVolumeInitializationRateZero,
	"aws_network_interface":       fixupNetworkInterfaceProviderQuirks,
	"aws_iam_role":                fixupIAMRoleNamePrefixConflict,
	"aws_instance":                fixupInstanceProviderQuirks,
	"aws_cloudwatch_metric_alarm": fixupMetricAlarmProviderQuirks,
	"google_compute_firewall":     fixupComputeFirewallEmptySourceTargetArrays,
}

// fixupCognitoVerificationMessageConflict drops Cognito's legacy top-level
// email verification fields when generate-config-out also emits the newer
// verification_message_template block carrying the same settings. The AWS
// provider schema marks the two forms as mutually exclusive, but live
// imports can include both.
func fixupCognitoVerificationMessageConflict(blk *hclwrite.Block) {
	body := blk.Body()
	for _, sub := range body.Blocks() {
		if sub.Type() != "verification_message_template" {
			continue
		}
		subBody := sub.Body()
		if subBody.GetAttribute("email_message") != nil {
			body.RemoveAttribute("email_verification_message")
		}
		if subBody.GetAttribute("email_subject") != nil {
			body.RemoveAttribute("email_verification_subject")
		}
		return
	}
}

// lambdaPlaceholderFile is what we set `filename` to so the block
// validates without holding actual code. It is the shared canonical
// value — the composer's imported.tf emitter injects the identical
// placeholder on the SDK-enrich path (see imported.LambdaPlaceholderFilename).
const lambdaPlaceholderFile = imported.LambdaPlaceholderFilename

// lambdaIgnoreChanges is the set of attributes lifecycle.ignore_changes
// must cover so a real `terraform apply` against this stack will not try
// to re-upload code or churn checksum-derived attrs the operator never
// edits. It is the shared canonical list — the composer's imported.tf
// emitter pins the identical set (see imported.LambdaCodeAttrs).
var lambdaIgnoreChanges = imported.LambdaCodeAttrs

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

// fixupKeyPairPublicKey is the aws_key_pair counterpart to
// fixupLambdaSource (#665). ec2:DescribeKeyPairs never returns the
// public-key material, so an imported aws_key_pair lands with no
// `public_key` — a REQUIRED, ForceNew argument. The fixup injects the
// shared placeholder and pins `public_key` under
// `lifecycle.ignore_changes` so terraform does not force-replace the
// live key pair to match the placeholder. The composer's imported.tf
// emitter (ensureKeyPairPlaceholder) does the identical injection on
// the SDK-enrich path.
func fixupKeyPairPublicKey(blk *hclwrite.Block) {
	body := blk.Body()
	if !hasUsableValue(body, "public_key") {
		body.SetAttributeValue("public_key", cty.StringVal(imported.KeyPairPlaceholderPublicKey))
	}
	for _, sub := range body.Blocks() {
		if sub.Type() == "lifecycle" {
			mergeIgnoreChanges(sub, imported.KeyPairPublicKeyAttr)
			return
		}
	}
	lc := body.AppendNewBlock("lifecycle", nil)
	lc.Body().SetAttributeRaw("ignore_changes", ignoreChangesTokens(imported.KeyPairPublicKeyAttr))
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
	// sub-block carries an operator-supplied static IP pin. A genuine pin
	// has a NON-EMPTY value — the reverse-import backfill (#708) re-adds
	// subnet_mapping blocks with empty-string allocation_id /
	// private_ipv4_address / ipv6_address (and a computed-only outpost_id),
	// which carry no intent and would fail validate ("expected a valid IPv4
	// address, got: "). hasUsableValue alone treats "" as set, so use the
	// stricter hasNonEmptyValue here.
	pinned := false
	for _, m := range mappings {
		mb := m.Body()
		if hasNonEmptyValue(mb, "allocation_id") ||
			hasNonEmptyValue(mb, "private_ipv4_address") ||
			hasNonEmptyValue(mb, "ipv6_address") {
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

// routeTableRouteStringFields is the provider's object shape for
// aws_route_table.route. Terraform treats object attributes as required at
// validate time, even for Optional+Computed set(object(...)) fields, so route
// elements must carry every key with null for the fields AWS left absent.
var routeTableRouteStringFields = []string{
	"carrier_gateway_id",
	"cidr_block",
	"core_network_arn",
	"destination_prefix_list_id",
	"egress_only_gateway_id",
	"gateway_id",
	"ipv6_cidr_block",
	"local_gateway_id",
	"nat_gateway_id",
	"network_interface_id",
	"odb_network_arn",
	"transit_gateway_id",
	"vpc_endpoint_id",
	"vpc_peering_connection_id",
}

// fixupRouteTableEmptyRouteFields replaces empty-string fields with
// null in each route object literal emitted in aws_route_table.route,
// and backfills provider-added route object keys that were absent from
// older generated models.
// The provider's per-field validators (CIDR check on ipv6_cidr_block,
// resource-id format on gateway_id, etc.) reject literal "" but skip
// null. terraform plan -generate-config-out emits "" for every absent
// field in the route object; null-replacement satisfies the validators
// while preserving the object type's field set (the route attribute is
// schema-typed as an object with every field required to be present —
// DROPPING fields breaks the object type and produces a different
// "Incorrect attribute value type" failure). AWS provider 6.47 added
// odb_network_arn; final imported.tf emitted from older generated models
// therefore needs a null placeholder for that key too.
//
// Shape note: generate-config-out emits route as an attribute carrying
// a list-of-objects expression (route = [{...}, ...]), NOT as nested
// route { ... } blocks. Mutation goes through cty round-trip rather
// than block iteration: parse the attribute's expression bytes,
// evaluate to a static cty value, replace "" string fields with
// null per object, re-emit via SetAttributeValue. Issue #345.
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
	filtered, changed := normalizeStringFieldsInTuple(val, routeTableRouteStringFields)
	if !changed {
		return
	}
	body.SetAttributeValue("route", filtered)
}

// normalizeStringFieldsInTuple walks a tuple/list of object values and returns
// a new tuple with each object's empty-string fields replaced by null and any
// missing required string fields added as null. The boolean reports whether any
// field was rewritten, so callers can short-circuit a no-op back to the original
// tokens. Non-tuple/non-list inputs and unknown/null values pass through
// unchanged.
func normalizeStringFieldsInTuple(v cty.Value, requiredFields []string) (cty.Value, bool) {
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
		cleaned, c := normalizeStringFieldsInObject(elem, requiredFields)
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

// normalizeStringFieldsInObject returns a new object value with empty-string
// string fields replaced by null and any missing required string fields added
// as null. Non-object inputs pass through unchanged. The field set is preserved
// or widened to the provider's required object shape — this is the difference
// between "satisfies the schema's type requirement that all route fields be
// present" and "fails with Incorrect attribute value type because a generated
// model omitted a provider-added key."
func normalizeStringFieldsInObject(v cty.Value, requiredFields []string) (cty.Value, bool) {
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
			fields[k] = cty.NullVal(cty.String)
			changed = true
			continue
		}
		fields[k] = fv
	}
	for _, k := range requiredFields {
		if _, ok := fields[k]; ok {
			continue
		}
		fields[k] = cty.NullVal(cty.String)
		changed = true
	}
	if !changed {
		return v, false
	}
	return cty.ObjectVal(fields), true
}

// securityGroupInlineRuleListFields is the collection-valued subset of the
// provider's aws_security_group ingress/egress object shape. The composer may
// omit an empty collection when the typed imported model has nil for that field,
// but terraform validate still requires the key to exist inside each inline
// object. Add empty collections for absent keys so self-referencing default
// rules validate without changing intent.
var securityGroupInlineRuleListFields = []string{
	"cidr_blocks",
	"ipv6_cidr_blocks",
	"prefix_list_ids",
	"security_groups",
}

// fixupSecurityGroupInlineRuleObjects backfills missing empty collection keys in
// aws_security_group ingress/egress object literals. The live failure this
// prevents is a self-referencing default security group rule with `self = true`
// and no CIDR ranges; the imported typed HCL omitted `cidr_blocks`, but the AWS
// provider's set(object(...)) schema requires that attribute to be present.
func fixupSecurityGroupInlineRuleObjects(blk *hclwrite.Block) {
	body := blk.Body()
	for _, attrName := range []string{"ingress", "egress"} {
		attr := body.GetAttribute(attrName)
		if attr == nil {
			continue
		}
		exprBytes := attr.Expr().BuildTokens(nil).Bytes()
		expr, diags := hclsyntax.ParseExpression(exprBytes, attrName, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			continue
		}
		val, diags := expr.Value(nil)
		if diags.HasErrors() {
			continue
		}
		filtered, changed := addMissingListFieldsInTuple(val, securityGroupInlineRuleListFields)
		if changed {
			body.SetAttributeValue(attrName, filtered)
		}
	}
}

func addMissingListFieldsInTuple(v cty.Value, requiredFields []string) (cty.Value, bool) {
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
		cleaned, c := addMissingListFieldsInObject(elem, requiredFields)
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

func addMissingListFieldsInObject(v cty.Value, requiredFields []string) (cty.Value, bool) {
	if v.IsNull() || !v.IsKnown() {
		return v, false
	}
	if !v.Type().IsObjectType() {
		return v, false
	}
	fields := v.AsValueMap()
	changed := false
	for _, k := range requiredFields {
		if _, ok := fields[k]; ok {
			continue
		}
		fields[k] = cty.ListValEmpty(cty.String)
		changed = true
	}
	if !changed {
		return v, false
	}
	return cty.ObjectVal(fields), true
}

// fixupNATGatewaySecondaryIPConflict resolves the
// secondary_private_ip_address_count vs secondary_private_ip_addresses
// mutual-exclusion. terraform plan -generate-config-out emits both
// (count = 0, addresses = []) for every NAT gateway not configured
// with secondary IPs. The provider rejects co-presence even when both
// carry the no-info shape.
//
// Heuristic: when both attrs carry the no-info shape (count = 0 AND
// addresses = []), drop both — neither carries information. When
// either side carries a meaningful value, keep it and drop the other.
// Issue #348.
func fixupNATGatewaySecondaryIPConflict(blk *hclwrite.Block) {
	body := blk.Body()
	hasCount := body.GetAttribute("secondary_private_ip_address_count") != nil
	hasAddrs := body.GetAttribute("secondary_private_ip_addresses") != nil
	if !hasCount || !hasAddrs {
		return
	}
	countZero := isAttrLiteralZero(body, "secondary_private_ip_address_count")
	addrsEmpty := isAttrLiteralEmptyList(body, "secondary_private_ip_addresses")
	switch {
	case countZero && addrsEmpty:
		// Both no-info — drop both.
		body.RemoveAttribute("secondary_private_ip_address_count")
		body.RemoveAttribute("secondary_private_ip_addresses")
	case countZero:
		// Operator pinned addresses; the count is redundant + conflicts.
		body.RemoveAttribute("secondary_private_ip_address_count")
	case addrsEmpty:
		// Operator pinned count; the empty list is redundant + conflicts.
		body.RemoveAttribute("secondary_private_ip_addresses")
	}
}

// fixupLBListenerStickinessDisabledDropped drops the entire stickiness
// sub-block from default_action.forward when its enabled = false.
// terraform plan -generate-config-out emits a stickiness block
// carrying `enabled = false` (and `duration = 0`, which schema cleanup
// drops as Computed-default before this fixup runs) for any forward
// target group not configured for stickiness. The provider treats
// `duration` as a required argument whenever the stickiness block is
// present — so leaving an empty `stickiness { enabled = false }`
// block fails validate with "Missing required argument". Drop the
// entire stickiness block when disabled; the provider treats
// stickiness as optional and accepts its absence. Issue #349.
func fixupLBListenerStickinessDurationZero(blk *hclwrite.Block) {
	for _, da := range blk.Body().Blocks() {
		if da.Type() != "default_action" {
			continue
		}
		for _, fwd := range da.Body().Blocks() {
			if fwd.Type() != "forward" {
				continue
			}
			for _, st := range fwd.Body().Blocks() {
				if st.Type() != "stickiness" {
					continue
				}
				// Drop the whole block when stickiness is disabled.
				// A real stickiness configuration carrying duration is
				// preserved (enabled = true with duration set).
				if isAttrLiteralFalse(st.Body(), "enabled") {
					fwd.Body().RemoveBlock(st)
				}
			}
		}
	}
}

// fixupLBTargetGroupProviderQuirks resolves three terraform-provider-aws
// schema constraints terraform plan -generate-config-out emits in
// violation of:
//
//   - target_control_port = 0 (provider rejects literal 0; range
//     1-65535). Drop the literal-zero attribute.
//   - target_failover block with on_deregistration = null AND
//     on_unhealthy = null (both required by schema; null fails the
//     required check). Drop the entire block.
//   - target_health_state block with
//     enable_unhealthy_connection_termination = null (required by
//     schema). Drop the entire block.
//
// Conservative: each block-drop fires only when the required field is
// the null literal. A real configuration carrying meaningful values
// preserves the block. Issue #350.
func fixupLBTargetGroupProviderQuirks(blk *hclwrite.Block) {
	body := blk.Body()
	if isAttrLiteralZero(body, "target_control_port") {
		body.RemoveAttribute("target_control_port")
	}
	for _, sub := range body.Blocks() {
		switch sub.Type() {
		case "target_failover":
			if isAttrLiteralNull(sub.Body(), "on_deregistration") && isAttrLiteralNull(sub.Body(), "on_unhealthy") {
				body.RemoveBlock(sub)
			}
		case "target_health_state":
			if isAttrLiteralNull(sub.Body(), "enable_unhealthy_connection_termination") {
				body.RemoveBlock(sub)
			}
		}
	}
}

// fixupDBInstanceProviderQuirks resolves two terraform-provider-aws
// schema constraints that `terraform plan -generate-config-out` emits
// in violation of:
//
//   - domain_dns_ips = [] (provider rejects empty literal; the schema's
//     MinItems=2 list requires either 0 or 2+ items, and 0 must mean
//     "attribute absent", not "empty list"). generate-config-out emits
//     `[]` for every aws_db_instance that has no AD-domain auth
//     configured. Drop the empty literal. Issue #358.
//   - db_name and username set on a read replica (replicate_source_db
//     has a usable value). Both attrs are source-inherited on a replica
//     and the provider marks them mutually-exclusive with
//     replicate_source_db. generate-config-out doesn't honor that
//     constraint when emitting the replica's body. Drop both attrs
//     when the row is a replica. Issue #359.
//
// Conservative: each transform fires only on its specific orphan
// pattern. A real Domain-auth DB carrying a populated domain_dns_ips
// is preserved; a standalone (non-replica) DB carrying db_name and
// username is preserved.
func fixupDBInstanceProviderQuirks(blk *hclwrite.Block) {
	body := blk.Body()
	// #358 — domain_dns_ips=[] violates MinItems=2 list constraint.
	if isAttrLiteralEmptyList(body, "domain_dns_ips") {
		body.RemoveAttribute("domain_dns_ips")
	}
	// #359 — db_name and username conflict with replicate_source_db on
	// read replicas. Source-inherited attributes that the provider
	// rejects when both sides are set.
	if hasUsableValue(body, "replicate_source_db") {
		body.RemoveAttribute("db_name")
		body.RemoveAttribute("username")
	}
}

// fixupSecretsManagerSecretDefaults replaces the literal `null`
// emitted by `terraform plan -generate-config-out` for two
// default-rich Optional+Computed attributes with their schema
// defaults, so the next plan after import is no-op rather than
// "1 to change":
//
//   - force_overwrite_replica_secret = null → false. The provider
//     marks this write-only; -generate-config-out can't read a real
//     value back from AWS, so it emits null. The provider's
//     schema default is false, and on the next plan the diff
//     "null → false" shows as an in-place update. Pinning false in
//     generated.tf eliminates the spurious diff.
//   - recovery_window_in_days = null → 30. AWS API leaves the field
//     unset on a non-pending-deletion secret, so the provider Reads
//     nil. The schema default is 30 days, and on the next plan the
//     diff "null → 30" shows as an in-place update. Pinning 30 in
//     generated.tf eliminates the spurious diff.
//
// Conservative shape: each transform fires only on the literal
// `null`. A secret deliberately configured with a different
// recovery_window (e.g. 7 days) preserves -generate-config-out's
// emitted value because the literal won't be `null`. Same shape as
// fixupKMSRotationPeriodZero. Issue #361.
func fixupSecretsManagerSecretDefaults(blk *hclwrite.Block) {
	body := blk.Body()
	if isAttrLiteralNull(body, "force_overwrite_replica_secret") {
		body.SetAttributeValue("force_overwrite_replica_secret", cty.False)
	}
	if isAttrLiteralNull(body, "recovery_window_in_days") {
		body.SetAttributeValue("recovery_window_in_days", cty.NumberIntVal(30))
	}
}

// fixupSNSTopicSignatureVersionZero drops the invalid literal zero
// terraform plan -generate-config-out can emit for SNS topics. The AWS
// provider validates signature_version as 1 or 2; zero is the unset
// provider-readback shape and carries no operator intent. Issue #708.
func fixupSNSTopicSignatureVersionZero(blk *hclwrite.Block) {
	if isAttrLiteralZero(blk.Body(), "signature_version") {
		blk.Body().RemoveAttribute("signature_version")
	}
}

// fixupEBSVolumeInitializationRateZero drops the invalid literal zero
// terraform plan -generate-config-out emits for aws_ebs_volume's
// volume_initialization_rate. The AWS provider validates the rate in the
// range 100-300 MiB/s (zero is the unset provider-readback shape and carries
// no operator intent); leaving it at 0 fails `terraform validate` with
// "expected volume_initialization_rate to be in the range (100 - 300), got 0".
// Same family as the SNS signature_version=0 quirk. Issue #708.
func fixupEBSVolumeInitializationRateZero(blk *hclwrite.Block) {
	if isAttrLiteralZero(blk.Body(), "volume_initialization_rate") {
		blk.Body().RemoveAttribute("volume_initialization_rate")
	}
}

// fixupNetworkInterfaceProviderQuirks resolves the over-emission terraform
// plan -generate-config-out produces for aws_network_interface. The provider
// schema exposes the same data through three mutually-exclusive interfaces,
// but generate-config-out emits ALL of them, so `terraform validate` reports
// a wall of "Conflicting configuration arguments":
//
//   - the *_list form (private_ip_list, ipv6_address_list) plus its
//     *_list_enabled flag conflicts with the plural form (private_ips,
//     ipv6_addresses). We always drop the *_list interface — it is the
//     alternative representation and the plural form carries the same data.
//   - each plural list (private_ips, ipv6_addresses, ipv4_prefixes,
//     ipv6_prefixes) conflicts with its *_count sibling. generate-config-out
//     emits both: a populated list AND a literal-zero count, or an empty
//     list AND a zero count. We keep whichever side carries intent (a
//     non-empty list, else a non-zero count) and drop the other; when both
//     are empty/zero we drop both so the attribute defaults.
//
// interface_type is also normalized: the literal "interface" is the
// describe-only value generate-config-out emits for a standard ENI, but the
// provider only accepts efa/efa-only/branch/trunk on create, so we drop it.
// Older enriched attrs can also carry the string literal "null"; drop that as
// an absent value rather than rendering invalid provider input.
// Service-managed interface_type values (nat_gateway, vpc_endpoint, …) are
// left in place — pruneUnimportable drops those whole resources, since a
// service-owned ENI cannot be adopted as a standalone aws_network_interface.
// Issue #708.
func fixupNetworkInterfaceProviderQuirks(blk *hclwrite.Block) {
	body := blk.Body()

	// The *_list interface is always the redundant alternative form.
	for _, name := range []string{
		"private_ip_list", "private_ip_list_enabled",
		"ipv6_address_list", "ipv6_address_list_enabled",
	} {
		body.RemoveAttribute(name)
	}

	// Resolve each plural/count conflict, preferring the side with intent.
	for _, pair := range [][2]string{
		{"private_ips", "private_ips_count"},
		{"ipv6_addresses", "ipv6_address_count"},
		{"ipv4_prefixes", "ipv4_prefix_count"},
		{"ipv6_prefixes", "ipv6_prefix_count"},
	} {
		resolveENIListCountConflict(body, pair[0], pair[1])
	}

	if v := stringLitFromAttr(body.GetAttribute("interface_type")); v == "interface" || v == "null" {
		body.RemoveAttribute("interface_type")
	}
}

// resolveENIListCountConflict drops one (or both) of a mutually-exclusive
// aws_network_interface plural-list / *_count attribute pair so only the
// side carrying operator intent survives:
//
//   - a non-empty list wins → drop the count
//   - else a non-zero count wins → drop the (empty) list
//   - else both are empty/zero → drop both so the provider default applies
func resolveENIListCountConflict(body *hclwrite.Body, listName, countName string) {
	listHasValue := hasUsableValue(body, listName) && !isAttrLiteralEmptyList(body, listName)
	countHasValue := body.GetAttribute(countName) != nil && !isAttrLiteralZero(body, countName) && hasUsableValue(body, countName)
	switch {
	case listHasValue:
		body.RemoveAttribute(countName)
	case countHasValue:
		body.RemoveAttribute(listName)
	default:
		body.RemoveAttribute(listName)
		body.RemoveAttribute(countName)
	}
}

// fixupComputeFirewallEmptySourceTargetArrays is the first GCP-side
// entry in resourceTypeFixups. terraform plan -generate-config-out
// emits all four source/target arrays as literal `[]` even when only
// one of the source pairs is configured:
//
//	source_service_accounts = []
//	source_tags             = []
//	target_service_accounts = []
//	target_tags             = []
//
// The Google provider rejects the combination — source_service_accounts
// is mutually-exclusive with source_tags, and target_service_accounts
// is mutually-exclusive with source_tags (asymmetric on the target
// side — target_tags is also caught by the cross-validator).
//
// Drop any of the four whose emitted value is the empty literal `[]`.
// Non-empty values (the operator did configure one side of the pair)
// are preserved. Same family as AWS #338/#343/#348/#351. Issue #363.
func fixupComputeFirewallEmptySourceTargetArrays(blk *hclwrite.Block) {
	body := blk.Body()
	for _, name := range []string{
		"source_service_accounts",
		"source_tags",
		"target_service_accounts",
		"target_tags",
	} {
		if isAttrLiteralEmptyList(body, name) {
			body.RemoveAttribute(name)
		}
	}
}

// fixupVPCEndpointEmptyDNSDomains drops the empty
// private_dns_specified_domains list inside the dns_options nested
// block. The provider's schema marks the list as MinItems=1 — empty
// list violates the constraint. The dns_options block accepts the
// field's absence as "default to nil". Issue #351.
func fixupVPCEndpointEmptyDNSDomains(blk *hclwrite.Block) {
	for _, sub := range blk.Body().Blocks() {
		if sub.Type() != "dns_options" {
			continue
		}
		if isAttrLiteralEmptyList(sub.Body(), "private_dns_specified_domains") {
			sub.Body().RemoveAttribute("private_dns_specified_domains")
		}
	}
}

// fixupIAMRoleNamePrefixConflict drops aws_iam_role.name_prefix when name
// is also present. terraform plan -generate-config-out emits BOTH for an
// imported role, but the provider marks them mutually exclusive ("name":
// conflicts with name_prefix). The explicit name is authoritative for an
// existing role, so the derived name_prefix is the one to drop. Conservative:
// fires only when both carry a usable value. (name/name_prefix is a common
// generate-config-out conflict; this helper can be reused for other types
// that surface it.)
func fixupIAMRoleNamePrefixConflict(blk *hclwrite.Block) {
	body := blk.Body()
	if hasUsableValue(body, "name") && hasUsableValue(body, "name_prefix") {
		body.RemoveAttribute("name_prefix")
	}
}

// instancePrimaryENIConflicts is the set of top-level aws_instance network
// attributes the provider marks mutually exclusive with a
// primary_network_interface block. When an instance is launched with an
// explicit primary ENI, all of its networking lives on the interface — but
// terraform plan -generate-config-out still emits these scalars too, so the
// provider rejects the pair (e.g. "primary_network_interface": conflicts with
// private_ip). The interface block is authoritative for an imported instance,
// so the redundant top-level attrs are the ones to drop.
var instancePrimaryENIConflicts = []string{
	"associate_public_ip_address",
	"private_ip",
	"secondary_private_ips",
	"security_groups",
	"source_dest_check",
	"subnet_id",
	"vpc_security_group_ids",
	"ipv6_address_count",
	"ipv6_addresses",
}

// fixupInstanceProviderQuirks resolves aws_instance mutual-exclusion conflicts
// that terraform plan -generate-config-out emits:
//
//   - When a primary_network_interface block is present, drop every top-level
//     network attribute it conflicts with (instancePrimaryENIConflicts) — the
//     interface governs the instance's networking.
//   - Otherwise, ipv6_address_count conflicts with ipv6_addresses:
//     generate-config-out emits both (count = 0, addresses = []) for a
//     non-IPv6 instance. Both empty convey no intent, so drop both; if exactly
//     one is empty, drop the empty one and keep the real value.
//
// Conservative: each transform fires only on its specific emitted pattern, and
// RemoveAttribute is a no-op for attrs the block doesn't carry.
func fixupInstanceProviderQuirks(blk *hclwrite.Block) {
	body := blk.Body()

	for _, sub := range body.Blocks() {
		if sub.Type() == "primary_network_interface" {
			for _, attr := range instancePrimaryENIConflicts {
				body.RemoveAttribute(attr)
			}
			return
		}
	}

	// No primary ENI: ipv6_address_count vs ipv6_addresses mutual exclusion.
	hasCount := body.GetAttribute("ipv6_address_count") != nil
	hasAddrs := body.GetAttribute("ipv6_addresses") != nil
	if hasCount && hasAddrs {
		countZero := isAttrLiteralZero(body, "ipv6_address_count")
		addrsEmpty := isAttrLiteralEmptyList(body, "ipv6_addresses")
		switch {
		case countZero && addrsEmpty:
			body.RemoveAttribute("ipv6_address_count")
			body.RemoveAttribute("ipv6_addresses")
		case countZero:
			body.RemoveAttribute("ipv6_address_count")
		case addrsEmpty:
			body.RemoveAttribute("ipv6_addresses")
		default:
			// Both carry real values (generate-config-out shouldn't, but be
			// safe): keep the explicit address list, drop the count.
			body.RemoveAttribute("ipv6_address_count")
		}
	}
}

// fixupMetricAlarmProviderQuirks drops zero-valued attributes that
// terraform plan -generate-config-out emits for aws_cloudwatch_metric_alarm
// and the provider then rejects:
//
//   - datapoints_to_alarm = 0: the provider's validator pins it to >= 1;
//     generate-config-out emits 0 when the alarm doesn't set it. null/absent
//     is valid, literal 0 is not.
//   - evaluation_interval = 0: an orphan of the mutually-required pair
//     {evaluation_criteria, evaluation_interval} (anomaly-detection alarms).
//     generate-config-out emits evaluation_interval = 0 standalone for a
//     standard alarm, breaking the all-of constraint. Drop it when its
//     sibling evaluation_criteria has no usable value.
//
// Conservative: only the literal-0 orphan patterns fire; a real anomaly
// alarm carrying both keys is preserved.
func fixupMetricAlarmProviderQuirks(blk *hclwrite.Block) {
	body := blk.Body()
	if isAttrLiteralZero(body, "datapoints_to_alarm") {
		body.RemoveAttribute("datapoints_to_alarm")
	}
	if isAttrLiteralZero(body, "evaluation_interval") && !hasUsableValue(body, "evaluation_criteria") {
		body.RemoveAttribute("evaluation_interval")
	}
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

// hasNonEmptyValue is hasUsableValue plus an empty-string-literal check: it
// reports present AND not `null` AND not `""`. Used where an empty-string
// readback carries no operator intent and must not be mistaken for a real
// value — e.g. the LB subnet_mapping pin fields the reverse-import backfill
// re-adds as `private_ipv4_address = ""`.
func hasNonEmptyValue(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	s := strings.TrimSpace(sb.String())
	return s != "null" && s != `""` && s != ""
}

// isAttrLiteralNull reports whether the named attribute exists and its
// expression is exactly the literal `null` (after whitespace trimming).
// Mirrors isAttrLiteralZero — only the raw literal terraform plan
// -generate-config-out would emit for a Required-but-unset attribute,
// not any computed expression that evaluates to null.
func isAttrLiteralNull(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	return strings.TrimSpace(sb.String()) == "null"
}

// isAttrLiteralEmptyList reports whether the named attribute exists and
// its expression is exactly the literal `[]` (after whitespace
// trimming). Mirrors isAttrLiteralZero — only the raw literal
// terraform plan -generate-config-out would emit for an empty
// list-shaped field, not any computed expression that evaluates to
// an empty list.
func isAttrLiteralEmptyList(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	return strings.TrimSpace(sb.String()) == "[]"
}

// isAttrLiteralFalse reports whether the named attribute exists and its
// expression is exactly the literal `false` (after whitespace
// trimming). Mirrors isAttrLiteralZero / isAttrLiteralNull — only the
// raw literal terraform plan -generate-config-out would emit, not any
// computed expression evaluating to false.
func isAttrLiteralFalse(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	return strings.TrimSpace(sb.String()) == "false"
}
