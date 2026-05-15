package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// secretsmanagerSecretTFType is the registered Terraform type for the
// Secrets Manager Secret enricher. Kept as a constant so the registry /
// ResourceType() stay in lockstep.
const secretsmanagerSecretTFType = "aws_secretsmanager_secret"

// secretsmanagerSecretEnricher implements AttributeEnricher and
// ByIDEnricher for aws_secretsmanager_secret. Pairs with the Cloud-
// Control-routed (or sdkonly) secrets_manager discoverer.
//
// Unlike DynamoDB — where TF surface aggregates four SDK calls — a
// single DescribeSecret returns every field the Layer 1 typed model
// needs: top-level attributes (Name, Description, KmsKeyId), tags
// (inline Tags []Tag), and replicas (inline ReplicationStatus []*).
// One fetch hook is therefore enough; no soft-fail overlay is needed.
//
// **Computed-only / TF-input-only fields skipped per decision #5:**
//   - `arn` (Computed) — stamped on ir.Identity.NativeIDs["arn"]
//     instead, matching the DynamoDB TableArn pattern.
//   - `id` (Optional+Computed alias for ARN).
//   - `tags_all` (Computed; provider merges defaults at plan time).
//   - `force_overwrite_replica_secret` (TF-input only; no API source).
//   - `name_prefix` (TF-input only; provider derives Name from it).
//   - `recovery_window_in_days` (TF-input only; delete-time parameter
//     consumed by DeleteSecret, with no Describe-side reflection).
//   - `policy` (no field on DescribeSecretOutput; the resource policy
//     lives behind a separate GetResourcePolicy call that this
//     bundle's single-call contract does not include).
//
// Sensitive fields: the secret *value* lives behind GetSecretValue,
// which we never call — only metadata is enriched. Decision #36
// redaction is downstream's concern.
type secretsmanagerSecretEnricher struct {
	// fetch is overridable for tests. Defaults to a real DescribeSecret
	// call against the secretsmanager.Client in EnrichClients. Tests
	// inject a fake by constructing the enricher with a custom fetch —
	// keeps the enricher hermetically testable without spinning up an
	// HTTP server for the SDK client.
	fetch func(ctx context.Context, c *secretsmanager.Client, secretID string) (*secretsmanager.DescribeSecretOutput, error)
}

// newSecretsManagerSecretEnricher returns the production-wired enricher.
// AWSDiscoverer's byTypeEnricher map registers this under
// "aws_secretsmanager_secret".
func newSecretsManagerSecretEnricher() AttributeEnricher {
	return &secretsmanagerSecretEnricher{fetch: defaultSecretsManagerSecretFetch}
}

func (secretsmanagerSecretEnricher) ResourceType() string { return secretsmanagerSecretTFType }

// Enrich populates ir.Attrs with a typed AWSSecretsmanagerSecret
// payload for the secret identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.SecretsManager is nil;
// any other error reflects a real Secrets Manager API failure on the
// load-bearing DescribeSecret call.
func (e secretsmanagerSecretEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.SecretsManager == nil {
		return ErrEnrichClientUnavailable
	}
	secretID := secretsmanagerSecretIDForEnrich(&ir.Identity)
	if secretID == "" {
		return fmt.Errorf("secretsmanager_secret: cannot derive secret id from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	out, err := e.fetch(ctx, c.SecretsManager, secretID)
	if err != nil {
		// Map typed not-found onto ErrNotFound so dispatchers / drift
		// flows can distinguish a deleted secret from a real API
		// failure. Mirrors the cloudcontrol_discoverer.go pattern.
		var notFound *smtypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return fmt.Errorf("secretsmanager_secret %q: %w", secretID, ErrNotFound)
		}
		return fmt.Errorf("secretsmanager_secret: describe %q: %w", secretID, err)
	}
	if out == nil {
		return fmt.Errorf("secretsmanager_secret: describe %q: empty response", secretID)
	}

	// Stamp ARN on Identity.NativeIDs so downstream consumers don't
	// have to round-trip back to the SDK for the ARN. The pure-
	// mapping helper does NOT touch ir.Identity per the
	// AttributeEnricher contract; this is the only place the enricher
	// writes to it.
	if arn := aws.ToString(out.ARN); arn != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["arn"] = arn
	}

	typed := mapSecretsmanagerSecret(out)

	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("secretsmanager_secret: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSSecretsmanagerSecret payload for the
// secret named by identity and returns it as the json.RawMessage shape
// that would land in ImportedResource.Attrs. Shares the SDK call +
// mapping with Enrich via the private mapSecretsmanagerSecret helper so
// the two paths cannot drift out of sync.
//
// EnrichByID does not mutate identity (callers passing a pointer get
// the same struct back unchanged) — the ARN that Enrich stamps onto
// NativeIDs is intentionally NOT stamped here, since the per-IR drift
// refresh path expects the identity to be authoritative input, not a
// destination.
func (e secretsmanagerSecretEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.SecretsManager == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, fmt.Errorf("secretsmanager_secret: nil identity")
	}
	secretID := secretsmanagerSecretIDForEnrich(identity)
	if secretID == "" {
		return nil, fmt.Errorf("secretsmanager_secret: cannot derive secret id from Identity (Address=%q ImportID=%q NameHint=%q)",
			identity.Address, identity.ImportID, identity.NameHint)
	}
	out, err := e.fetch(ctx, c.SecretsManager, secretID)
	if err != nil {
		var notFound *smtypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("secretsmanager_secret %q: %w", secretID, ErrNotFound)
		}
		return nil, fmt.Errorf("secretsmanager_secret: describe %q: %w", secretID, err)
	}
	if out == nil {
		return nil, fmt.Errorf("secretsmanager_secret: describe %q: empty response", secretID)
	}
	typed := mapSecretsmanagerSecret(out)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("secretsmanager_secret: marshal Attrs: %w", err)
	}
	return raw, nil
}

