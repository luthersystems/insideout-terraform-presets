// Package awsdiscover — IAM role attribute enricher (#661 follow-up).
//
// Pairs with the Cloud-Control-routed `aws_iam_role` discoverer
// registered in cloudControlTypeConfigs ("AWS::IAM::Role").
//
// **Why a hand-rolled override**: `aws_iam_role.assume_role_policy` is
// a REQUIRED JSON-encoded string. The generic Cloud Control enricher
// reads `AWS::IAM::Role.AssumeRolePolicyDocument` as create-time input,
// not a stably-readable property — GetResource frequently omits it,
// leaving the required `assume_role_policy` argument empty and tripping
// the composer's `imported_resource_missing_required_attr`. Same gap as
// aws_iam_policy (#661); same fix shape.
//
// One iam:GetRole call returns every field the Layer-1 model needs —
// the trust policy (AssumeRolePolicyDocument, URL-encoded), the
// metadata (Name / Path / Description / MaxSessionDuration /
// PermissionsBoundary) and inline Tags — so no second call is needed.
//
// **Computed-only / TF-input-only / separate-resource fields skipped:**
//   - `arn` (Computed) — stamped on ir.Identity.NativeIDs["arn"].
//   - `id`, `unique_id`, `create_date` (Computed).
//   - `name_prefix` (Optional+Computed), `tags_all` (Computed).
//   - `force_detach_policies` (TF-input only; no API source).
//   - `inline_policy` / `managed_policy_arns` blocks — these duplicate
//     the standalone aws_iam_role_policy / aws_iam_role_policy_attachment
//     resources and the provider docs mark the in-role forms as
//     exclusive alternatives; the discoverer emits the standalone
//     resources, so populating the blocks here would double-count.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// iamRoleTFType is the registered Terraform type for the IAM role
// enricher.
const iamRoleTFType = "aws_iam_role"

// iamRoleAPI is the narrow subset of the IAM API the aws_iam_role
// enricher's fetch path issues. The real *iam.Client and in-test fakes
// both satisfy it, so fetchIAMRoleWithClient is unit-testable without a
// stubbed HTTP transport. Mirrors iamPolicyAPI.
type iamRoleAPI interface {
	GetRole(ctx context.Context, in *iam.GetRoleInput, opts ...func(*iam.Options)) (*iam.GetRoleOutput, error)
}

// iamRoleEnricher implements both AttributeEnricher and ByIDEnricher
// for aws_iam_role.
type iamRoleEnricher struct {
	// fetch is overridable for tests. Defaults to the real GetRole
	// path against the iam.Client in EnrichClients.
	fetch func(ctx context.Context, c *iam.Client, roleName string) (*iamtypes.Role, error)
}

// newIAMRoleEnricher returns the production-wired enricher.
func newIAMRoleEnricher() *iamRoleEnricher {
	return &iamRoleEnricher{fetch: defaultIAMRoleFetch}
}

func (iamRoleEnricher) ResourceType() string { return iamRoleTFType }

