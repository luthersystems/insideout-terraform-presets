package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// bedrockGuardrailEnricher implements both AttributeEnricher and
// ByIDEnricher for aws_bedrock_guardrail (#482 Bucket-C push). Pairs
// with the hand-rolled bedrock_guardrail discoverer (Cloud Control does
// not list the type usefully — per-version fan-out semantics make the
// unified path unworkable for guardrails).
//
// SDK shape: GetGuardrail takes the bare guardrail id (and an optional
// version) and returns the full configuration: messaging strings,
// timestamps, KMS key, status, and the four nested policy families
// (content / topic / word / sensitive-information). Tags are inline on
// the discoverer-emitted Identity.Tags map (ListTagsForResource at
// discovery time), so the enricher's mapping skips them — overlaying
// them would double-fetch.
//
// Per decision #5, Computed-only TF fields are populated when they
// exist on the API response (`status`, `created_at`, `guardrail_arn`).
// The TF surface also has `tags_all` (Computed merged tag bag) which is
// downstream's concern; we never populate it.
//
// Sensitive fields: none on this resource — guardrail content is
// operational metadata, not secret material. Decision #36 redaction
// stays downstream.
type bedrockGuardrailEnricher struct {
	// fetch is overridable for tests. Defaults to a real GetGuardrail
	// call against the bedrock.Client in EnrichClients. Tests inject a
	// fake by constructing the enricher with a custom fetch — keeps the
	// enricher hermetically testable without spinning up an HTTP server
	// for the SDK client.
	fetch func(ctx context.Context, c *bedrock.Client, guardrailID, version string) (*bedrock.GetGuardrailOutput, error)
}

// newBedrockGuardrailEnricher returns the production-wired enricher.
// AWSDiscoverer's byTypeEnricher map registers this under
// "aws_bedrock_guardrail".
func newBedrockGuardrailEnricher() *bedrockGuardrailEnricher {
	return &bedrockGuardrailEnricher{fetch: defaultBedrockGuardrailFetch}
}

func (bedrockGuardrailEnricher) ResourceType() string { return bedrockGuardrailTFType }

// Enrich populates ir.Attrs with a typed AWSBedrockGuardrail payload
// for the guardrail identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.Bedrock is nil; any other
// error reflects a real Bedrock API failure on the GetGuardrail call.
// A typed ResourceNotFoundException is mapped onto ErrNotFound so drift
// flows can distinguish a deleted guardrail from a real API failure.
func (e bedrockGuardrailEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.Bedrock == nil {
		return ErrEnrichClientUnavailable
	}
	guardrailID, version := bedrockGuardrailIDForEnrich(&ir.Identity)
	if guardrailID == "" {
		return fmt.Errorf("bedrock_guardrail: cannot derive guardrail id from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	out, err := e.fetch(ctx, c.Bedrock, guardrailID, version)
	if err != nil {
		var notFound *bedrocktypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return fmt.Errorf("bedrock_guardrail %q: %w", guardrailID, ErrNotFound)
		}
		return fmt.Errorf("bedrock_guardrail: get %q: %w", guardrailID, err)
	}
	if out == nil {
		return fmt.Errorf("bedrock_guardrail %q: %w", guardrailID, ErrNotFound)
	}

	// Stamp guardrail_arn on Identity.NativeIDs so downstream consumers
	// don't have to round-trip back to the SDK for the ARN. The pure-
	// mapping helper does NOT touch ir.Identity per the
	// AttributeEnricher contract; this is the only place the enricher
	// writes to it.
	if arn := aws.ToString(out.GuardrailArn); arn != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["arn"] = arn
	}

	typed := mapBedrockGuardrail(out)

	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("bedrock_guardrail: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSBedrockGuardrail payload for the
// guardrail named by identity and returns it as the json.RawMessage
// shape that would land in ImportedResource.Attrs. Shares the SDK call
// + mapping with Enrich via the private mapBedrockGuardrail helper so
// the two paths cannot drift out of sync.
func (e bedrockGuardrailEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New("bedrock_guardrail: nil identity")
	}
	if c.Bedrock == nil {
		return nil, ErrEnrichClientUnavailable
	}
	guardrailID, version := bedrockGuardrailIDForEnrich(identity)
	if guardrailID == "" {
		return nil, fmt.Errorf("bedrock_guardrail: cannot derive guardrail id from Identity (Address=%q ImportID=%q NameHint=%q)",
			identity.Address, identity.ImportID, identity.NameHint)
	}
	out, err := e.fetch(ctx, c.Bedrock, guardrailID, version)
	if err != nil {
		var notFound *bedrocktypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("bedrock_guardrail %q: %w", guardrailID, ErrNotFound)
		}
		return nil, fmt.Errorf("bedrock_guardrail: get %q: %w", guardrailID, err)
	}
	if out == nil {
		return nil, fmt.Errorf("bedrock_guardrail %q: %w", guardrailID, ErrNotFound)
	}
	typed := mapBedrockGuardrail(out)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("bedrock_guardrail: marshal Attrs: %w", err)
	}
	return raw, nil
}

