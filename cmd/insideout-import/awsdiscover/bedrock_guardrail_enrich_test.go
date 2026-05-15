package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// newTestBedrockGuardrailEnricher builds a bedrockGuardrailEnricher with
// a fake GetGuardrail fetch wired in. Mirrors the function-field
// injection pattern used by the other AWS-side enricher tests.
func newTestBedrockGuardrailEnricher(
	get func(ctx context.Context, c *bedrock.Client, guardrailID, version string) (*bedrock.GetGuardrailOutput, error),
) *bedrockGuardrailEnricher {
	return &bedrockGuardrailEnricher{fetch: get}
}

// decodeBedrockGuardrailAttrs round-trips ir.Attrs through UnmarshalAttrs
// and returns the typed AWSBedrockGuardrail.
func decodeBedrockGuardrailAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSBedrockGuardrail {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_bedrock_guardrail", ir.Attrs)
	require.NoError(t, err)
	g, ok := decoded.(*generated.AWSBedrockGuardrail)
	require.True(t, ok, "decoded type must be *AWSBedrockGuardrail, got %T", decoded)
	return g
}

// decodeBedrockGuardrailRaw is the EnrichByID counterpart.
func decodeBedrockGuardrailRaw(t *testing.T, raw json.RawMessage) *generated.AWSBedrockGuardrail {
	t.Helper()
	require.NotEmpty(t, raw, "EnrichByID must return a non-empty payload")
	decoded, err := generated.UnmarshalAttrs("aws_bedrock_guardrail", raw)
	require.NoError(t, err)
	g, ok := decoded.(*generated.AWSBedrockGuardrail)
	require.True(t, ok, "decoded type must be *AWSBedrockGuardrail, got %T", decoded)
	return g
}

func TestBedrockGuardrailEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newBedrockGuardrailEnricher()
	assert.Equal(t, "aws_bedrock_guardrail", enr.ResourceType())
}

func TestBedrockGuardrailEnricher_ImplementsByIDEnricher(t *testing.T) {
	t.Parallel()
	// Compile-time guarantee that the production constructor returns
	// something satisfying both interfaces. Phase 2 contract.
	var _ AttributeEnricher = newBedrockGuardrailEnricher()
	enr := newBedrockGuardrailEnricher()
	var _ ByIDEnricher = enr
}

func TestBedrockGuardrailEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := bedrockGuardrailEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "aws_bedrock_guardrail",
			ImportID:  "abc123,DRAFT",
			NativeIDs: map[string]string{"guardrail_id": "abc123", "version": "DRAFT"},
		},
	}, EnrichClients{Bedrock: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestBedrockGuardrailEnricher_EnrichByID_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := bedrockGuardrailEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_bedrock_guardrail",
		ImportID: "abc123,DRAFT",
	}, EnrichClients{Bedrock: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestBedrockGuardrailEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := newBedrockGuardrailEnricher()
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{Bedrock: &bedrock.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestBedrockGuardrailEnricher_IDDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label   string
		id      imported.ResourceIdentity
		wantID  string
		wantVer string
	}{
		{
			label: "NativeIDs win",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{"guardrail_id": "abc123", "version": "v1"},
				ImportID:  "ignored,DRAFT",
			},
			wantID: "abc123", wantVer: "v1",
		},
		{
			label:  "ImportID comma form parsed when NativeIDs empty",
			id:     imported.ResourceIdentity{ImportID: "abc123,v2"},
			wantID: "abc123", wantVer: "v2",
		},
		{
			label:  "Bare ImportID defaults version to DRAFT",
			id:     imported.ResourceIdentity{ImportID: "abc123"},
			wantID: "abc123", wantVer: "DRAFT",
		},
		{
			label:   "empty everywhere → empty id, empty version",
			id:      imported.ResourceIdentity{},
			wantID:  "",
			wantVer: "",
		},
		{
			label: "NativeIDs id only — version still DRAFT",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{"guardrail_id": "abc123"},
			},
			wantID: "abc123", wantVer: "DRAFT",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			gotID, gotVer := bedrockGuardrailIDForEnrich(&tc.id)
			assert.Equal(t, tc.wantID, gotID)
			assert.Equal(t, tc.wantVer, gotVer)
		})
	}
	// nil-pointer guard.
	gotID, gotVer := bedrockGuardrailIDForEnrich(nil)
	assert.Equal(t, "", gotID)
	assert.Equal(t, "", gotVer)
}

