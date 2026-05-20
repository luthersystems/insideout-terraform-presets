package awsdiscover

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestTFTagKind pins the `tf:` struct-tag parser. The block-vs-attr
// classification is what drives wrapStructBlocks, so a misparse would
// silently disable the whole generic fix.
func TestTFTagKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tag      string
		wantName string
		wantKind tfKind
	}{
		{"", "", tfAttr},
		{"-", "", tfAttr},
		{"function_name", "function_name", tfAttr},
		{"timeouts,block", "timeouts", tfBlock},
		{"environment,blocks", "environment", tfBlocks},
		{"dns_options,blocks", "dns_options", tfBlocks},
		// Unknown suffix is treated as a plain attribute, not a block.
		{"weird,unknown", "weird", tfAttr},
	}
	for _, tc := range cases {
		t.Run(tc.tag, func(t *testing.T) {
			t.Parallel()
			name, kind := tfTagKind(tc.tag)
			assert.Equal(t, tc.wantName, name)
			assert.Equal(t, tc.wantKind, kind)
		})
	}
}

// TestBlockElemType resolves the nested struct type behind a block
// field for both the slice-backed (`,blocks`) and pointer-backed
// (`,block`) shapes. The helper is only ever invoked by wrapStructBlocks
// on a field already classified as a block, so the cases here mirror
// exactly those two shapes.
func TestBlockElemType(t *testing.T) {
	t.Parallel()
	fnType := reflect.TypeFor[generated.AWSLambdaFunction]()

	blocksField, ok := fnType.FieldByName("Environment") // []AWSLambdaFunctionEnvironment
	require.True(t, ok)
	got := blockElemType(blocksField.Type)
	require.NotNil(t, got)
	assert.Equal(t, "AWSLambdaFunctionEnvironment", got.Name())

	blockField, ok := fnType.FieldByName("Timeouts") // *AWSLambdaFunctionTimeouts
	require.True(t, ok)
	got = blockElemType(blockField.Type)
	require.NotNil(t, got)
	assert.Equal(t, "AWSLambdaFunctionTimeouts", got.Name())
}

// TestWrapObjectBlocksForType_FailOpen pins the two fail-open paths: an
// unregistered type and a nil map both pass through without panicking.
func TestWrapObjectBlocksForType_FailOpen(t *testing.T) {
	t.Parallel()

	in := map[string]any{"dns_options": map[string]any{"x": 1}}
	out := wrapObjectBlocksForType("aws_not_a_real_type", in)
	assert.Equal(t, in, out, "unregistered type must pass through unchanged")
	_, stillObject := out["dns_options"].(map[string]any)
	assert.True(t, stillObject, "unregistered type must not wrap anything")

	assert.Nil(t, wrapObjectBlocksForType("aws_vpc_endpoint", nil),
		"nil map must pass through as nil")
}

// TestWrapObjectBlocksForType_WrapsSingletonObject is the core unit: a
// CFN object landing on a `,blocks` slice field is wrapped into a
// one-element list so the downstream typed unmarshal stops hard-failing.
func TestWrapObjectBlocksForType_WrapsSingletonObject(t *testing.T) {
	t.Parallel()
	shaped := map[string]any{
		"dns_options": map[string]any{
			"dns_record_ip_type": map[string]any{"literal": "ipv4"},
		},
	}
	wrapObjectBlocksForType("aws_vpc_endpoint", shaped)

	list, ok := shaped["dns_options"].([]any)
	require.Truef(t, ok, "dns_options must become a list, got %T", shaped["dns_options"])
	require.Len(t, list, 1)
	elem, ok := list[0].(map[string]any)
	require.True(t, ok, "the wrapped element must be the original object")
	assert.Contains(t, elem, "dns_record_ip_type", "the original object's contents must be preserved, not replaced with an empty map")
}

