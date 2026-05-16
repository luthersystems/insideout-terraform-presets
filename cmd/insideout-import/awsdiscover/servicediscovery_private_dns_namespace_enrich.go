package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdtypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// serviceDiscoveryPrivateDNSNamespaceEnricher implements both
// AttributeEnricher and ByIDEnricher for
// aws_service_discovery_private_dns_namespace (#482 Bucket-C push).
// Pairs with the hand-rolled
// servicediscovery_private_dns_namespace.go discoverer (Cloud Control
// returns UnsupportedActionException on READ for the CFN type, so the
// unified path can't enrich this type either).
//
// SDK shape: GetNamespace takes the namespace id and returns the
// namespace metadata — name, description, ARN, and the
// Properties.DnsProperties.HostedZoneId. Notably, the
// servicediscovery SDK does NOT surface VpcId on either the
// ListNamespaces summary OR the GetNamespace response; recovering the
// VPC id requires a Route53 GetHostedZone hop the discoverer already
// performs. The enricher does NOT redo that hop — the VPC id is carried
// on the discoverer-emitted Identity.NativeIDs["vpc_id"], so the
// enricher mapping pulls it from there rather than re-fetching.
//
// Per decision #5, Computed-only TF fields are populated when they
// exist on the API response — `arn`, `hosted_zone`, and `id` are the
// Computed fields here. The Required input `vpc` cannot be re-derived
// from GetNamespace alone, so the enricher composes it from the
// discoverer-emitted Identity.NativeIDs["vpc_id"]; callers refreshing a
// single row via EnrichByID need to populate that NativeID (or live
// without the `vpc` field on the typed payload).
//
// Sensitive fields: none on this resource. Decision #36 redaction
// stays downstream.
type serviceDiscoveryPrivateDNSNamespaceEnricher struct {
	// fetch is overridable for tests. Defaults to a real GetNamespace
	// call against the servicediscovery.Client in EnrichClients.
	fetch func(ctx context.Context, c *servicediscovery.Client, namespaceID string) (*servicediscovery.GetNamespaceOutput, error)
}

// newServiceDiscoveryPrivateDNSNamespaceEnricher returns the production-
// wired enricher. AWSDiscoverer's byTypeEnricher map registers this
// under "aws_service_discovery_private_dns_namespace".
func newServiceDiscoveryPrivateDNSNamespaceEnricher() *serviceDiscoveryPrivateDNSNamespaceEnricher {
	return &serviceDiscoveryPrivateDNSNamespaceEnricher{fetch: defaultServiceDiscoveryPrivateDNSNamespaceFetch}
}

func (serviceDiscoveryPrivateDNSNamespaceEnricher) ResourceType() string {
	return serviceDiscoveryPrivateDNSNamespaceTFType
}