// secretsmanagerSecretIDForEnrich derives the SecretId argument for
// DescribeSecret. Secrets Manager's DescribeSecret accepts either the
// secret's bare name or its full ARN, so the order of preference
// favors the most-fully-qualified identifier the discoverer is likely
// to have populated:
//
//  1. Identity.ImportID — the per-service discoverer's
//     passthroughImportID emits the secret ARN as the Identifier for
//     Secrets Manager, so this is the load-bearing source.
//  2. Identity.NameHint — explicit secret name set by the discoverer
//     (or by a caller refreshing a single row via EnrichByID).
//  3. Identity.NativeIDs["name"] — last-resort fallback if a future
//     config populates the NativeIDs bag instead.
//
// Distinct from dynamodb_table's NameHint-first ordering because
// DescribeSecret can take an ARN directly and the discoverer's
// ImportID is the ARN — preferring ImportID avoids an unnecessary
// fall-through.
func secretsmanagerSecretIDForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if s := strings.TrimSpace(id.ImportID); s != "" {
		return s
	}
	if s := strings.TrimSpace(id.NameHint); s != "" {
		return s
	}
	return strings.TrimSpace(id.NativeIDs["name"])
}

// defaultSecretsManagerSecretFetch is the production fetch path: a
// single DescribeSecret call.
func defaultSecretsManagerSecretFetch(ctx context.Context, c *secretsmanager.Client, secretID string) (*secretsmanager.DescribeSecretOutput, error) {
	out, err := c.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: aws.String(secretID)})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("describe secret: nil output")
	}
	return out, nil
}

// mapSecretsmanagerSecret is the pure-mapping helper shared by Enrich
// and EnrichByID. Hand-rolled (no enrichgen) because the Layer 1 typed
// surface and the SDK DescribeSecretOutput shape are stable and the
// special cases (skip-default-KMS-key, replication-block lift) are
// short enough to inline. Mirrors the dynamodb_table_enrich.gen.go
// shape so future readers can pattern-match.
//
// Decision-#34 cleanliness: every field is emitted only when present
// on the API response, so the resulting HCL does not contain
// "field = null" noise.
func mapSecretsmanagerSecret(out *secretsmanager.DescribeSecretOutput) *generated.AWSSecretsmanagerSecret {
	typed := &generated.AWSSecretsmanagerSecret{}

	if name := aws.ToString(out.Name); name != "" {
		typed.Name = generated.LiteralOf(name)
	}
	if desc := aws.ToString(out.Description); desc != "" {
		typed.Description = generated.LiteralOf(desc)
	}
	// Skip the AWS-owned default key. The Secrets Manager API returns
	// `KmsKeyId == ""` when the secret uses the AWS-managed default
	// key (`alias/aws/secretsmanager`); emitting that as an explicit
	// literal would diff against TF state where the field is left
	// unset. Matches the DynamoDB SSE "absent when default" guard.
	if k := aws.ToString(out.KmsKeyId); k != "" {
		typed.KMSKeyID = generated.LiteralOf(k)
	}

	// Tags — DescribeSecretOutput already carries them inline, so no
	// overlay call is needed.
	if len(out.Tags) > 0 {
		m := map[string]*generated.Value[string]{}
		for _, t := range out.Tags {
			if t.Key != nil {
				m[*t.Key] = generated.LiteralOf(aws.ToString(t.Value))
			}
		}
		if len(m) > 0 {
			typed.Tags = m
		}
	}

	// Replica blocks come from ReplicationStatus. The TF schema's
	// `replica` block is Optional in input but each entry's fields
	// here are downstream of the actual replication state, so the
	// block list reflects observed state.
	if len(out.ReplicationStatus) > 0 {
		blocks := make([]generated.AWSSecretsmanagerSecretReplica, 0, len(out.ReplicationStatus))
		for i := range out.ReplicationStatus {
			blocks = append(blocks, enrichAWSSecretsmanagerSecretReplica(&out.ReplicationStatus[i]))
		}
		if len(blocks) > 0 {
			typed.Replica = blocks
		}
	}

	return typed
}

// enrichAWSSecretsmanagerSecretReplica lifts a single ReplicationStatus
// entry into a Layer 1 replica block. Like the parent helper, every
// field is emitted only when present so the resulting HCL stays
// decision-#34 clean.
func enrichAWSSecretsmanagerSecretReplica(r *smtypes.ReplicationStatusType) generated.AWSSecretsmanagerSecretReplica {
	out := generated.AWSSecretsmanagerSecretReplica{}
	if r == nil {
		return out
	}
	if region := aws.ToString(r.Region); region != "" {
		out.Region = generated.LiteralOf(region)
	}
	if k := aws.ToString(r.KmsKeyId); k != "" {
		out.KMSKeyID = generated.LiteralOf(k)
	}
	if r.LastAccessedDate != nil {
		// RFC3339 — matches what the AWS provider records in TF
		// state for *_date fields.
		out.LastAccessedDate = generated.LiteralOf(r.LastAccessedDate.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if s := string(r.Status); s != "" {
		out.Status = generated.LiteralOf(s)
	}
	if msg := aws.ToString(r.StatusMessage); msg != "" {
		out.StatusMessage = generated.LiteralOf(msg)
	}
	return out
}