// TestWrapObjectBlocksForType_Idempotent pins that a value already in
// list shape (CFN plural property, or a re-run after a per-type
// wrapObjectAsList Normalizer) is left untouched — no double-wrapping.
func TestWrapObjectBlocksForType_Idempotent(t *testing.T) {
	t.Parallel()
	shaped := map[string]any{
		"dns_options": []any{
			map[string]any{"dns_record_ip_type": map[string]any{"literal": "ipv4"}},
		},
	}
	wrapObjectBlocksForType("aws_vpc_endpoint", shaped)

	list, ok := shaped["dns_options"].([]any)
	require.True(t, ok, "an already-list block field must stay a list")
	require.Len(t, list, 1, "an already-list block field must not be re-wrapped")
	elem, ok := list[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, elem, "dns_record_ip_type", "the existing element's contents must survive untouched")
}

// TestWrapObjectBlocksForType_ScalarLeftAlone pins the fail-open posture
// for a genuinely wrong shape: a scalar on a block field is not wrapped,
// so the downstream unmarshal surfaces the real shape error instead of
// this pass masking it.
func TestWrapObjectBlocksForType_ScalarLeftAlone(t *testing.T) {
	t.Parallel()
	shaped := map[string]any{"dns_options": "not-an-object"}
	wrapObjectBlocksForType("aws_vpc_endpoint", shaped)
	assert.Equal(t, "not-an-object", shaped["dns_options"])
}

// TestWrapObjectBlocksForType_NestedBlocks pins the recursion: a CFN
// object nested inside another CFN object — both landing on `,blocks`
// fields — is wrapped at every level. aws_eks_cluster's
// KubernetesNetworkConfig (block) contains ElasticLoadBalancing (block).
func TestWrapObjectBlocksForType_NestedBlocks(t *testing.T) {
	t.Parallel()
	shaped := map[string]any{
		"kubernetes_network_config": map[string]any{
			"ip_family": map[string]any{"literal": "ipv4"},
			"elastic_load_balancing": map[string]any{
				"enabled": map[string]any{"literal": true},
			},
		},
	}
	wrapObjectBlocksForType("aws_eks_cluster", shaped)

	outer, ok := shaped["kubernetes_network_config"].([]any)
	require.Truef(t, ok, "outer block must wrap to a list, got %T", shaped["kubernetes_network_config"])
	require.Len(t, outer, 1)
	elem, ok := outer[0].(map[string]any)
	require.True(t, ok)
	inner, ok := elem["elastic_load_balancing"].([]any)
	require.Truef(t, ok, "nested block must also wrap to a list, got %T", elem["elastic_load_balancing"])
	require.Len(t, inner, 1)
}

// TestWrapObjectBlocksForType_SingleBlockNotWrapped pins that a `,block`
// (single, pointer-backed) field is recursed into but NOT wrapped —
// encoding/json accepts an object on a *struct field, so wrapping it
// would be wrong. aws_lambda_function.Timeouts is the `,block` case.
func TestWrapObjectBlocksForType_SingleBlockNotWrapped(t *testing.T) {
	t.Parallel()
	shaped := map[string]any{
		"timeouts": map[string]any{"create": map[string]any{"literal": "10m"}},
	}
	wrapObjectBlocksForType("aws_lambda_function", shaped)
	_, stillObject := shaped["timeouts"].(map[string]any)
	assert.True(t, stillObject, "a single `,block` field must stay an object, not be wrapped into a list")
}

// TestGeneratedUnmarshalAttrs_ObjectOnBlockSlice_Crashes is the
// load-bearing reproduction: it proves the bug class still exists at the
// generated.UnmarshalAttrs boundary and that wrapping is what fixes it.
// An object on a `,blocks` slice field hard-fails encoding/json and
// aborts the WHOLE unmarshal; the one-element-list shape decodes clean.
func TestGeneratedUnmarshalAttrs_ObjectOnBlockSlice_Crashes(t *testing.T) {
	t.Parallel()

	// Object on the dns_options slice field — the crash shape.
	_, err := generated.UnmarshalAttrs("aws_vpc_endpoint",
		json.RawMessage(`{"dns_options":{}}`))
	require.Error(t, err, "object on a `,blocks` slice field must fail the unmarshal")

	// One-element list — the shape wrapObjectBlocksForType produces.
	_, err = generated.UnmarshalAttrs("aws_vpc_endpoint",
		json.RawMessage(`{"dns_options":[{}]}`))
	require.NoError(t, err, "one-element-list block shape must decode cleanly")
}

