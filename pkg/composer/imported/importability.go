package imported

import "strings"

// Instance-level un-importability classification (#709).
//
// Some AWS resources are *supported types* (they appear in
// registry.SupportedDiscoverTypes and have discoverers) but whose *specific
// instances* can never be adopted into customer Terraform state — the AWS
// provider rejects the import. The two known families:
//
//   - AWS-managed default KMS aliases (alias/aws/rds, alias/aws/ebs, …): the
//     provider refuses any aws_kms_alias under the reserved `alias/aws/`
//     prefix.
//   - Service/parent-managed ENIs (NAT gateway, VPC endpoint, load balancer,
//     Lambda, …): owned by their parent resource, not standalone-importable
//     as aws_network_interface.
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
// It inspects only the ResourceIdentity surface populated at discovery time —
// never live provider schema or rendered HCL — so it produces the same verdict
// everywhere it runs.

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

	// ReasonServiceManagedENI marks an aws_network_interface whose
	// interface_type is owned by an AWS service (nat_gateway, vpc_endpoint,
	// network_load_balancer, lambda, …) rather than a customer. These ENIs
	// are managed by their parent resource and cannot be adopted as a
	// standalone aws_network_interface.
	ReasonServiceManagedENI = "service_managed_eni"
)

// awsManagedKMSAliasPrefix is the reserved alias prefix the AWS provider
// refuses to create or import an aws_kms_alias under.
const awsManagedKMSAliasPrefix = "alias/aws/"

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

// IsServiceManagedENIInterfaceType reports whether an interface_type value
// denotes an AWS-service-managed ENI (and therefore an un-importable one).
// Forward-compatible: any interface_type AWS introduces for a managed ENI
// family is treated as service-managed without a code change here. The empty
// string (attribute absent / standard ENI) is importable.
func IsServiceManagedENIInterfaceType(interfaceType string) bool {
	_, importable := importableENIInterfaceTypes[interfaceType]
	return !importable
}

// UnimportableReason classifies a discovered resource as inherently
// un-importable into customer Terraform state, returning the reason code (one
// of the Reason* consts) or "" when the resource is importable.
//
// KMS aliases are always classifiable — the alias name is the import ID /
// native ID stamped by every discoverer. Service-managed ENIs are classifiable
// only when the discoverer surfaced interface_type into
// NativeIDs["interface_type"]; when it is absent the ENI is treated as
// importable here and the genconfig prune (#708) remains the backstop.
func UnimportableReason(ir ImportedResource) string {
	switch ir.Identity.Type {
	case "aws_kms_alias":
		if IsAWSManagedKMSAliasName(kmsAliasName(ir.Identity)) {
			return ReasonAWSManagedKMSAlias
		}
	case "aws_network_interface":
		if IsServiceManagedENIInterfaceType(eniInterfaceType(ir.Identity)) {
			return ReasonServiceManagedENI
		}
	}
	return ""
}

// ReasonDescription returns a short, operator-facing explanation for an
// un-importability reason code, suitable for a wizard tooltip or a CLI / 422
// message. Returns "" for an unknown code so callers can fall back to a generic
// message.
func ReasonDescription(reason string) string {
	switch reason {
	case ReasonAWSManagedKMSAlias:
		return "AWS-managed KMS alias (reserved alias/aws/* prefix) — cannot be imported into Terraform."
	case ReasonServiceManagedENI:
		return "Service-managed network interface (owned by its parent NAT gateway / VPC endpoint / load balancer) — cannot be imported as a standalone network interface."
	default:
		return ""
	}
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

// eniInterfaceType resolves an ENI's interface_type from
// NativeIDs["interface_type"] (populated by the network-interface discoverer's
// property extractor). Empty when the discoverer did not surface it.
func eniInterfaceType(id ResourceIdentity) string {
	return id.NativeIDs["interface_type"]
}