// Enrich populates ir.Attrs with a typed AWSIAMRole payload for the
// role identified by ir.Identity. Returns ErrEnrichClientUnavailable if
// EnrichClients.IAM is nil, ErrNotFound if the role no longer exists,
// and any other error reflects a real IAM API failure.
func (e iamRoleEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.IAM == nil {
		return ErrEnrichClientUnavailable
	}
	name := iamRoleNameForEnrich(&ir.Identity)
	if name == "" {
		return fmt.Errorf("%s: cannot derive role name from Identity (Address=%q ImportID=%q NameHint=%q)",
			iamRoleTFType, ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	role, err := e.fetch(ctx, c.IAM, name)
	if err != nil {
		if isAPIErrorCode(err, "NoSuchEntity", "NoSuchEntityException") {
			return fmt.Errorf("%s %q: %w", iamRoleTFType, name, ErrNotFound)
		}
		return fmt.Errorf("%s: fetch %q: %w", iamRoleTFType, name, err)
	}
	if role == nil {
		return fmt.Errorf("%s: fetch %q: empty response", iamRoleTFType, name)
	}

	// Stamp ARN on Identity.NativeIDs so downstream consumers don't
	// have to round-trip back to the SDK. The pure-mapping helper does
	// NOT touch ir.Identity per the AttributeEnricher contract.
	if arn := aws.ToString(role.Arn); arn != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["arn"] = arn
	}

	typed, err := mapIAMRole(role)
	if err != nil {
		return fmt.Errorf("%s %q: %w", iamRoleTFType, name, err)
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", iamRoleTFType, err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSIAMRole payload for the role named by
// identity. Shares the SDK call + mapping with Enrich. Does not mutate
// identity.
func (e iamRoleEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.IAM == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(iamRoleTFType + ": identity is nil")
	}
	name := iamRoleNameForEnrich(identity)
	if name == "" {
		return nil, fmt.Errorf("%s: cannot derive role name from Identity (Address=%q ImportID=%q NameHint=%q)",
			iamRoleTFType, identity.Address, identity.ImportID, identity.NameHint)
	}
	role, err := e.fetch(ctx, c.IAM, name)
	if err != nil {
		if isAPIErrorCode(err, "NoSuchEntity", "NoSuchEntityException") {
			return nil, fmt.Errorf("%s %q: %w", iamRoleTFType, name, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: fetch %q: %w", iamRoleTFType, name, err)
	}
	if role == nil {
		return nil, fmt.Errorf("%s: fetch %q: empty response", iamRoleTFType, name)
	}
	typed, err := mapIAMRole(role)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", iamRoleTFType, name, err)
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", iamRoleTFType, err)
	}
	return raw, nil
}

// iamRoleNameForEnrich derives the RoleName argument for GetRole.
// iam:GetRole takes the bare role name (not the ARN), and the Cloud
// Control discoverer's passthroughImportID emits the role name as the
// import ID for aws_iam_role.
//
// Preference order:
//
//  1. Identity.ImportID — the discoverer's import ID (the role name).
//  2. Identity.NameHint — the RoleName surfaced from CFN properties.
func iamRoleNameForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if s := strings.TrimSpace(id.ImportID); s != "" {
		return s
	}
	return strings.TrimSpace(id.NameHint)
}

// defaultIAMRoleFetch is the production fetch path: it wires the real
// *iam.Client into fetchIAMRoleWithClient.
func defaultIAMRoleFetch(ctx context.Context, c *iam.Client, roleName string) (*iamtypes.Role, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return fetchIAMRoleWithClient(ctx, c, roleName)
}

// fetchIAMRoleWithClient issues the single GetRole call behind the
// narrow iamRoleAPI interface so the orchestration is unit-testable.
func fetchIAMRoleWithClient(ctx context.Context, c iamRoleAPI, roleName string) (*iamtypes.Role, error) {
	out, err := c.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		return nil, fmt.Errorf("iam:GetRole: %w", err)
	}
	if out == nil || out.Role == nil {
		return nil, errors.New("iam:GetRole: nil role in response")
	}
	return out.Role, nil
}

// mapIAMRole projects an iamtypes.Role into the typed AWSIAMRole
// struct. Every field is emitted only when present so the resulting
// HCL stays decision-#34 clean. The trust policy is URL-decoded +
// JSON-compacted; a non-JSON trust policy is a hard error (the
// required `assume_role_policy` argument must hold valid JSON).
func mapIAMRole(role *iamtypes.Role) (*generated.AWSIAMRole, error) {
	typed := &generated.AWSIAMRole{}
	if role == nil {
		return typed, nil
	}
	if name := aws.ToString(role.RoleName); name != "" {
		typed.Name = generated.LiteralOf(name)
	}
	if path := aws.ToString(role.Path); path != "" {
		typed.Path = generated.LiteralOf(path)
	}
	if desc := aws.ToString(role.Description); desc != "" {
		typed.Description = generated.LiteralOf(desc)
	}
	if role.MaxSessionDuration != nil {
		typed.MaxSessionDuration = generated.LiteralOf(int64(*role.MaxSessionDuration))
	}
	if role.PermissionsBoundary != nil {
		if pb := aws.ToString(role.PermissionsBoundary.PermissionsBoundaryArn); pb != "" {
			typed.PermissionsBoundary = generated.LiteralOf(pb)
		}
	}
	if len(role.Tags) > 0 {
		m := map[string]*generated.Value[string]{}
		for _, t := range role.Tags {
			if t.Key != nil {
				m[*t.Key] = generated.LiteralOf(aws.ToString(t.Value))
			}
		}
		if len(m) > 0 {
			typed.Tags = m
		}
	}
	doc, err := decodeIAMPolicyDocument(aws.ToString(role.AssumeRolePolicyDocument))
	if err != nil {
		return nil, fmt.Errorf("decode assume_role_policy: %w", err)
	}
	if doc != "" {
		typed.AssumeRolePolicy = generated.LiteralOf(doc)
	}
	return typed, nil
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*iamRoleEnricher)(nil)
	_ ByIDEnricher      = (*iamRoleEnricher)(nil)
)
