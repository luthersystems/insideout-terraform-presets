package imported

import "strings"

// Instance-level un-importability classification (#709).
//
// Some resources are *supported types* (they appear in
// registry.SupportedDiscoverTypes and have discoverers) but whose *specific
// instances* must not be offered for a new import. The known families:
//
//   - AWS-managed default KMS aliases (alias/aws/rds, alias/aws/ebs, …): the
//     provider refuses any aws_kms_alias under the reserved `alias/aws/`
//     prefix.
//   - AWS-managed KMS keys (KeyManager == "AWS", e.g. the ACM default key):
//     the provider's read calls kms:GetKeyRotationStatus, which AWS-managed
//     keys deny, so importing one fails.
//   - Service/parent-managed ENIs (NAT gateway, VPC endpoint, load balancer,
//     Lambda, …): owned by their parent resource, not standalone-importable
//     as aws_network_interface.
//   - Resources already stamped with InsideOut's imported marker tag/label:
//     already managed by InsideOut, so they are not selectable for another
//     import run.
//
// This predicate is the single source of truth shared across the pipeline so
// the reason codes never drift:
//
//   - discovery (cmd/insideout-import): route these instances into the
//     unsupported set with a reason so the wizard greys them out instead of
//     offering them (#709);
//   - reverse-import genconfig (cmd/insideout-import/genconfig): the
//     defense-in-depth prune that drops them from a selection so a stale
//     selection still imports cleanly (#708);
//   - reliable's importer wizard + reverse-run persist gate (reliable#1967):
//     stamp support=unsupported on the DTO and reject the run path.
//
// It inspects only the ResourceIdentity surface populated at discovery time
// (including Tags/labels) — never live provider schema or rendered HCL — so it
// produces the same verdict everywhere it runs.

