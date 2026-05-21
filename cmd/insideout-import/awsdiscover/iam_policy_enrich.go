// Package awsdiscover — IAM customer-managed policy attribute enricher
// (#661).
//
// Pairs with the Cloud-Control-routed `aws_iam_policy` discoverer
// registered in cloudControlTypeConfigs ("AWS::IAM::ManagedPolicy").
//
// **Why a hand-rolled override**: `aws_iam_policy.policy` is a REQUIRED
// JSON-encoded string. The generic Cloud Control enricher relies on the
// `jsonStringifyField("PolicyDocument", "Policy")` normalizer (#1621),
// which assumes Cloud Control's GetResource returns a nested
// `PolicyDocument` property. The CFN AWS::IAM::ManagedPolicy schema
// treats `PolicyDocument` as create-time *input*, not a stably-readable
// property — GetResource frequently omits it. The normalizer is
// fail-open, so the required `policy` argument is silently left empty
// and the composer flags the resource `imported_resource_missing_
// required_attr`.
//
// This enricher does the two standard IAM read calls instead:
//
//  1. iam:GetPolicy(PolicyArn)            → Policy.DefaultVersionId
//     (plus Name / Path / Description / Tags metadata).
//  2. iam:GetPolicyVersion(PolicyArn, V)  → PolicyVersion.Document,
//     a URL-encoded (RFC 3986) JSON string that is URL-decoded and
//     re-compacted into the `policy` argument.
//
// Soft-fails NoSuchEntity onto ErrNotFound so a policy deleted between
// discovery and enrichment downgrades to a per-resource warn rather
// than failing the batch (issue #654 semantics).
//
// **Computed-only / TF-input-only fields skipped per decision #5:**
//   - `arn` (Computed) — stamped on ir.Identity.NativeIDs["arn"]
//     instead, matching the secretsmanager_secret pattern.
//   - `attachment_count`, `policy_id` (Computed).
//   - `id` (Optional+Computed alias for ARN).
//   - `name_prefix` (Optional+Computed; provider derives Name from it).
//   - `tags_all` (Computed; provider merges defaults at plan time).
//   - `delay_after_policy_creation_in_ms` (TF-input only; no API
//     source).
package awsdiscover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// iamPolicyTFType is the registered Terraform type for the IAM policy
// enricher. Kept as a constant so the registry / ResourceType() stay
// in lockstep.
const iamPolicyTFType = "aws_iam_policy"

// iamPolicyData is the combined result of the two IAM read calls the
// enricher issues. Policy carries the GetPolicy metadata; Document is
// the URL-decoded + JSON-compacted policy document from the default
// policy version.
type iamPolicyData struct {
	Policy   *iamtypes.Policy
	Document string
}

// iamPolicyEnricher implements both AttributeEnricher and ByIDEnricher
// for aws_iam_policy.
type iamPolicyEnricher struct {
	// fetch is overridable for tests. Defaults to the real two-call
	// GetPolicy + GetPolicyVersion path against the iam.Client in
	// EnrichClients. Tests inject a fake by constructing the enricher
	// with a custom fetch — keeps the enricher hermetically testable
	// without spinning up an HTTP server for the SDK client.
	fetch func(ctx context.Context, c *iam.Client, policyARN string) (*iamPolicyData, error)
}

// newIAMPolicyEnricher returns the production-wired enricher.
// AWSDiscoverer's byTypeEnricher map registers this under
// "aws_iam_policy", overriding the generic Cloud Control fallback.
func newIAMPolicyEnricher() *iamPolicyEnricher {
	return &iamPolicyEnricher{fetch: defaultIAMPolicyFetch}
}

func (iamPolicyEnricher) ResourceType() string { return iamPolicyTFType }

