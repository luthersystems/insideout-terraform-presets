package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdtypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// newTestServiceDiscoveryPrivateDNSNamespaceEnricher injects a fake
// GetNamespace hook.
func newTestServiceDiscoveryPrivateDNSNamespaceEnricher(
	get func(ctx context.Context, c *servicediscovery.Client, namespaceID string) (*servicediscovery.GetNamespaceOutput, error),
) *serviceDiscoveryPrivateDNSNamespaceEnricher {
	return &serviceDiscoveryPrivateDNSNamespaceEnricher{fetch: get}
}

func decodeSDPrivateDNSNSAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSServiceDiscoveryPrivateDNSNamespace {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_service_discovery_private_dns_namespace", ir.Attrs)
	require.NoError(t, err)
	g, ok := decoded.(*generated.AWSServiceDiscoveryPrivateDNSNamespace)
	require.True(t, ok, "decoded type must be *AWSServiceDiscoveryPrivateDNSNamespace, got %T", decoded)
	return g
}

func decodeSDPrivateDNSNSRaw(t *testing.T, raw json.RawMessage) *generated.AWSServiceDiscoveryPrivateDNSNamespace {
	t.Helper()
	require.NotEmpty(t, raw, "EnrichByID must return a non-empty payload")
	decoded, err := generated.UnmarshalAttrs("aws_service_discovery_private_dns_namespace", raw)
	require.NoError(t, err)
	g, ok := decoded.(*generated.AWSServiceDiscoveryPrivateDNSNamespace)
	require.True(t, ok, "decoded type must be *AWSServiceDiscoveryPrivateDNSNamespace, got %T", decoded)
	return g
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newServiceDiscoveryPrivateDNSNamespaceEnricher()
	assert.Equal(t, "aws_service_discovery_private_dns_namespace", enr.ResourceType())
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_ImplementsByIDEnricher(t *testing.T) {
	t.Parallel()
	var _ AttributeEnricher = newServiceDiscoveryPrivateDNSNamespaceEnricher()
	enr := newServiceDiscoveryPrivateDNSNamespaceEnricher()
	var _ ByIDEnricher = enr
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := serviceDiscoveryPrivateDNSNamespaceEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "aws_service_discovery_private_dns_namespace",
			ImportID:  "ns-abc:vpc-123",
			NativeIDs: map[string]string{"namespace_id": "ns-abc", "vpc_id": "vpc-123"},
		},
	}, EnrichClients{ServiceDiscovery: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_EnrichByID_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := serviceDiscoveryPrivateDNSNamespaceEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_service_discovery_private_dns_namespace",
		ImportID: "ns-abc:vpc-123",
	}, EnrichClients{ServiceDiscovery: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := newServiceDiscoveryPrivateDNSNamespaceEnricher()
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_IDDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		id    imported.ResourceIdentity
		want  string
	}{
		{
			label: "NativeIDs win",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{"namespace_id": "ns-abc"},
				ImportID:  "ignored:vpc-xx",
			},
			want: "ns-abc",
		},
		{
			label: "ImportID parsed as <ns>:<vpc>",
			id:    imported.ResourceIdentity{ImportID: "ns-abc:vpc-123"},
			want:  "ns-abc",
		},
		{
			label: "Bare ImportID returns full string",
			id:    imported.ResourceIdentity{ImportID: "ns-abc"},
			want:  "ns-abc",
		},
		{
			label: "empty everywhere → empty",
			id:    imported.ResourceIdentity{},
			want:  "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got := serviceDiscoveryPrivateDNSNamespaceIDForEnrich(&tc.id)
			assert.Equal(t, tc.want, got)
		})
	}
	// nil-pointer guard.
	assert.Equal(t, "", serviceDiscoveryPrivateDNSNamespaceIDForEnrich(nil))
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_NotFoundMapsToErrNotFound(t *testing.T) {
	t.Parallel()
	enr := newTestServiceDiscoveryPrivateDNSNamespaceEnricher(
		func(context.Context, *servicediscovery.Client, string) (*servicediscovery.GetNamespaceOutput, error) {
			return nil, &sdtypes.NamespaceNotFound{Message: aws.String("not found")}
		},
	)
	t.Run("Enrich", func(t *testing.T) {
		t.Parallel()
		err := enr.Enrich(context.Background(), &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:      "aws_service_discovery_private_dns_namespace",
				NativeIDs: map[string]string{"namespace_id": "missing"},
			},
		}, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("EnrichByID", func(t *testing.T) {
		t.Parallel()
		_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
			Type:      "aws_service_discovery_private_dns_namespace",
			NativeIDs: map[string]string{"namespace_id": "missing"},
		}, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDenied")
	enr := newTestServiceDiscoveryPrivateDNSNamespaceEnricher(
		func(context.Context, *servicediscovery.Client, string) (*servicediscovery.GetNamespaceOutput, error) {
			return nil, wantErr
		},
	)
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "aws_service_discovery_private_dns_namespace",
			NativeIDs: map[string]string{"namespace_id": "ns-abc"},
		},
	}, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_NoIDReturnsError(t *testing.T) {
	t.Parallel()
	enr := newServiceDiscoveryPrivateDNSNamespaceEnricher()
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_service_discovery_private_dns_namespace"},
	}, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive namespace id")
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	const (
		namespaceID = "ns-abcdef123"
		nsARN       = "arn:aws:servicediscovery:us-east-1:012345678901:namespace/ns-abcdef123"
		vpcID       = "vpc-0123456789abcdef0"
		hostedZone  = "Z1234567890ABC"
		nsName      = "my-service.internal"
	)
	out := &servicediscovery.GetNamespaceOutput{
		Namespace: &sdtypes.Namespace{
			Id:          aws.String(namespaceID),
			Arn:         aws.String(nsARN),
			Name:        aws.String(nsName),
			Type:        sdtypes.NamespaceTypeDnsPrivate,
			Description: aws.String("Service registry for internal traffic"),
			Properties: &sdtypes.NamespaceProperties{
				DnsProperties: &sdtypes.DnsProperties{
					HostedZoneId: aws.String(hostedZone),
				},
			},
		},
	}
	enr := newTestServiceDiscoveryPrivateDNSNamespaceEnricher(
		func(_ context.Context, _ *servicediscovery.Client, gotID string) (*servicediscovery.GetNamespaceOutput, error) {
			assert.Equal(t, namespaceID, gotID)
			return out, nil
		},
	)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_service_discovery_private_dns_namespace",
			ImportID: namespaceID + ":" + vpcID,
			NameHint: nsName,
			NativeIDs: map[string]string{
				"namespace_id":   namespaceID,
				"vpc_id":         vpcID,
				"hosted_zone_id": hostedZone,
			},
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}}))

	// ARN promoted onto NativeIDs.
	assert.Equal(t, nsARN, ir.Identity.NativeIDs["arn"],
		"enricher must stamp ARN onto Identity.NativeIDs[arn]")

	g := decodeSDPrivateDNSNSAttrs(t, ir)
	require.NotNil(t, g.ARN)
	assert.Equal(t, nsARN, *g.ARN.Literal)
	require.NotNil(t, g.ID)
	assert.Equal(t, namespaceID, *g.ID.Literal)
	require.NotNil(t, g.Name)
	assert.Equal(t, nsName, *g.Name.Literal)
	require.NotNil(t, g.Description)
	assert.Equal(t, "Service registry for internal traffic", *g.Description.Literal)
	require.NotNil(t, g.HostedZone)
	assert.Equal(t, hostedZone, *g.HostedZone.Literal)
	require.NotNil(t, g.VPC)
	assert.Equal(t, vpcID, *g.VPC.Literal)
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_VPCPlaceholderOmitted(t *testing.T) {
	t.Parallel()
	const namespaceID = "ns-abc"
	out := &servicediscovery.GetNamespaceOutput{
		Namespace: &sdtypes.Namespace{
			Id:   aws.String(namespaceID),
			Name: aws.String("svc.internal"),
		},
	}
	enr := newTestServiceDiscoveryPrivateDNSNamespaceEnricher(
		func(context.Context, *servicediscovery.Client, string) (*servicediscovery.GetNamespaceOutput, error) {
			return out, nil
		},
	)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "aws_service_discovery_private_dns_namespace",
			NativeIDs: map[string]string{
				"namespace_id": namespaceID,
				// vpc_id is the placeholder for "couldn't recover" — the
				// enricher must NOT emit the field on the typed payload.
				"vpc_id": vpcIDPlaceholderUnknown,
			},
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}}))
	g := decodeSDPrivateDNSNSAttrs(t, ir)
	assert.Nil(t, g.VPC, "vpc field must be omitted when NativeIDs[vpc_id] is the UNKNOWN placeholder")
}

