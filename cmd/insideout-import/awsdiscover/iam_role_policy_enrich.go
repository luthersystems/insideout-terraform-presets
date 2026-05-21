// Package awsdiscover — IAM inline role-policy attribute enricher
// (#661 follow-up).
//
// Pairs with the Cloud-Control-routed `aws_iam_role_policy` discoverer
// registered in cloudControlTypeConfigs ("AWS::IAM::RolePolicy").
//
// **Why a hand-rolled override**: `aws_iam_role_policy.policy` is a
// REQUIRED JSON-encoded string with the same Cloud-Control read-back
// gap as aws_iam_policy (#661) — the CFN schema treats the inline
// policy document as create-time input. One iam:GetRolePolicy call
// returns the document (URL-encoded) plus the role/policy names.
//
// Distinct from aws_iam_role_policy_attachment: that resource binds a
// *managed* policy ARN to a role; this resource is an *inline* policy
// document embedded in the role. They share neither the API nor the
// import-ID shape.
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

// iamRolePolicyTFType is the registered Terraform type for the inline
// role-policy enricher.
const iamRolePolicyTFType = "aws_iam_role_policy"

// iamRolePolicyData is the result of the GetRolePolicy call: the
// resolved role/policy names and the URL-decoded + compacted policy
// document.
type iamRolePolicyData struct {
	RoleName   string
	PolicyName string
	Document   string
}

// iamRolePolicyAPI is the narrow subset of the IAM API the
// aws_iam_role_policy enricher's fetch path issues.
type iamRolePolicyAPI interface {
	GetRolePolicy(ctx context.Context, in *iam.GetRolePolicyInput, opts ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error)
}

// iamRolePolicyEnricher implements both AttributeEnricher and
// ByIDEnricher for aws_iam_role_policy.
type iamRolePolicyEnricher struct {
	// fetch is overridable for tests. Defaults to the real
	// GetRolePolicy path against the iam.Client in EnrichClients.
	fetch func(ctx context.Context, c *iam.Client, roleName, policyName string) (*iamRolePolicyData, error)
}

// newIAMRolePolicyEnricher returns the production-wired enricher.
func newIAMRolePolicyEnricher() *iamRolePolicyEnricher {
	return &iamRolePolicyEnricher{fetch: defaultIAMRolePolicyFetch}
}

func (iamRolePolicyEnricher) ResourceType() string { return iamRolePolicyTFType }