// Reason codes for instance-level un-importability. Stable wire identifiers
// carried in the unsupported.json `reason` field (#709), the reverse-import
// imports-skipped.json `reason` field (#708), and consumed by reliable
// (reliable#1967). Adding a code is additive; renaming one is a wire-format
// break — bump the reliable consumer in lockstep.
const (
	// ReasonAWSManagedKMSAlias marks an aws_kms_alias whose name carries the
	// reserved `alias/aws/` prefix — an AWS-managed default alias
	// (alias/aws/rds, alias/aws/ebs, …). The provider rejects creating or
	// importing any alias with that prefix.
	ReasonAWSManagedKMSAlias = "aws_managed_kms_alias"

	// ReasonAWSManagedKMSKey marks an aws_kms_key whose KeyManager is "AWS"
	// — an AWS-managed key (e.g. the ACM default key) rather than a
	// customer-managed one. The provider's read path calls
	// kms:GetKeyRotationStatus, which AWS-managed keys deny, so the key
	// cannot be imported into customer Terraform state.
	ReasonAWSManagedKMSKey = "aws_managed_kms_key"

	// ReasonServiceManagedENI marks an aws_network_interface whose
	// interface_type is owned by an AWS service (nat_gateway, vpc_endpoint,
	// network_load_balancer, lambda, …) rather than a customer. These ENIs
	// are managed by their parent resource and cannot be adopted as a
	// standalone aws_network_interface.
	ReasonServiceManagedENI = "service_managed_eni"

	// ReasonServiceManaged marks an instance that an AWS/GCP service owns on
	// the customer's behalf, detected via a non-empty Identity.ServiceManagedBy
	// marker (e.g. an EventBridge rule's ManagedBy =
	// "autoscaling.amazonaws.com"). Terraform cannot manage these resources at
	// all — tag, create, and delete operations are categorically rejected by
	// the provider (EventBridge surfaces ManagedRuleException, #785). Generic
	// across types: any discoverer that surfaces a service-owner marker routes
	// the instance here.
	ReasonServiceManaged = "service_managed"

	// ReasonEphemeralLogStream marks an aws_cloudwatch_log_stream. Individual
	// log streams are created automatically by their log group as events
	// arrive and are continuously rotated — they are not declarative
	// infrastructure (Terraform manages the aws_cloudwatch_log_group, not its
	// streams), and terraform plan -generate-config-out emits no body for
	// them. The whole type is un-importable, so streams are classified into
	// unsupported.json instead of being silently dropped as no_generated_config.
	ReasonEphemeralLogStream = "ephemeral_log_stream"

	// ReasonInsideOutImported marks a resource already carrying InsideOut's
	// adopted-resource marker tag/label. Discovery routes these rows to
	// unsupported.json so another import run cannot select a resource already
	// managed by InsideOut. The marker key is the signal; a project/account tag
	// alone is not enough.
	ReasonInsideOutImported = "insideout_imported"

	// ReasonServiceLinkedIAMRole marks an aws_iam_role whose ARN carries the
	// reserved `role/aws-service-role/` path segment — an AWS service-linked
	// role (AWSServiceRoleFor…). Service-linked roles are created and deleted
	// by the owning AWS service, not the customer; the IAM API rejects an
	// out-of-band create/delete, so they cannot be adopted into customer
	// Terraform state. The bulk list discoverer already excludes them
	// (listIAMRoleNamesNonSLR), but the dep-chase DiscoverByID path can still
	// reach one via a cross-resource ARN reference (e.g. a role= literal
	// pointing at a service-linked role); this reason lets that path leave the
	// literal knowingly instead of dropping it as an opaque no_generated_config
	// orphan (presets#834).
	ReasonServiceLinkedIAMRole = "service_linked_iam_role"

	// ReasonAWSDefaultParameterGroup marks an AWS-managed default parameter
	// group (ElastiCache `default.<family>` / RDS `default.<family>` names —
	// the reserved `default.` prefix customers cannot create under). AWS
	// creates one per engine family; they import into state, but every
	// mutating operation is rejected — the triggering case was the imported
	// Project tag stamp failing the WHOLE apply with "InvalidParameterValue:
	// Tagging on default resources is not supported" (20 default ElastiCache
	// parameter groups on a whole-account staging import). Not customer
	// infrastructure; manage a custom parameter group instead.
	ReasonAWSDefaultParameterGroup = "aws_default_parameter_group"

	// ReasonAWSManagedEventRule marks an aws_cloudwatch_event_rule whose name
	// is a well-known AWS-managed rule (AutoScalingManagedRule, …). AWS sets
	// ManagedBy on these rules and rejects tag / PutRule / DeleteRule with a
	// ManagedRuleException, so the rule cannot be managed by Terraform. The
	// generic ReasonServiceManaged path already catches these WHEN discovery
	// surfaced the ManagedBy marker (Identity.ServiceManagedBy) — but a stale
	// imported baseline persisted before discovery learned to backfill
	// ManagedBy carries no marker, so this NAME-based classifier is the
	// compose-time backstop that still drops the rule (DropUnimportable in
	// ComposeStackWithIssues, #860). Triggering incident: staging apply
	// ccs-101d5181-5czmw, where the imported Project-tag stamp on
	// AutoScalingManagedRule (arn …:rule/AutoScalingManagedRule) failed the
	// whole 264-import apply with ManagedRuleException.
	ReasonAWSManagedEventRule = "aws_managed_event_rule"
)

// awsDefaultParameterGroupPrefix is the reserved name prefix of AWS-managed
// default parameter groups (ElastiCache and RDS both use `default.<family>`;
// the API rejects customer-created groups under it).
const awsDefaultParameterGroupPrefix = "default."

// awsDefaultParameterGroupTypes is the set of Terraform types whose
// AWS-managed `default.*` instances are un-importable (tagging and every
// other mutation is rejected by the service).
var awsDefaultParameterGroupTypes = map[string]struct{}{
	"aws_elasticache_parameter_group": {},
	"aws_db_parameter_group":          {},
	"aws_rds_cluster_parameter_group": {},
}

// IsAWSDefaultParameterGroupName reports whether a parameter-group name is an
// AWS-managed default (reserved `default.` prefix).
func IsAWSDefaultParameterGroupName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), awsDefaultParameterGroupPrefix)
}

// awsManagedEventRuleNames is the allowlist of well-known AWS-managed
// EventBridge rule names. AWS creates these on the customer's behalf and
// stamps ManagedBy on them, so tag / PutRule / DeleteRule are all rejected
// with ManagedRuleException — they cannot be managed by Terraform. This is a
// name-based backstop for the case where discovery did NOT surface the live
// ManagedBy marker (a stale imported baseline); a fresh discovery backfills
// Identity.ServiceManagedBy and the generic IsServiceManaged path catches it
// first. Extend by adding the exact rule name AWS uses.
//
//   - AutoScalingManagedRule — the EC2 Auto Scaling capacity-rebalance /
//     lifecycle rule (ManagedBy = autoscaling.amazonaws.com). Triggering
//     incident: staging apply ccs-101d5181-5czmw.
var awsManagedEventRuleNames = map[string]struct{}{
	"AutoScalingManagedRule": {},
}