// bedrockGuardrailIDForEnrich pulls the (guardrail_id, version) pair
// from the identifiers the bedrock_guardrail discoverer populates.
// Order of preference:
//
//  1. Identity.NativeIDs["guardrail_id"] / NativeIDs["version"] — the
//     discoverer's canonical fields.
//  2. Identity.ImportID parsed as "<guardrail_id>,<version>" — the
//     terraform-provider-aws shape; works for callers refreshing a
//     single row via EnrichByID without rehydrating NativeIDs.
//
// Version defaults to "DRAFT" when missing — matches the discoverer's
// fallback.
func bedrockGuardrailIDForEnrich(id *imported.ResourceIdentity) (string, string) {
	if id == nil {
		return "", ""
	}
	guardrailID := strings.TrimSpace(id.NativeIDs["guardrail_id"])
	version := strings.TrimSpace(id.NativeIDs["version"])
	if guardrailID == "" {
		// Fall through to ImportID parsing.
		s := strings.TrimSpace(id.ImportID)
		if s == "" {
			return "", ""
		}
		if i := strings.Index(s, ","); i > 0 {
			guardrailID = strings.TrimSpace(s[:i])
			if version == "" {
				version = strings.TrimSpace(s[i+1:])
			}
		} else {
			guardrailID = s
		}
	}
	if version == "" {
		version = "DRAFT"
	}
	return guardrailID, version
}