// Enrich populates ir.Attrs with a typed AWSIAMRolePolicy payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.IAM is nil,
// ErrNotFound if the role or inline policy no longer exists, and any
// other error reflects a real IAM API failure.
func (e iamRolePolicyEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.IAM == nil {
		return ErrEnrichClientUnavailable
	}
	role, policy, err := iamRolePolicyParts(&ir.Identity)
	if err != nil {
		return err
	}
	data, ferr := e.fetch(ctx, c.IAM, role, policy)
	if ferr != nil {
		if isAPIErrorCode(ferr, "NoSuchEntity", "NoSuchEntityException") {
			return fmt.Errorf("%s (role=%s, policy=%s): %w", iamRolePolicyTFType, role, policy, ErrNotFound)
		}
		return fmt.Errorf("%s: fetch (role=%s, policy=%s): %w", iamRolePolicyTFType, role, policy, ferr)
	}
	if data == nil {
		return fmt.Errorf("%s: fetch (role=%s, policy=%s): empty response", iamRolePolicyTFType, role, policy)
	}
	raw, err := json.Marshal(mapIAMRolePolicy(data))
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", iamRolePolicyTFType, err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSIAMRolePolicy payload for the inline
// policy named by identity. Shares the SDK call + mapping with Enrich.
func (e iamRolePolicyEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.IAM == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(iamRolePolicyTFType + ": identity is nil")
	}
	role, policy, err := iamRolePolicyParts(identity)
	if err != nil {
		return nil, err
	}
	data, ferr := e.fetch(ctx, c.IAM, role, policy)
	if ferr != nil {
		if isAPIErrorCode(ferr, "NoSuchEntity", "NoSuchEntityException") {
			return nil, fmt.Errorf("%s (role=%s, policy=%s): %w", iamRolePolicyTFType, role, policy, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: fetch (role=%s, policy=%s): %w", iamRolePolicyTFType, role, policy, ferr)
	}
	if data == nil {
		return nil, fmt.Errorf("%s: fetch (role=%s, policy=%s): empty response", iamRolePolicyTFType, role, policy)
	}
	raw, err := json.Marshal(mapIAMRolePolicy(data))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", iamRolePolicyTFType, err)
	}
	return raw, nil
}

// iamRolePolicyParts resolves (roleName, policyName) from the
// discoverer-populated Identity. Preference order:
//
//  1. Identity.NativeIDs["role_name"] + ["policy_name"] — the Cloud
//     Control discoverer's NativeIDsFromProperties stamps both.
//  2. Identity.ImportID parsed as "<RoleName>:<PolicyName>" — the
//     discoverer rewrites the CC identifier into the Terraform import
//     form. IAM role and policy names cannot contain ":" (the allowed
//     set is [\w+=,.@-]), so a single SplitN(2) is unambiguous.
func iamRolePolicyParts(id *imported.ResourceIdentity) (string, string, error) {
	if id == nil {
		return "", "", errors.New(iamRolePolicyTFType + ": identity is nil")
	}
	role := strings.TrimSpace(id.NativeIDs["role_name"])
	policy := strings.TrimSpace(id.NativeIDs["policy_name"])
	if role != "" && policy != "" {
		return role, policy, nil
	}
	if imp := strings.TrimSpace(id.ImportID); imp != "" {
		parts := strings.SplitN(imp, ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
	}
	return "", "", fmt.Errorf("%s: cannot resolve (role, policy) from Identity (Address=%q ImportID=%q)",
		iamRolePolicyTFType, id.Address, id.ImportID)
}

// defaultIAMRolePolicyFetch is the production fetch path.
func defaultIAMRolePolicyFetch(ctx context.Context, c *iam.Client, roleName, policyName string) (*iamRolePolicyData, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return fetchIAMRolePolicyWithClient(ctx, c, roleName, policyName)
}

// fetchIAMRolePolicyWithClient issues GetRolePolicy behind the narrow
// iamRolePolicyAPI interface so the orchestration is unit-testable.
// The returned PolicyDocument is URL-encoded (RFC 3986) and is decoded
// + compacted by decodeIAMPolicyDocument.
func fetchIAMRolePolicyWithClient(ctx context.Context, c iamRolePolicyAPI, roleName, policyName string) (*iamRolePolicyData, error) {
	out, err := c.GetRolePolicy(ctx, &iam.GetRolePolicyInput{
		RoleName:   aws.String(roleName),
		PolicyName: aws.String(policyName),
	})
	if err != nil {
		return nil, fmt.Errorf("iam:GetRolePolicy: %w", err)
	}
	if out == nil {
		return nil, errors.New("iam:GetRolePolicy: nil response")
	}
	doc, err := decodeIAMPolicyDocument(aws.ToString(out.PolicyDocument))
	if err != nil {
		return nil, fmt.Errorf("iam:GetRolePolicy: decode document: %w", err)
	}
	// Prefer the API-echoed names; fall back to the request arguments
	// (GetRolePolicy always echoes them, but a defensive fallback keeps
	// the mapping correct if a future SDK shape changes).
	role := aws.ToString(out.RoleName)
	if role == "" {
		role = roleName
	}
	policy := aws.ToString(out.PolicyName)
	if policy == "" {
		policy = policyName
	}
	return &iamRolePolicyData{RoleName: role, PolicyName: policy, Document: doc}, nil
}

// mapIAMRolePolicy projects the fetched data into the typed
// AWSIAMRolePolicy struct. The Terraform resource id is the
// "<role>:<policy>" import form; downstream consumers don't have to
// reconstruct it.
func mapIAMRolePolicy(d *iamRolePolicyData) *generated.AWSIAMRolePolicy {
	typed := &generated.AWSIAMRolePolicy{}
	if d == nil {
		return typed
	}
	if d.RoleName != "" {
		typed.Role = generated.LiteralOf(d.RoleName)
	}
	if d.PolicyName != "" {
		typed.Name = generated.LiteralOf(d.PolicyName)
	}
	if d.RoleName != "" && d.PolicyName != "" {
		typed.ID = generated.LiteralOf(d.RoleName + ":" + d.PolicyName)
	}
	if d.Document != "" {
		typed.Policy = generated.LiteralOf(d.Document)
	}
	return typed
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*iamRolePolicyEnricher)(nil)
	_ ByIDEnricher      = (*iamRolePolicyEnricher)(nil)
)