// IsAWSManagedEventRuleName reports whether an EventBridge rule name is a
// well-known AWS-managed rule (see awsManagedEventRuleNames). The empty string
// (name not surfaced by the discoverer) is treated as importable so an absent
// name never drops a customer rule — matching the KMS-key / ENI posture.
func IsAWSManagedEventRuleName(name string) bool {
	_, ok := awsManagedEventRuleNames[strings.TrimSpace(name)]
	return ok
}

// awsManagedKMSAliasPrefix is the reserved alias prefix the AWS provider
// refuses to create or import an aws_kms_alias under.
const awsManagedKMSAliasPrefix = "alias/aws/"

// The provenance marker key literals live in marker_tags.go (canonical home
// for both this package and the pkg/composer re-exports — reliable#2230
// consolidation; this file previously carried a private duplicate set).

// awsManagedKMSKeyManager is the KeyManager value KMS reports for keys it
// manages on the customer's behalf (vs "CUSTOMER" for customer-managed
// keys). An AWS-managed key cannot be imported into Terraform state.
const awsManagedKMSKeyManager = "AWS"

// serviceLinkedIAMRoleARNSegment is the reserved ARN path segment AWS stamps
// on every service-linked role (the role's Path is "/aws-service-role/…",
// which renders as "role/aws-service-role/…" in the role ARN). A role whose
// ARN carries it is created and owned by an AWS service and cannot be adopted
// into customer Terraform state.
const serviceLinkedIAMRoleARNSegment = ":role/aws-service-role/"

// importableENIInterfaceTypes is the set of interface_type values an
// aws_network_interface can legitimately carry for a customer-managed,
// importable ENI. The empty string means the attribute is absent (the standard
// case). "interface" is the default standard ENI; efa/efa-only/branch/trunk
// are the only other values the provider's interface_type argument accepts on
// create. Every OTHER value (nat_gateway, vpc_endpoint, network_load_balancer,
// lambda, …) denotes a service-managed ENI owned by its parent resource and is
// not standalone-importable.
var importableENIInterfaceTypes = map[string]struct{}{
	"":          {},
	"interface": {},
	"efa":       {},
	"efa-only":  {},
	"branch":    {},
	"trunk":     {},
}

// IsAWSManagedKMSAliasName reports whether an aws_kms_alias name is an
// AWS-managed default alias (reserved `alias/aws/` prefix), which the provider
// cannot import.
func IsAWSManagedKMSAliasName(name string) bool {
	return strings.HasPrefix(name, awsManagedKMSAliasPrefix)
}

// IsAWSManagedKMSKeyManager reports whether a KMS key's KeyManager value
// denotes an AWS-managed key (KeyManager == "AWS"), which the provider
// cannot import. The empty string (KeyManager not surfaced by the
// discoverer) is treated as importable so an absent discriminator never
// drops a customer-managed key — the genconfig prune (#708) remains the
// backstop.
func IsAWSManagedKMSKeyManager(keyManager string) bool {
	return keyManager == awsManagedKMSKeyManager
}

// IsServiceLinkedIAMRoleARN reports whether an aws_iam_role ARN denotes an AWS
// service-linked role (reserved `role/aws-service-role/` path segment), which
// the customer cannot import or manage. The empty string (ARN not surfaced by
// the discoverer) is treated as importable so an absent discriminator never
// drops a customer-managed role — the genconfig prune (#708) remains the
// backstop, matching the KMS-key / ENI posture.
func IsServiceLinkedIAMRoleARN(arn string) bool {
	return strings.Contains(arn, serviceLinkedIAMRoleARNSegment)
}

// IsServiceManaged reports whether an identity carries a service-managed
// marker (Identity.ServiceManagedBy is non-empty after trimming). A
// service-managed instance is owned by an AWS/GCP service and cannot be
// managed by Terraform. Generic across resource types; the discoverer
// populates ServiceManagedBy from the per-type ServiceManagedByFromProperties
// hook.
func IsServiceManaged(id ResourceIdentity) bool {
	return strings.TrimSpace(id.ServiceManagedBy) != ""
}

// IsServiceManagedENIInterfaceType reports whether an interface_type value
// denotes an AWS-service-managed ENI (and therefore an un-importable one).
// Forward-compatible: any interface_type AWS introduces for a managed ENI
// family is treated as service-managed without a code change here. The empty
// string (attribute absent / standard ENI) is importable.
func IsServiceManagedENIInterfaceType(interfaceType string) bool {
	_, importable := importableENIInterfaceTypes[interfaceType]
	return !importable
}