// defaultBedrockGuardrailFetch is the production fetch path: a single
// GetGuardrail call with the guardrail identifier and (optionally) a
// version. Bedrock accepts an empty GuardrailVersion to return the
// DRAFT version; we pass the resolved version through so the response
// matches the imported row's recorded version.
func defaultBedrockGuardrailFetch(ctx context.Context, c *bedrock.Client, guardrailID, version string) (*bedrock.GetGuardrailOutput, error) {
	in := &bedrock.GetGuardrailInput{GuardrailIdentifier: aws.String(guardrailID)}
	if version != "" && version != "DRAFT" {
		in.GuardrailVersion = aws.String(version)
	}
	out, err := c.GetGuardrail(ctx, in)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mapBedrockGuardrail is the pure-mapping helper shared by Enrich and
// EnrichByID. Hand-rolled (no enrichgen): the Layer 1 typed surface
// has multiple nested-block families and the SDK responses use
// per-family slice types, so a hand-rolled translator is more readable
// than the reflection-driven enrichgen output.
//
// Decision-#34 cleanliness: every field is emitted only when present on
// the API response, so the resulting HCL does not contain "field =
// null" noise.
func mapBedrockGuardrail(out *bedrock.GetGuardrailOutput) *generated.AWSBedrockGuardrail {
	typed := &generated.AWSBedrockGuardrail{}

	if name := aws.ToString(out.Name); name != "" {
		typed.Name = generated.LiteralOf(name)
	}
	if desc := aws.ToString(out.Description); desc != "" {
		typed.Description = generated.LiteralOf(desc)
	}
	if id := aws.ToString(out.GuardrailId); id != "" {
		typed.GuardrailID = generated.LiteralOf(id)
	}
	if arn := aws.ToString(out.GuardrailArn); arn != "" {
		typed.GuardrailARN = generated.LiteralOf(arn)
	}
	if v := aws.ToString(out.Version); v != "" {
		typed.Version = generated.LiteralOf(v)
	}
	if s := string(out.Status); s != "" {
		typed.Status = generated.LiteralOf(s)
	}
	if out.CreatedAt != nil {
		typed.CreatedAt = generated.LiteralOf(out.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if k := aws.ToString(out.KmsKeyArn); k != "" {
		typed.KMSKeyARN = generated.LiteralOf(k)
	}
	if msg := aws.ToString(out.BlockedInputMessaging); msg != "" {
		typed.BlockedInputMessaging = generated.LiteralOf(msg)
	}
	if msg := aws.ToString(out.BlockedOutputsMessaging); msg != "" {
		typed.BlockedOutputsMessaging = generated.LiteralOf(msg)
	}

	// Content policy filters.
	if out.ContentPolicy != nil && len(out.ContentPolicy.Filters) > 0 {
		filters := make([]generated.AWSBedrockGuardrailContentPolicyConfigFiltersConfig, 0, len(out.ContentPolicy.Filters))
		for i := range out.ContentPolicy.Filters {
			f := &out.ContentPolicy.Filters[i]
			block := generated.AWSBedrockGuardrailContentPolicyConfigFiltersConfig{}
			if s := string(f.InputStrength); s != "" {
				block.InputStrength = generated.LiteralOf(s)
			}
			if s := string(f.OutputStrength); s != "" {
				block.OutputStrength = generated.LiteralOf(s)
			}
			if s := string(f.Type); s != "" {
				block.Type_ = generated.LiteralOf(s)
			}
			filters = append(filters, block)
		}
		typed.ContentPolicyConfig = []generated.AWSBedrockGuardrailContentPolicyConfig{{FiltersConfig: filters}}
	}

	// Contextual grounding filters.
	if out.ContextualGroundingPolicy != nil && len(out.ContextualGroundingPolicy.Filters) > 0 {
		filters := make([]generated.AWSBedrockGuardrailContextualGroundingPolicyConfigFiltersConfig, 0, len(out.ContextualGroundingPolicy.Filters))
		for i := range out.ContextualGroundingPolicy.Filters {
			f := &out.ContextualGroundingPolicy.Filters[i]
			block := generated.AWSBedrockGuardrailContextualGroundingPolicyConfigFiltersConfig{}
			if f.Threshold != nil {
				block.Threshold = generated.LiteralOf(*f.Threshold)
			}
			if s := string(f.Type); s != "" {
				block.Type_ = generated.LiteralOf(s)
			}
			filters = append(filters, block)
		}
		typed.ContextualGroundingPolicyConfig = []generated.AWSBedrockGuardrailContextualGroundingPolicyConfig{{FiltersConfig: filters}}
	}

	// Sensitive-information policy.
	if out.SensitiveInformationPolicy != nil {
		sip := generated.AWSBedrockGuardrailSensitiveInformationPolicyConfig{}
		if len(out.SensitiveInformationPolicy.PiiEntities) > 0 {
			entities := make([]generated.AWSBedrockGuardrailSensitiveInformationPolicyConfigPiiEntitiesConfig, 0, len(out.SensitiveInformationPolicy.PiiEntities))
			for i := range out.SensitiveInformationPolicy.PiiEntities {
				p := &out.SensitiveInformationPolicy.PiiEntities[i]
				block := generated.AWSBedrockGuardrailSensitiveInformationPolicyConfigPiiEntitiesConfig{}
				if s := string(p.Action); s != "" {
					block.Action = generated.LiteralOf(s)
				}
				if s := string(p.Type); s != "" {
					block.Type_ = generated.LiteralOf(s)
				}
				entities = append(entities, block)
			}
			sip.PiiEntitiesConfig = entities
		}
		if len(out.SensitiveInformationPolicy.Regexes) > 0 {
			regexes := make([]generated.AWSBedrockGuardrailSensitiveInformationPolicyConfigRegexesConfig, 0, len(out.SensitiveInformationPolicy.Regexes))
			for i := range out.SensitiveInformationPolicy.Regexes {
				r := &out.SensitiveInformationPolicy.Regexes[i]
				block := generated.AWSBedrockGuardrailSensitiveInformationPolicyConfigRegexesConfig{}
				if s := string(r.Action); s != "" {
					block.Action = generated.LiteralOf(s)
				}
				if s := aws.ToString(r.Description); s != "" {
					block.Description = generated.LiteralOf(s)
				}
				if s := aws.ToString(r.Name); s != "" {
					block.Name = generated.LiteralOf(s)
				}
				if s := aws.ToString(r.Pattern); s != "" {
					block.Pattern = generated.LiteralOf(s)
				}
				regexes = append(regexes, block)
			}
			sip.RegexesConfig = regexes
		}
		if len(sip.PiiEntitiesConfig) > 0 || len(sip.RegexesConfig) > 0 {
			typed.SensitiveInformationPolicyConfig = []generated.AWSBedrockGuardrailSensitiveInformationPolicyConfig{sip}
		}
	}

	// Topic policy.
	if out.TopicPolicy != nil && len(out.TopicPolicy.Topics) > 0 {
		topics := make([]generated.AWSBedrockGuardrailTopicPolicyConfigTopicsConfig, 0, len(out.TopicPolicy.Topics))
		for i := range out.TopicPolicy.Topics {
			tp := &out.TopicPolicy.Topics[i]
			block := generated.AWSBedrockGuardrailTopicPolicyConfigTopicsConfig{}
			if s := aws.ToString(tp.Name); s != "" {
				block.Name = generated.LiteralOf(s)
			}
			if s := aws.ToString(tp.Definition); s != "" {
				block.Definition = generated.LiteralOf(s)
			}
			if s := string(tp.Type); s != "" {
				block.Type_ = generated.LiteralOf(s)
			}
			if len(tp.Examples) > 0 {
				examples := make([]*generated.Value[string], 0, len(tp.Examples))
				for _, ex := range tp.Examples {
					examples = append(examples, generated.LiteralOf(ex))
				}
				block.Examples = examples
			}
			topics = append(topics, block)
		}
		typed.TopicPolicyConfig = []generated.AWSBedrockGuardrailTopicPolicyConfig{{TopicsConfig: topics}}
	}

	// Word policy.
	if out.WordPolicy != nil {
		wpc := generated.AWSBedrockGuardrailWordPolicyConfig{}
		if len(out.WordPolicy.Words) > 0 {
			words := make([]generated.AWSBedrockGuardrailWordPolicyConfigWordsConfig, 0, len(out.WordPolicy.Words))
			for i := range out.WordPolicy.Words {
				w := &out.WordPolicy.Words[i]
				block := generated.AWSBedrockGuardrailWordPolicyConfigWordsConfig{}
				if s := aws.ToString(w.Text); s != "" {
					block.Text = generated.LiteralOf(s)
				}
				words = append(words, block)
			}
			wpc.WordsConfig = words
		}
		if len(out.WordPolicy.ManagedWordLists) > 0 {
			lists := make([]generated.AWSBedrockGuardrailWordPolicyConfigManagedWordListsConfig, 0, len(out.WordPolicy.ManagedWordLists))
			for i := range out.WordPolicy.ManagedWordLists {
				ml := &out.WordPolicy.ManagedWordLists[i]
				block := generated.AWSBedrockGuardrailWordPolicyConfigManagedWordListsConfig{}
				if s := string(ml.Type); s != "" {
					block.Type_ = generated.LiteralOf(s)
				}
				lists = append(lists, block)
			}
			wpc.ManagedWordListsConfig = lists
		}
		if len(wpc.WordsConfig) > 0 || len(wpc.ManagedWordListsConfig) > 0 {
			typed.WordPolicyConfig = []generated.AWSBedrockGuardrailWordPolicyConfig{wpc}
		}
	}

	return typed
}

// Compile-time assertions: bedrockGuardrailEnricher must satisfy both
// AttributeEnricher and ByIDEnricher. Per Phase 2 contract, every new
// enricher implements ByIDEnricher.
var (
	_ AttributeEnricher = (*bedrockGuardrailEnricher)(nil)
	_ ByIDEnricher      = (*bedrockGuardrailEnricher)(nil)
)