// newCCEnricher builds the Cloud Control enricher for tfType wired
// exactly as NewAWSDiscoverer wires it: the registered per-type
// Normalizer chained with the generic stripComputedOnlyForType filter.
// Keeps the end-to-end tests honest against the production path.
func newCCEnricher(t *testing.T, tfType string, get cloudControlGetResourceFn) *cloudControlEnricher {
	t.Helper()
	for _, c := range cloudControlTypeConfigs {
		if c.TFType == tfType {
			norm := chain(c.Normalizer, stripComputedOnlyForType(c.TFType))
			return newCloudControlEnricherWithNormalizer(c.TFType, c.CloudFormationType, get, norm)
		}
	}
	t.Fatalf("no cloudControlTypeConfigs entry for %s", tfType)
	return nil
}

// TestCloudControlEnricher_Enrich_VPCEndpoint_GenericBlockWrap is the
// end-to-end regression for a type with NO per-type Normalizer: the
// generic wrapObjectBlocksForType pass is the sole fix. Before it, the
// CFN object-shaped DnsOptions aborted UnmarshalAttrs and the imported
// aws_vpc_endpoint came back with Attrs=nil — the same reliable #1620
// failure mode PR #641 fixed only for aws_lambda_function.
func TestCloudControlEnricher_Enrich_VPCEndpoint_GenericBlockWrap(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{
		"Id": "vpce-0abc123",
		"VpcId": "vpc-0aaa",
		"ServiceName": "com.amazonaws.us-east-1.s3",
		"VpcEndpointType": "Interface",
		"DnsOptions": {"DnsRecordIpType": "ipv4", "PrivateDnsOnlyForInboundResolverEndpoint": false}
	}`}
	enr := newCCEnricher(t, "aws_vpc_endpoint", fake.call)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_vpc_endpoint",
			ImportID: "vpce-0abc123",
			Address:  "aws_vpc_endpoint.imported",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	require.NotEmpty(t, ir.Attrs, "Attrs must not be empty — the object-on-block-slice bug drops everything")

	decoded, err := generated.UnmarshalAttrs("aws_vpc_endpoint", ir.Attrs)
	require.NoError(t, err)
	ep, ok := decoded.(*generated.AWSVPCEndpoint)
	require.Truef(t, ok, "decoded type is %T, want *generated.AWSVPCEndpoint", decoded)

	require.NotNil(t, ep.ServiceName)
	require.NotNil(t, ep.ServiceName.Literal)
	assert.Equal(t, "com.amazonaws.us-east-1.s3", *ep.ServiceName.Literal)

	require.Lenf(t, ep.DNSOptions, 1, "DnsOptions object must wrap into a one-element block slice")
	require.NotNil(t, ep.DNSOptions[0].DNSRecordIpType)
	require.NotNil(t, ep.DNSOptions[0].DNSRecordIpType.Literal)
	assert.Equal(t, "ipv4", *ep.DNSOptions[0].DNSRecordIpType.Literal)
}

// TestCloudControlEnricher_Enrich_EKSCluster_NestedBlockWrap exercises
// the recursive case end-to-end: aws_eks_cluster has no per-type
// Normalizer, and its KubernetesNetworkConfig CFN object contains a
// further ElasticLoadBalancing CFN object — both must wrap for the
// typed unmarshal to succeed.
func TestCloudControlEnricher_Enrich_EKSCluster_NestedBlockWrap(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{
		"Name": "io-prod-eks",
		"RoleArn": "arn:aws:iam::031780745048:role/io-prod-eks",
		"Version": "1.30",
		"AccessConfig": {"AuthenticationMode": "API"},
		"KubernetesNetworkConfig": {"IpFamily": "ipv4", "ElasticLoadBalancing": {"Enabled": true}}
	}`}
	enr := newCCEnricher(t, "aws_eks_cluster", fake.call)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_eks_cluster",
			ImportID: "io-prod-eks",
			Address:  "aws_eks_cluster.imported",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	require.NotEmpty(t, ir.Attrs)

	decoded, err := generated.UnmarshalAttrs("aws_eks_cluster", ir.Attrs)
	require.NoError(t, err)
	cl, ok := decoded.(*generated.AWSEKSCluster)
	require.Truef(t, ok, "decoded type is %T, want *generated.AWSEKSCluster", decoded)

	require.NotNil(t, cl.Name)
	require.NotNil(t, cl.Name.Literal)
	assert.Equal(t, "io-prod-eks", *cl.Name.Literal)

	require.Lenf(t, cl.AccessConfig, 1, "AccessConfig object must wrap into a block slice")
	require.NotNil(t, cl.AccessConfig[0].AuthenticationMode)
	require.NotNil(t, cl.AccessConfig[0].AuthenticationMode.Literal)
	assert.Equal(t, "API", *cl.AccessConfig[0].AuthenticationMode.Literal)

	require.Lenf(t, cl.KubernetesNetworkConfig, 1, "KubernetesNetworkConfig object must wrap into a block slice")
	knc := cl.KubernetesNetworkConfig[0]
	require.NotNil(t, knc.IpFamily)
	require.NotNil(t, knc.IpFamily.Literal)
	assert.Equal(t, "ipv4", *knc.IpFamily.Literal)
	require.Lenf(t, knc.ElasticLoadBalancing, 1, "nested ElasticLoadBalancing object must also wrap")
	require.NotNil(t, knc.ElasticLoadBalancing[0].Enabled)
	require.NotNil(t, knc.ElasticLoadBalancing[0].Enabled.Literal)
	assert.True(t, *knc.ElasticLoadBalancing[0].Enabled.Literal)
}