// HasInsideOutImportedMarker reports whether discovery observed the
// adopted-resource marker tag/label stamped by InsideOut. The marker key's
// presence is enough; callers must not infer ownership from the project tag
// because some historical resources carry a bare account/project marker
// without having been imported. Key matching (TrimSpace + case-insensitive)
// is shared with the provenance-owner reader via normalizedTagValue so the
// "is it marked" and "who marked it" questions can never disagree.
func HasInsideOutImportedMarker(tags map[string]string) bool {
	_, ok := normalizedTagValue(tags, AWSTagKeyImported, GCPLabelKeyImported)
	return ok
}

// UnimportableReason classifies a discovered resource as inherently
// un-importable into customer Terraform state, returning the reason code (one
// of the Reason* consts) or "" when the resource is importable.
//
// InsideOut-managed resources are classified by the imported marker key in
// Identity.Tags; the project/account marker alone is deliberately ignored. KMS
// aliases are always classifiable — the alias name is the import ID / native ID
// stamped by every discoverer. AWS-managed KMS keys and service-managed ENIs
// are classifiable only when the discoverer surfaced the discriminator
// (NativeIDs["key_manager"] / NativeIDs["interface_type"]); when it is absent
// the resource is treated as importable here and the genconfig prune (#708)
// remains the backstop.
func UnimportableReason(ir ImportedResource) string {
	if HasInsideOutImportedMarker(ir.Identity.Tags) {
		return ReasonInsideOutImported
	}

	// Service-managed instances are un-importable regardless of type: the
	// owning service rejects tag/create/delete operations (EventBridge
	// ManagedRuleException, #785). Checked here, before the type switch, so
	// any type that surfaces a service-owner marker is covered without a
	// per-type case.
	if IsServiceManaged(ir.Identity) {
		return ReasonServiceManaged
	}

	switch ir.Identity.Type {
	case "aws_iam_role":
		if IsServiceLinkedIAMRoleARN(iamRoleARN(ir.Identity)) {
			return ReasonServiceLinkedIAMRole
		}
	case "aws_kms_alias":
		if IsAWSManagedKMSAliasName(kmsAliasName(ir.Identity)) {
			return ReasonAWSManagedKMSAlias
		}
	case "aws_kms_key":
		if IsAWSManagedKMSKeyManager(kmsKeyManager(ir.Identity)) {
			return ReasonAWSManagedKMSKey
		}
	case "aws_network_interface":
		if IsServiceManagedENIInterfaceType(eniInterfaceType(ir.Identity)) {
			return ReasonServiceManagedENI
		}
	case "aws_cloudwatch_log_stream":
		// Type-level: every log stream is ephemeral and un-importable (see
		// ReasonEphemeralLogStream).
		return ReasonEphemeralLogStream
	case "aws_cloudwatch_event_rule":
		// Name-based backstop for AWS-managed rules (AutoScalingManagedRule,
		// …) when discovery did not surface the live ManagedBy marker — a
		// stale imported baseline. A fresh discovery stamps
		// Identity.ServiceManagedBy and the IsServiceManaged check above wins
		// first (ReasonServiceManaged); this fires only when that marker is
		// absent (incident: staging apply ccs-101d5181-5czmw).
		if IsAWSManagedEventRuleName(eventRuleName(ir.Identity)) {
			return ReasonAWSManagedEventRule
		}
	}
	if _, pg := awsDefaultParameterGroupTypes[ir.Identity.Type]; pg {
		if IsAWSDefaultParameterGroupName(parameterGroupName(ir.Identity)) {
			return ReasonAWSDefaultParameterGroup
		}
	}
	return ""
}

// DropUnimportable returns irs with every resource whose UnimportableReason
// is non-empty removed, plus the dropped (address, reason) pairs for the
// caller's audit trail. Compose consumes this right after
// DropOrphanedChildren and with the same philosophy: one un-manageable
// resource must not fail the entire stack. The canonical case is an
// AWS-managed default parameter group persisted into a session's imported
// baseline before discovery learned to exclude it — it imports, but the
// Project-tag stamp then fails the WHOLE apply ("Tagging on default
// resources is not supported").
func DropUnimportable(irs []ImportedResource) (kept []ImportedResource, dropped [][2]string) {
	kept = make([]ImportedResource, 0, len(irs))
	for _, ir := range irs {
		if reason := UnimportableReason(ir); reason != "" {
			dropped = append(dropped, [2]string{ir.Identity.Address, reason})
			continue
		}
		kept = append(kept, ir)
	}
	return kept, dropped
}

