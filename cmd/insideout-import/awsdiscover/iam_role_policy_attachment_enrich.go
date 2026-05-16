// Package awsdiscover — IAM role policy attachment attribute enricher (#482).
//
// Pairs with the SDK-only sub-resource discoverer for
// `aws_iam_role_policy_attachment` (sdkonly_iam.go). The discoverer
// emits one ImportedResource per (role, policy_arn) pair attached to
// a non-service-linked IAM role; the enricher confirms the attachment
// still exists and produces a typed AWSIAMRolePolicyAttachment payload.
//
// **Why not a single Get RPC**: IAM has no `GetRolePolicyAttachment`
// SDK method — attachments are managed via Attach/DetachRolePolicy
// plus enumerated via ListAttachedRolePolicies. The enricher uses the
// same ListAttachedRolePolicies SDK call as the discoverer's
// FetchItems, but scans for the specific policy_arn rather than
// emitting every attached policy. Cost: one ListAttachedRolePolicies
// per enriched attachment (IAM caps attachments per role at 20 by
// default, so the list call returns a small constant-size page in the
// common case). The cost is comparable to a Get RPC and the API
// surface is the only one AWS offers.
//
// Identity carries NativeIDs["role"] and NativeIDs["policy_arn"]
// (discoverer-set), plus ImportID in "<role>/<policy_arn>" form.
// The enricher reads either path so by-ID callers that synthesize
// Identity from the import ID alone still resolve correctly.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

const iamRolePolicyAttachmentTFType = "aws_iam_role_policy_attachment"

// iamRolePolicyAttachmentEnricher implements both AttributeEnricher
// and ByIDEnricher.
type iamRolePolicyAttachmentEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// ListAttachedRolePolicies call that pages until the target
	// policy ARN is found (or pagination exhausts).
	fetch func(ctx context.Context, c *iam.Client, roleName, policyARN string) (found bool, err error)
}

func newIAMRolePolicyAttachmentEnricher() *iamRolePolicyAttachmentEnricher {
	return &iamRolePolicyAttachmentEnricher{fetch: defaultIAMRolePolicyAttachmentFetch}
}

func (iamRolePolicyAttachmentEnricher) ResourceType() string {
	return iamRolePolicyAttachmentTFType
}

func (e iamRolePolicyAttachmentEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.IAM == nil {
		return ErrEnrichClientUnavailable
	}
	role, policyARN, err := iamRolePolicyAttachmentParts(&ir.Identity)
	if err != nil {
		return err
	}
	found, ferr := e.fetch(ctx, c.IAM, role, policyARN)
	if ferr != nil {
		if isAPIErrorCode(ferr, "NoSuchEntity", "NoSuchEntityException") {
			return fmt.Errorf("%s (role=%s, policy_arn=%s): %w", iamRolePolicyAttachmentTFType, role, policyARN, ErrNotFound)
		}
		return fmt.Errorf("%s: list attached role policies (role=%s): %w", iamRolePolicyAttachmentTFType, role, ferr)
	}
	if !found {
		return fmt.Errorf("%s (role=%s, policy_arn=%s): %w", iamRolePolicyAttachmentTFType, role, policyARN, ErrNotFound)
	}
	typed := mapIAMRolePolicyAttachment(role, policyARN)
	raw, mErr := json.Marshal(typed)
	if mErr != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", iamRolePolicyAttachmentTFType, mErr)
	}
	ir.Attrs = raw
	return nil
}

func (e iamRolePolicyAttachmentEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.IAM == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(iamRolePolicyAttachmentTFType + ": identity is nil")
	}
	role, policyARN, err := iamRolePolicyAttachmentParts(identity)
	if err != nil {
		return nil, err
	}
	found, ferr := e.fetch(ctx, c.IAM, role, policyARN)
	if ferr != nil {
		if isAPIErrorCode(ferr, "NoSuchEntity", "NoSuchEntityException") {
			return nil, fmt.Errorf("%s (role=%s, policy_arn=%s): %w", iamRolePolicyAttachmentTFType, role, policyARN, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: list attached role policies (role=%s): %w", iamRolePolicyAttachmentTFType, role, ferr)
	}
	if !found {
		return nil, fmt.Errorf("%s (role=%s, policy_arn=%s): %w", iamRolePolicyAttachmentTFType, role, policyARN, ErrNotFound)
	}
	typed := mapIAMRolePolicyAttachment(role, policyARN)
	raw, mErr := json.Marshal(typed)
	if mErr != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", iamRolePolicyAttachmentTFType, mErr)
	}
	return raw, nil
}

// iamRolePolicyAttachmentParts resolves (role, policy_arn) from the
// discoverer-populated Identity. Preference order mirrors the
// apigatewayv2_stage helper:
//
//  1. Identity.NativeIDs["role"] + ["policy_arn"] (discoverer-set).
//  2. Identity.ImportID parsed as "<role>/<policy_arn>" — a policy ARN
//     contains its own slashes, so SplitN(2) preserves them on the
//     ARN half.
func iamRolePolicyAttachmentParts(id *imported.ResourceIdentity) (string, string, error) {
	if id == nil {
		return "", "", errors.New(iamRolePolicyAttachmentTFType + ": identity is nil")
	}
	role := strings.TrimSpace(id.NativeIDs["role"])
	policyARN := strings.TrimSpace(id.NativeIDs["policy_arn"])
	if role != "" && policyARN != "" {
		return role, policyARN, nil
	}
	if imp := strings.TrimSpace(id.ImportID); imp != "" {
		// Import format: "<role_name>/<policy_arn>". Split on the FIRST
		// "/" because policy ARNs (e.g. arn:aws:iam::aws:policy/...) carry
		// their own slashes after the path prefix.
		idx := strings.Index(imp, "/")
		if idx > 0 && idx < len(imp)-1 {
			return imp[:idx], imp[idx+1:], nil
		}
	}
	return "", "", fmt.Errorf("%s: cannot resolve (role, policy_arn) from Identity (Address=%q ImportID=%q)",
		iamRolePolicyAttachmentTFType, id.Address, id.ImportID)
}

// defaultIAMRolePolicyAttachmentFetch is the production fetch path: a
// paginated ListAttachedRolePolicies scan that short-circuits as soon
// as the target policy_arn is found. Returns (false, nil) if pagination
// exhausts without a hit — surfaces as ErrNotFound at the call site.
func defaultIAMRolePolicyAttachmentFetch(ctx context.Context, c *iam.Client, roleName, policyARN string) (bool, error) {
	if c == nil {
		return false, ErrEnrichClientUnavailable
	}
	var marker *string
	for {
		page, err := c.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
			RoleName: aws.String(roleName),
			Marker:   marker,
		})
		if err != nil {
			return false, err
		}
		for _, ap := range page.AttachedPolicies {
			if aws.ToString(ap.PolicyArn) == policyARN {
				return true, nil
			}
		}
		if !page.IsTruncated || page.Marker == nil || aws.ToString(page.Marker) == "" {
			return false, nil
		}
		marker = page.Marker
	}
}

// mapIAMRolePolicyAttachment builds the typed payload from the
// (role, policy_arn) pair. The TF state stores the import ID as the
// resource id; we replicate that here so downstream consumers don't
// have to reconstruct it.
func mapIAMRolePolicyAttachment(role, policyARN string) *generated.AWSIAMRolePolicyAttachment {
	return &generated.AWSIAMRolePolicyAttachment{
		Role:      generated.LiteralOf(role),
		PolicyARN: generated.LiteralOf(policyARN),
		ID:        generated.LiteralOf(role + "/" + policyARN),
	}
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*iamRolePolicyAttachmentEnricher)(nil)
	_ ByIDEnricher      = (*iamRolePolicyAttachmentEnricher)(nil)
)