// --- CFN array-shaped Tags → struct map (#640 follow-up, tags variant) ---

// TestGeneratedUnmarshalAttrs_ArrayOnTagsMap_Crashes pins the tag-shape
// sibling of the object-on-block-slice bug: CFN serializes Tags as a
// list-of-{Key,Value}, but the generated struct types `tags` as a map.
// A list on a map field hard-fails encoding/json and aborts the WHOLE
// unmarshal; the flat-map shape flattenTagListsForType produces decodes
// clean.
func TestGeneratedUnmarshalAttrs_ArrayOnTagsMap_Crashes(t *testing.T) {
	t.Parallel()
	_, err := generated.UnmarshalAttrs("aws_lambda_function",
		json.RawMessage(`{"tags":[{"Key":"Project","Value":"io"}]}`))
	require.Error(t, err, "list on a map-typed `tags` field must fail the unmarshal")

	_, err = generated.UnmarshalAttrs("aws_lambda_function",
		json.RawMessage(`{"tags":{"Project":{"literal":"io"}}}`))
	require.NoError(t, err, "flat-map `tags` shape must decode cleanly")
}

// TestFlattenTagListsForType_FlattensArrayTags is the core unit for the
// generic tag-flatten: a CFN list-of-{Key,Value} Tags property is
// collapsed into the verbatim-wrapped flat map the struct's `tags` field
// expects, for a type (aws_security_group) with no per-type
// flattenTagList Normalizer.
func TestFlattenTagListsForType_FlattensArrayTags(t *testing.T) {
	t.Parallel()
	out, err := flattenTagListsForType("aws_security_group",
		json.RawMessage(`{"GroupName":"sg-x","Tags":[{"Key":"Project","Value":"io"},{"Key":"Env","Value":"prod"}]}`))
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))
	tagsWrap, ok := m["Tags"].(map[string]any)
	require.Truef(t, ok, "Tags must become an object, got %T", m["Tags"])
	inner, ok := tagsWrap[verbatimMarkerKey].(map[string]any)
	require.True(t, ok, "flattened tags must be wrapped under the verbatim marker so keys survive camelToSnake")
	assert.Equal(t, "io", inner["Project"])
	assert.Equal(t, "prod", inner["Env"])
}