// parameterGroupName resolves a parameter group's name: the import ID is the
// group name for all three parameter-group types; NameHint is the fallback.
func parameterGroupName(id ResourceIdentity) string {
	if n := strings.TrimSpace(id.ImportID); n != "" {
		return n
	}
	return strings.TrimSpace(id.NameHint)
}

// eventRuleName resolves an EventBridge rule's bare name from the identity the
// aws_cloudwatch_event_rule discoverer populates. NameHint carries the CFN
// `Name` property (the bare rule name); ImportID is the `<bus>/<name>` form, so
// the segment after the last `/` is the fallback rule name.
func eventRuleName(id ResourceIdentity) string {
	if n := strings.TrimSpace(id.NameHint); n != "" {
		return n
	}
	imp := strings.TrimSpace(id.ImportID)
	if idx := strings.LastIndex(imp, "/"); idx != -1 {
		return imp[idx+1:]
	}
	return imp
}

// ReasonDescription returns a short, operator-facing explanation for an
// un-importability reason code, suitable for a wizard tooltip or a CLI / 422
// message. Returns "" for an unknown code so callers can fall back to a generic
// message.
func ReasonDescription(reason string) string {
	switch reason {
	case ReasonAWSManagedKMSAlias:
		return "AWS-managed KMS alias (reserved alias/aws/* prefix) — cannot be imported into Terraform."
	case ReasonAWSManagedKMSKey:
		return "AWS-managed KMS key (KeyManager=AWS, e.g. the ACM default key) — cannot be imported into Terraform."
	case ReasonServiceManagedENI:
		return "Service-managed network interface (owned by its parent NAT gateway / VPC endpoint / load balancer) — cannot be imported as a standalone network interface."
	case ReasonServiceManaged:
		return "Service-managed resource (owned by an AWS/GCP service, e.g. an EventBridge ManagedBy rule) — tag, create, and delete operations are rejected by the provider, so it cannot be managed by Terraform."
	case ReasonEphemeralLogStream:
		return "Ephemeral CloudWatch log stream (auto-created and rotated by its log group) — not declarative infrastructure; manage the log group instead."
	case ReasonInsideOutImported:
		return "Already managed by InsideOut — cannot be selected for a new import."
	case ReasonServiceLinkedIAMRole:
		return "AWS service-linked IAM role (role/aws-service-role/* path) — created and deleted by the owning AWS service; cannot be imported into Terraform."
	case ReasonAWSDefaultParameterGroup:
		return "AWS-managed default parameter group (reserved default.* name) — AWS rejects tagging and every other modification; manage a custom parameter group instead."
	case ReasonAWSManagedEventRule:
		return "AWS-managed EventBridge rule (well-known managed rule such as AutoScalingManagedRule, ManagedBy set) — AWS rejects tag / PutRule / DeleteRule with ManagedRuleException; manage a custom rule instead."
	default:
		return ""
	}
}

// iamRoleARN resolves an IAM role's ARN from NativeIDs["arn"] (populated by the
// iam_role discoverer's arnUnderKey("Arn") extractor). Empty when the
// discoverer did not surface it — treated as importable upstream.
func iamRoleARN(id ResourceIdentity) string {
	return id.NativeIDs["arn"]
}

// kmsAliasName resolves the alias name from the identity surface the KMS-alias
// discoverer populates: NativeIDs["name"], then ImportID, then NameHint — all
// three carry the bare alias name (e.g. "alias/aws/rds") for this type.
func kmsAliasName(id ResourceIdentity) string {
	if n := id.NativeIDs["name"]; n != "" {
		return n
	}
	if id.ImportID != "" {
		return id.ImportID
	}
	return id.NameHint
}

// kmsKeyManager resolves a KMS key's KeyManager value from
// NativeIDs["key_manager"] (populated by the kms_key discoverer's property
// extractor when the Cloud Control payload carries KeyManager). Empty when
// the discoverer did not surface it — treated as importable upstream.
func kmsKeyManager(id ResourceIdentity) string {
	return id.NativeIDs["key_manager"]
}

// eniInterfaceType resolves an ENI's interface_type from
// NativeIDs["interface_type"] (populated by the network-interface discoverer's
// property extractor). Empty when the discoverer did not surface it.
func eniInterfaceType(id ResourceIdentity) string {
	return id.NativeIDs["interface_type"]
}