func TestServiceDiscoveryPrivateDNSNamespaceEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	const (
		namespaceID = "ns-mirror-1"
		nsARN       = "arn:aws:servicediscovery:us-east-1:012345678901:namespace/ns-mirror-1"
		vpcID       = "vpc-abc"
	)
	out := &servicediscovery.GetNamespaceOutput{
		Namespace: &sdtypes.Namespace{
			Id:   aws.String(namespaceID),
			Arn:  aws.String(nsARN),
			Name: aws.String("svc.internal"),
			Type: sdtypes.NamespaceTypeDnsPrivate,
		},
	}
	enr := newTestServiceDiscoveryPrivateDNSNamespaceEnricher(
		func(context.Context, *servicediscovery.Client, string) (*servicediscovery.GetNamespaceOutput, error) {
			return out, nil
		},
	)

	// Enrich path.
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "aws_service_discovery_private_dns_namespace",
			NativeIDs: map[string]string{
				"namespace_id": namespaceID,
				"vpc_id":       vpcID,
			},
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}}))
	gFromEnrich := decodeSDPrivateDNSNSAttrs(t, ir)

	// EnrichByID path.
	identity := &imported.ResourceIdentity{
		Type: "aws_service_discovery_private_dns_namespace",
		NativeIDs: map[string]string{
			"namespace_id": namespaceID,
			"vpc_id":       vpcID,
		},
	}
	raw, err := enr.EnrichByID(context.Background(), identity, EnrichClients{ServiceDiscovery: &servicediscovery.Client{}})
	require.NoError(t, err)
	gFromByID := decodeSDPrivateDNSNSRaw(t, raw)

	assert.Equal(t, gFromEnrich, gFromByID,
		"Enrich and EnrichByID must produce identical typed payloads")

	// EnrichByID must NOT mutate the caller's identity (no ARN stamping).
	assert.NotContains(t, identity.NativeIDs, "arn",
		"EnrichByID must not stamp ARN onto the caller's identity")
}