// TestFlattenTagListsForType_Idempotent pins that an already-flattened
// (map-shaped) tag set, an absent Tags key, and an unregistered type all
// pass through without error or double-processing.
func TestFlattenTagListsForType_Idempotent(t *testing.T) {
	t.Parallel()

	mapShaped := json.RawMessage(`{"Tags":{"Project":"io"}}`)
	out, err := flattenTagListsForType("aws_security_group", mapShaped)
	require.NoError(t, err)
	// flattenTagListsForType only rewrites a *list*-shaped tag set (the
	// crash case). An already-map Tags value is not a list, so it passes
	// through completely untouched — content intact, no double-wrap.
	assert.JSONEq(t, `{"Tags":{"Project":"io"}}`, string(out),
		"an already-map Tags value must pass through unchanged")

	out, err = flattenTagListsForType("aws_security_group", json.RawMessage(`{"GroupName":"sg-x"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"GroupName":"sg-x"}`, string(out), "absent Tags must pass through unchanged")

	out, err = flattenTagListsForType("aws_not_a_real_type", json.RawMessage(`{"Tags":[{"Key":"k","Value":"v"}]}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"Tags":[{"Key":"k","Value":"v"}]}`, string(out),
		"unregistered type must pass through unchanged")
}

// TestCloudControlEnricher_Enrich_ArrayTags is the end-to-end regression
// for the tags variant: aws_lambda_function's CFN payload carries
// array-shaped Tags, and the per-type Normalizer chain does NOT flatten
// them — the generic flattenTagListsForType pass is the sole fix. Before
// it, the array aborted UnmarshalAttrs and the imported function came
// back with Attrs=nil.
func TestCloudControlEnricher_Enrich_ArrayTags(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{
		"FunctionName": "io-prod-lambda",
		"Role": "arn:aws:iam::031780745048:role/io-prod-lambda-exec",
		"Runtime": "python3.12",
		"Handler": "index.handler",
		"MemorySize": 256,
		"Timeout": 30,
		"Tags": [{"Key": "Project", "Value": "io-prod"}, {"Key": "ManagedBy", "Value": "insideout"}]
	}`}
	enr := newLambdaEnricher(t, fake.call)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_lambda_function",
			ImportID: "io-prod-lambda",
			Address:  "aws_lambda_function.io_prod_lambda",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	require.NotEmpty(t, ir.Attrs, "array-shaped Tags must not abort the unmarshal and drop every attribute")

	fn := decodeLambda(t, ir.Attrs)
	require.NotNil(t, fn.FunctionName)
	require.NotNil(t, fn.FunctionName.Literal)
	assert.Equal(t, "io-prod-lambda", *fn.FunctionName.Literal)

	// Tag keys are operator data — must survive verbatim, not snake-cased.
	require.NotNil(t, fn.Tags["Project"])
	require.NotNil(t, fn.Tags["Project"].Literal)
	assert.Equal(t, "io-prod", *fn.Tags["Project"].Literal)
	require.NotNil(t, fn.Tags["ManagedBy"])
	require.NotNil(t, fn.Tags["ManagedBy"].Literal)
	assert.Equal(t, "insideout", *fn.Tags["ManagedBy"].Literal)
}

// --- Cloud Control region threading (#640 follow-up) ---

// TestCloudControlEnricher_Enrich_ThreadsResourceRegion pins that
// fetchAndMap pins GetResource to the resource's own Identity.Region.
// Cloud Control is a regional API; the enricher's default client is
// pinned to the discoverer's primary region, so without the per-call
// override a cross-region resource is queried in the wrong region and
// comes back ResourceNotFound — dropping its Attrs.
func TestCloudControlEnricher_Enrich_ThreadsResourceRegion(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{"Id":"vpce-0abc123","ServiceName":"com.amazonaws.us-east-1.s3"}`}
	enr := newCCEnricher(t, "aws_vpc_endpoint", fake.call)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_vpc_endpoint",
			ImportID: "vpce-0abc123",
			Address:  "aws_vpc_endpoint.imported",
			Region:   "us-east-1",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	assert.Equal(t, "us-east-1", fake.optsRegion(),
		"GetResource must be pinned to the resource's discovered region")
}

// TestCloudControlEnricher_Enrich_NoRegionNoOverride pins the
// complement: a resource with no Identity.Region passes no per-call
// override, leaving GetResource on the enricher's default-region client.
func TestCloudControlEnricher_Enrich_NoRegionNoOverride(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{"Id":"vpce-0abc123","ServiceName":"com.amazonaws.us-east-1.s3"}`}
	enr := newCCEnricher(t, "aws_vpc_endpoint", fake.call)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_vpc_endpoint",
			ImportID: "vpce-0abc123",
			Address:  "aws_vpc_endpoint.imported",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	assert.Empty(t, fake.gotOpts,
		"no Identity.Region must append no per-call override at all")
	assert.Empty(t, fake.optsRegion(),
		"no Identity.Region must leave GetResource on the default-region client")
}

// --- CFN string-encoded scalar coercion (#640 follow-up) ---

// TestCloudControlEnricher_Enrich_VPCEndpoint_StringEncodedBool is the
// end-to-end regression for the scalar-coercion variant: CloudFormation
// returns aws_vpc_endpoint's PrivateDnsEnabled / RequesterManaged as the
// JSON string "true", which the generated Value[bool] field rejected —
// aborting UnmarshalAttrs and dropping every aws_vpc_endpoint's Attrs
// (live-confirmed: 4/4 nil). The tolerant Value literal decode coerces
// the stringified scalar.
func TestCloudControlEnricher_Enrich_VPCEndpoint_StringEncodedBool(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{
		"Id": "vpce-0abc123",
		"VpcId": "vpc-0aaa",
		"ServiceName": "com.amazonaws.us-east-1.s3",
		"VpcEndpointType": "Interface",
		"PrivateDnsEnabled": "true",
		"RequesterManaged": "false"
	}`}
	enr := newCCEnricher(t, "aws_vpc_endpoint", fake.call)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_vpc_endpoint",
			ImportID: "vpce-0abc123",
			Address:  "aws_vpc_endpoint.imported",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	require.NotEmpty(t, ir.Attrs, "a string-encoded bool must not abort the unmarshal and drop every attribute")

	decoded, err := generated.UnmarshalAttrs("aws_vpc_endpoint", ir.Attrs)
	require.NoError(t, err)
	ep, ok := decoded.(*generated.AWSVPCEndpoint)
	require.Truef(t, ok, "decoded type is %T, want *generated.AWSVPCEndpoint", decoded)

	require.NotNil(t, ep.PrivateDNSEnabled)
	require.NotNil(t, ep.PrivateDNSEnabled.Literal)
	assert.True(t, *ep.PrivateDNSEnabled.Literal, `CFN string "true" must coerce to bool true`)

	// requester_managed is Computed-only on the schema, so
	// stripComputedOnlyForType elides it (decision #5) before the
	// coercion path is even reached — asserting its absence pins that
	// the string-encoded value didn't sneak past the strip filter.
	assert.Nil(t, ep.RequesterManaged, "computed-only requester_managed must be stripped")
}