func TestBedrockGuardrailEnricher_NotFoundMapsToErrNotFound(t *testing.T) {
	t.Parallel()
	enr := newTestBedrockGuardrailEnricher(
		func(context.Context, *bedrock.Client, string, string) (*bedrock.GetGuardrailOutput, error) {
			return nil, &bedrocktypes.ResourceNotFoundException{Message: aws.String("not found")}
		},
	)
	t.Run("Enrich", func(t *testing.T) {
		t.Parallel()
		err := enr.Enrich(context.Background(), &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:      "aws_bedrock_guardrail",
				NativeIDs: map[string]string{"guardrail_id": "missing"},
			},
		}, EnrichClients{Bedrock: &bedrock.Client{}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("EnrichByID", func(t *testing.T) {
		t.Parallel()
		_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
			Type:      "aws_bedrock_guardrail",
			NativeIDs: map[string]string{"guardrail_id": "missing"},
		}, EnrichClients{Bedrock: &bedrock.Client{}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestBedrockGuardrailEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDenied")
	enr := newTestBedrockGuardrailEnricher(
		func(context.Context, *bedrock.Client, string, string) (*bedrock.GetGuardrailOutput, error) {
			return nil, wantErr
		},
	)
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "aws_bedrock_guardrail",
			NativeIDs: map[string]string{"guardrail_id": "abc"},
		},
	}, EnrichClients{Bedrock: &bedrock.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestBedrockGuardrailEnricher_NoIDReturnsError(t *testing.T) {
	t.Parallel()
	// Empty Identity (no NativeIDs, no ImportID) → derivation returns
	// empty id and the enricher refuses to issue the SDK call.
	enr := newBedrockGuardrailEnricher()
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_bedrock_guardrail"},
	}, EnrichClients{Bedrock: &bedrock.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive guardrail id")
}

func TestBedrockGuardrailEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	const (
		guardrailID  = "abc123"
		guardrailARN = "arn:aws:bedrock:us-east-1:012345678901:guardrail/abc123"
		kmsKeyARN    = "arn:aws:kms:us-east-1:012345678901:key/zzz"
	)
	threshold := 0.75
	out := &bedrock.GetGuardrailOutput{
		GuardrailId:             aws.String(guardrailID),
		GuardrailArn:            aws.String(guardrailARN),
		Name:                    aws.String("my-guardrail"),
		Description:             aws.String("Test guardrail"),
		Version:                 aws.String("DRAFT"),
		Status:                  bedrocktypes.GuardrailStatusReady,
		KmsKeyArn:               aws.String(kmsKeyARN),
		BlockedInputMessaging:   aws.String("Sorry, I can't help with that."),
		BlockedOutputsMessaging: aws.String("Sorry, I had to filter that response."),
		ContentPolicy: &bedrocktypes.GuardrailContentPolicy{
			Filters: []bedrocktypes.GuardrailContentFilter{
				{
					InputStrength:  bedrocktypes.GuardrailFilterStrengthHigh,
					OutputStrength: bedrocktypes.GuardrailFilterStrengthMedium,
					Type:           bedrocktypes.GuardrailContentFilterTypeHate,
				},
			},
		},
		ContextualGroundingPolicy: &bedrocktypes.GuardrailContextualGroundingPolicy{
			Filters: []bedrocktypes.GuardrailContextualGroundingFilter{
				{
					Threshold: &threshold,
					Type:      bedrocktypes.GuardrailContextualGroundingFilterTypeGrounding,
				},
			},
		},
		TopicPolicy: &bedrocktypes.GuardrailTopicPolicy{
			Topics: []bedrocktypes.GuardrailTopic{
				{
					Name:       aws.String("finance"),
					Definition: aws.String("Don't give financial advice"),
					Type:       bedrocktypes.GuardrailTopicTypeDeny,
					Examples:   []string{"Should I buy stock X?", "What's a good ETF?"},
				},
			},
		},
		WordPolicy: &bedrocktypes.GuardrailWordPolicy{
			Words: []bedrocktypes.GuardrailWord{
				{Text: aws.String("forbidden")},
			},
			ManagedWordLists: []bedrocktypes.GuardrailManagedWords{
				{Type: bedrocktypes.GuardrailManagedWordsTypeProfanity},
			},
		},
	}
	enr := newTestBedrockGuardrailEnricher(
		func(_ context.Context, _ *bedrock.Client, gotID, gotVer string) (*bedrock.GetGuardrailOutput, error) {
			assert.Equal(t, guardrailID, gotID)
			assert.Equal(t, "DRAFT", gotVer)
			return out, nil
		},
	)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "aws_bedrock_guardrail",
			ImportID:  guardrailID + ",DRAFT",
			NameHint:  "my-guardrail",
			NativeIDs: map[string]string{"guardrail_id": guardrailID, "version": "DRAFT"},
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{Bedrock: &bedrock.Client{}}))

	// ARN promoted onto NativeIDs.
	assert.Equal(t, guardrailARN, ir.Identity.NativeIDs["arn"],
		"enricher must stamp ARN onto Identity.NativeIDs[arn]")

	g := decodeBedrockGuardrailAttrs(t, ir)
	require.NotNil(t, g.Name)
	assert.Equal(t, "my-guardrail", *g.Name.Literal)
	require.NotNil(t, g.GuardrailID)
	assert.Equal(t, guardrailID, *g.GuardrailID.Literal)
	require.NotNil(t, g.GuardrailARN)
	assert.Equal(t, guardrailARN, *g.GuardrailARN.Literal)
	require.NotNil(t, g.KMSKeyARN)
	assert.Equal(t, kmsKeyARN, *g.KMSKeyARN.Literal)
	require.NotNil(t, g.Version)
	assert.Equal(t, "DRAFT", *g.Version.Literal)
	require.NotNil(t, g.Status)
	assert.Equal(t, "READY", *g.Status.Literal)
	require.NotNil(t, g.BlockedInputMessaging)
	assert.Equal(t, "Sorry, I can't help with that.", *g.BlockedInputMessaging.Literal)
	require.NotNil(t, g.BlockedOutputsMessaging)
	assert.Equal(t, "Sorry, I had to filter that response.", *g.BlockedOutputsMessaging.Literal)

	// Content policy.
	require.Len(t, g.ContentPolicyConfig, 1)
	require.Len(t, g.ContentPolicyConfig[0].FiltersConfig, 1)
	cf := g.ContentPolicyConfig[0].FiltersConfig[0]
	require.NotNil(t, cf.InputStrength)
	assert.Equal(t, "HIGH", *cf.InputStrength.Literal)
	require.NotNil(t, cf.OutputStrength)
	assert.Equal(t, "MEDIUM", *cf.OutputStrength.Literal)
	require.NotNil(t, cf.Type_)
	assert.Equal(t, "HATE", *cf.Type_.Literal)

	// Contextual grounding policy.
	require.Len(t, g.ContextualGroundingPolicyConfig, 1)
	require.Len(t, g.ContextualGroundingPolicyConfig[0].FiltersConfig, 1)
	cg := g.ContextualGroundingPolicyConfig[0].FiltersConfig[0]
	require.NotNil(t, cg.Threshold)
	assert.Equal(t, 0.75, *cg.Threshold.Literal)
	require.NotNil(t, cg.Type_)
	assert.Equal(t, "GROUNDING", *cg.Type_.Literal)

	// Topic policy.
	require.Len(t, g.TopicPolicyConfig, 1)
	require.Len(t, g.TopicPolicyConfig[0].TopicsConfig, 1)
	tp := g.TopicPolicyConfig[0].TopicsConfig[0]
	require.NotNil(t, tp.Name)
	assert.Equal(t, "finance", *tp.Name.Literal)
	require.NotNil(t, tp.Definition)
	assert.Equal(t, "Don't give financial advice", *tp.Definition.Literal)
	require.NotNil(t, tp.Type_)
	assert.Equal(t, "DENY", *tp.Type_.Literal)
	require.Len(t, tp.Examples, 2)
	require.NotNil(t, tp.Examples[0])
	assert.Equal(t, "Should I buy stock X?", *tp.Examples[0].Literal)
	require.NotNil(t, tp.Examples[1])
	assert.Equal(t, "What's a good ETF?", *tp.Examples[1].Literal)

	// Word policy.
	require.Len(t, g.WordPolicyConfig, 1)
	require.Len(t, g.WordPolicyConfig[0].WordsConfig, 1)
	require.NotNil(t, g.WordPolicyConfig[0].WordsConfig[0].Text)
	assert.Equal(t, "forbidden", *g.WordPolicyConfig[0].WordsConfig[0].Text.Literal)
	require.Len(t, g.WordPolicyConfig[0].ManagedWordListsConfig, 1)
	require.NotNil(t, g.WordPolicyConfig[0].ManagedWordListsConfig[0].Type_)
	assert.Equal(t, "PROFANITY", *g.WordPolicyConfig[0].ManagedWordListsConfig[0].Type_.Literal)

	// Computed-only fields not curated by enricher today: Tags (overlay
	// is the discoverer's; no Tags emitted by mapBedrockGuardrail), TagsAll.
	assert.Empty(t, g.TagsAll, "tags_all is Computed mirror; enricher must not emit it")
}

func TestBedrockGuardrailEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	const (
		guardrailID  = "abc123"
		guardrailARN = "arn:aws:bedrock:us-east-1:012345678901:guardrail/abc123"
	)
	out := &bedrock.GetGuardrailOutput{
		GuardrailId:  aws.String(guardrailID),
		GuardrailArn: aws.String(guardrailARN),
		Name:         aws.String("my-guardrail"),
		Description:  aws.String("Test guardrail"),
		Version:      aws.String("DRAFT"),
	}
	enr := newTestBedrockGuardrailEnricher(
		func(context.Context, *bedrock.Client, string, string) (*bedrock.GetGuardrailOutput, error) {
			return out, nil
		},
	)

	// Enrich path.
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "aws_bedrock_guardrail",
			NativeIDs: map[string]string{"guardrail_id": guardrailID, "version": "DRAFT"},
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{Bedrock: &bedrock.Client{}}))
	gFromEnrich := decodeBedrockGuardrailAttrs(t, ir)

	// EnrichByID path.
	identity := &imported.ResourceIdentity{
		Type:      "aws_bedrock_guardrail",
		NativeIDs: map[string]string{"guardrail_id": guardrailID, "version": "DRAFT"},
	}
	raw, err := enr.EnrichByID(context.Background(), identity, EnrichClients{Bedrock: &bedrock.Client{}})
	require.NoError(t, err)
	gFromByID := decodeBedrockGuardrailRaw(t, raw)

	// Both decode to the same payload.
	assert.Equal(t, gFromEnrich, gFromByID,
		"Enrich and EnrichByID must produce identical typed payloads")

	// EnrichByID must NOT mutate the caller's identity.
	assert.Equal(t, map[string]string{"guardrail_id": guardrailID, "version": "DRAFT"}, identity.NativeIDs,
		"EnrichByID must not stamp NativeIDs onto the caller's identity")
}