// Enrich populates ir.Attrs with a typed
// AWSServiceDiscoveryPrivateDNSNamespace payload for the namespace
// identified by ir.Identity. Returns ErrEnrichClientUnavailable if
// EnrichClients.ServiceDiscovery is nil; ErrNotFound when GetNamespace
// returns a typed NamespaceNotFound.
func (e serviceDiscoveryPrivateDNSNamespaceEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.ServiceDiscovery == nil {
		return ErrEnrichClientUnavailable
	}
	namespaceID := serviceDiscoveryPrivateDNSNamespaceIDForEnrich(&ir.Identity)
	if namespaceID == "" {
		return fmt.Errorf("service_discovery_private_dns_namespace: cannot derive namespace id from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	out, err := e.fetch(ctx, c.ServiceDiscovery, namespaceID)
	if err != nil {
		var nf *sdtypes.NamespaceNotFound
		if errors.As(err, &nf) {
			return fmt.Errorf("service_discovery_private_dns_namespace %q: %w", namespaceID, ErrNotFound)
		}
		return fmt.Errorf("service_discovery_private_dns_namespace: get %q: %w", namespaceID, err)
	}
	if out == nil || out.Namespace == nil {
		return fmt.Errorf("service_discovery_private_dns_namespace %q: %w", namespaceID, ErrNotFound)
	}

	// Stamp ARN on Identity.NativeIDs (matching the secretsmanager_secret
	// pattern). The pure-mapping helper does NOT touch ir.Identity per
	// the AttributeEnricher contract; this is the only place the
	// enricher writes to it.
	if arn := aws.ToString(out.Namespace.Arn); arn != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["arn"] = arn
	}

	// Pull the VPC id from the discoverer-emitted NativeIDs (Route53
	// GetHostedZone is the only path to it and the discoverer already
	// did that work).
	vpcID := strings.TrimSpace(ir.Identity.NativeIDs["vpc_id"])
	if vpcID == vpcIDPlaceholderUnknown {
		vpcID = ""
	}

	typed := mapServiceDiscoveryPrivateDNSNamespace(out.Namespace, vpcID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("service_discovery_private_dns_namespace: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed payload for the namespace named by
// identity and returns it as the json.RawMessage shape that would land
// in ImportedResource.Attrs. Mirrors Enrich's SDK call + mapping path;
// because EnrichByID does not mutate identity, the ARN that Enrich
// stamps onto NativeIDs is NOT stamped here.
func (e serviceDiscoveryPrivateDNSNamespaceEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New("service_discovery_private_dns_namespace: nil identity")
	}
	if c.ServiceDiscovery == nil {
		return nil, ErrEnrichClientUnavailable
	}
	namespaceID := serviceDiscoveryPrivateDNSNamespaceIDForEnrich(identity)
	if namespaceID == "" {
		return nil, fmt.Errorf("service_discovery_private_dns_namespace: cannot derive namespace id from Identity (Address=%q ImportID=%q NameHint=%q)",
			identity.Address, identity.ImportID, identity.NameHint)
	}
	out, err := e.fetch(ctx, c.ServiceDiscovery, namespaceID)
	if err != nil {
		var nf *sdtypes.NamespaceNotFound
		if errors.As(err, &nf) {
			return nil, fmt.Errorf("service_discovery_private_dns_namespace %q: %w", namespaceID, ErrNotFound)
		}
		return nil, fmt.Errorf("service_discovery_private_dns_namespace: get %q: %w", namespaceID, err)
	}
	if out == nil || out.Namespace == nil {
		return nil, fmt.Errorf("service_discovery_private_dns_namespace %q: %w", namespaceID, ErrNotFound)
	}
	vpcID := strings.TrimSpace(identity.NativeIDs["vpc_id"])
	if vpcID == vpcIDPlaceholderUnknown {
		vpcID = ""
	}
	typed := mapServiceDiscoveryPrivateDNSNamespace(out.Namespace, vpcID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("service_discovery_private_dns_namespace: marshal Attrs: %w", err)
	}
	return raw, nil
}

// serviceDiscoveryPrivateDNSNamespaceIDForEnrich pulls the namespace id
// from the identifiers the discoverer populates. The discoverer emits
// the namespace id on both NativeIDs["namespace_id"] and (as part of
// the comma-delimited "<namespace_id>:<vpc_id>" form) ImportID. Order
// of preference:
//
//  1. Identity.NativeIDs["namespace_id"] — canonical field from the
//     hand-rolled discoverer.
//  2. Identity.ImportID — parsed as "<namespace_id>:<vpc_id>" or bare.
func serviceDiscoveryPrivateDNSNamespaceIDForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if s := strings.TrimSpace(id.NativeIDs["namespace_id"]); s != "" {
		return s
	}
	s := strings.TrimSpace(id.ImportID)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, ":"); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// defaultServiceDiscoveryPrivateDNSNamespaceFetch is the production
// fetch path: a single GetNamespace call.
func defaultServiceDiscoveryPrivateDNSNamespaceFetch(ctx context.Context, c *servicediscovery.Client, namespaceID string) (*servicediscovery.GetNamespaceOutput, error) {
	return c.GetNamespace(ctx, &servicediscovery.GetNamespaceInput{Id: aws.String(namespaceID)})
}

// mapServiceDiscoveryPrivateDNSNamespace is the pure-mapping helper
// shared by Enrich and EnrichByID. The Layer 1 typed surface is flat —
// scalar arn / description / hosted_zone / id / name / vpc plus the
// tags map. The vpc field is special: GetNamespace does not surface
// VpcId, so the caller threads the Route53-resolved value through
// vpcID; an empty vpcID skips the field rather than emitting a placeholder.
//
// Decision-#34 cleanliness: every field is emitted only when present on
// the API response, so the resulting HCL does not contain "field =
// null" noise.
func mapServiceDiscoveryPrivateDNSNamespace(ns *sdtypes.Namespace, vpcID string) *generated.AWSServiceDiscoveryPrivateDNSNamespace {
	typed := &generated.AWSServiceDiscoveryPrivateDNSNamespace{}
	if ns == nil {
		return typed
	}
	if s := aws.ToString(ns.Arn); s != "" {
		typed.ARN = generated.LiteralOf(s)
	}
	if s := aws.ToString(ns.Description); s != "" {
		typed.Description = generated.LiteralOf(s)
	}
	if ns.Properties != nil && ns.Properties.DnsProperties != nil {
		if s := aws.ToString(ns.Properties.DnsProperties.HostedZoneId); s != "" {
			typed.HostedZone = generated.LiteralOf(s)
		}
	}
	if s := aws.ToString(ns.Id); s != "" {
		typed.ID = generated.LiteralOf(s)
	}
	if s := aws.ToString(ns.Name); s != "" {
		typed.Name = generated.LiteralOf(s)
	}
	if vpcID != "" {
		typed.VPC = generated.LiteralOf(vpcID)
	}
	return typed
}

// Compile-time assertions: must satisfy both AttributeEnricher and
// ByIDEnricher (Phase 2 contract).
var (
	_ AttributeEnricher = (*serviceDiscoveryPrivateDNSNamespaceEnricher)(nil)
	_ ByIDEnricher      = (*serviceDiscoveryPrivateDNSNamespaceEnricher)(nil)
)