// Enrich populates ir.Attrs with a typed AWSIAMPolicy payload for the
// policy identified by ir.Identity. Returns ErrEnrichClientUnavailable
// if EnrichClients.IAM is nil, ErrNotFound if the policy no longer
// exists, and any other error reflects a real IAM API failure.
func (e iamPolicyEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.IAM == nil {
		return ErrEnrichClientUnavailable
	}
	arn := iamPolicyARNForEnrich(&ir.Identity)
	if arn == "" {
		return fmt.Errorf("%s: cannot derive policy ARN from Identity (Address=%q ImportID=%q)",
			iamPolicyTFType, ir.Identity.Address, ir.Identity.ImportID)
	}
	data, err := e.fetch(ctx, c.IAM, arn)
	if err != nil {
		if isAPIErrorCode(err, "NoSuchEntity", "NoSuchEntityException") {
			return fmt.Errorf("%s %q: %w", iamPolicyTFType, arn, ErrNotFound)
		}
		return fmt.Errorf("%s: fetch %q: %w", iamPolicyTFType, arn, err)
	}
	if data == nil {
		return fmt.Errorf("%s: fetch %q: empty response", iamPolicyTFType, arn)
	}

	// Stamp ARN on Identity.NativeIDs so downstream consumers don't
	// have to round-trip back to the SDK for the ARN. The pure-mapping
	// helper does NOT touch ir.Identity per the AttributeEnricher
	// contract; this is the only place Enrich writes to it. Prefer the
	// API-returned ARN, falling back to the ARN the lookup used.
	stampARN := arn
	if data.Policy != nil {
		if a := aws.ToString(data.Policy.Arn); a != "" {
			stampARN = a
		}
	}
	if ir.Identity.NativeIDs == nil {
		ir.Identity.NativeIDs = map[string]string{}
	}
	ir.Identity.NativeIDs["arn"] = stampARN

	raw, err := json.Marshal(mapIAMPolicy(data))
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", iamPolicyTFType, err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSIAMPolicy payload for the policy
// named by identity and returns it as the json.RawMessage shape that
// would land in ImportedResource.Attrs. Shares the SDK calls + mapping
// with Enrich via the private fetch hook + mapIAMPolicy helper so the
// two paths cannot drift out of sync. Does not mutate identity.
func (e iamPolicyEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.IAM == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(iamPolicyTFType + ": identity is nil")
	}
	arn := iamPolicyARNForEnrich(identity)
	if arn == "" {
		return nil, fmt.Errorf("%s: cannot derive policy ARN from Identity (Address=%q ImportID=%q)",
			iamPolicyTFType, identity.Address, identity.ImportID)
	}
	data, err := e.fetch(ctx, c.IAM, arn)
	if err != nil {
		if isAPIErrorCode(err, "NoSuchEntity", "NoSuchEntityException") {
			return nil, fmt.Errorf("%s %q: %w", iamPolicyTFType, arn, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: fetch %q: %w", iamPolicyTFType, arn, err)
	}
	if data == nil {
		return nil, fmt.Errorf("%s: fetch %q: empty response", iamPolicyTFType, arn)
	}
	raw, err := json.Marshal(mapIAMPolicy(data))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", iamPolicyTFType, err)
	}
	return raw, nil
}

// iamPolicyARNForEnrich derives the PolicyArn argument for GetPolicy.
// Both iam:GetPolicy and iam:GetPolicyVersion require the full ARN —
// the bare policy name is not accepted — so NameHint (the policy name)
// is intentionally NOT a fallback here.
//
// Preference order:
//
//  1. Identity.NativeIDs["arn"] — the Cloud Control discoverer's
//     NativeIDsFromProperties stamps the policy ARN here.
//  2. Identity.ImportID — the discoverer's passthroughImportID emits
//     the policy ARN as the import ID for aws_iam_policy.
func iamPolicyARNForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if s := strings.TrimSpace(id.NativeIDs["arn"]); s != "" {
		return s
	}
	return strings.TrimSpace(id.ImportID)
}

// iamPolicyAPI is the narrow subset of the IAM API the aws_iam_policy
// enricher's fetch path issues. The real *iam.Client and in-test fakes
// both satisfy it, so the two-call orchestration in
// fetchIAMPolicyWithClient is unit-testable without a stubbed HTTP
// transport. Mirrors the narrow-client-interface convention in
// sdkonly_iam.go.
type iamPolicyAPI interface {
	GetPolicy(ctx context.Context, in *iam.GetPolicyInput, opts ...func(*iam.Options)) (*iam.GetPolicyOutput, error)
	GetPolicyVersion(ctx context.Context, in *iam.GetPolicyVersionInput, opts ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error)
}

// defaultIAMPolicyFetch is the production fetch path: it wires the real
// *iam.Client into fetchIAMPolicyWithClient, which holds the two-call
// orchestration logic behind the narrow iamPolicyAPI interface.
func defaultIAMPolicyFetch(ctx context.Context, c *iam.Client, policyARN string) (*iamPolicyData, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return fetchIAMPolicyWithClient(ctx, c, policyARN)
}

// fetchIAMPolicyWithClient calls GetPolicy to resolve the default
// version ID and metadata, then GetPolicyVersion to retrieve the policy
// document. The document is URL-encoded (RFC 3986) and is decoded +
// compacted by decodeIAMPolicyDocument.
func fetchIAMPolicyWithClient(ctx context.Context, c iamPolicyAPI, policyARN string) (*iamPolicyData, error) {
	gp, err := c.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(policyARN)})
	if err != nil {
		return nil, fmt.Errorf("iam:GetPolicy: %w", err)
	}
	if gp == nil || gp.Policy == nil {
		return nil, errors.New("iam:GetPolicy: nil policy in response")
	}
	versionID := aws.ToString(gp.Policy.DefaultVersionId)
	if versionID == "" {
		return nil, errors.New("iam:GetPolicy: response carries no DefaultVersionId")
	}
	gpv, err := c.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
		PolicyArn: aws.String(policyARN),
		VersionId: aws.String(versionID),
	})
	if err != nil {
		return nil, fmt.Errorf("iam:GetPolicyVersion: %w", err)
	}
	if gpv == nil || gpv.PolicyVersion == nil {
		return nil, errors.New("iam:GetPolicyVersion: nil policy version in response")
	}
	doc, err := decodeIAMPolicyDocument(aws.ToString(gpv.PolicyVersion.Document))
	if err != nil {
		return nil, fmt.Errorf("iam:GetPolicyVersion: decode document: %w", err)
	}
	return &iamPolicyData{Policy: gp.Policy, Document: doc}, nil
}

// decodeIAMPolicyDocument turns the raw GetPolicyVersion document into
// the compact JSON string that lands in `aws_iam_policy.policy`.
//
// The IAM API returns the document URL-encoded compliant with RFC 3986;
// url.QueryUnescape reverses that. If unescaping fails the raw value is
// used as-is (some callers / fakes pass an already-decoded document).
// The result is then re-compacted via json.Compact so the emitted
// `policy` string is stable regardless of the API's whitespace — a
// non-JSON document is a hard error since `policy` must be valid JSON.
//
// An empty document yields an empty string (the caller omits the
// `policy` field entirely in that case).
func decodeIAMPolicyDocument(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		decoded = raw
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(decoded)); err != nil {
		return "", fmt.Errorf("policy document is not valid JSON: %w", err)
	}
	return buf.String(), nil
}

// mapIAMPolicy is the pure-mapping helper shared by Enrich and
// EnrichByID. Every field is emitted only when present on the API
// response so the resulting HCL stays decision-#34 clean (no
// "field = null" noise).
func mapIAMPolicy(d *iamPolicyData) *generated.AWSIAMPolicy {
	typed := &generated.AWSIAMPolicy{}
	if d == nil {
		return typed
	}
	if p := d.Policy; p != nil {
		if name := aws.ToString(p.PolicyName); name != "" {
			typed.Name = generated.LiteralOf(name)
		}
		if path := aws.ToString(p.Path); path != "" {
			typed.Path = generated.LiteralOf(path)
		}
		if desc := aws.ToString(p.Description); desc != "" {
			typed.Description = generated.LiteralOf(desc)
		}
		if len(p.Tags) > 0 {
			m := map[string]*generated.Value[string]{}
			for _, t := range p.Tags {
				if t.Key != nil {
					m[*t.Key] = generated.LiteralOf(aws.ToString(t.Value))
				}
			}
			if len(m) > 0 {
				typed.Tags = m
			}
		}
	}
	if d.Document != "" {
		typed.Policy = generated.LiteralOf(d.Document)
	}
	return typed
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*iamPolicyEnricher)(nil)
	_ ByIDEnricher      = (*iamPolicyEnricher)(nil)
)
