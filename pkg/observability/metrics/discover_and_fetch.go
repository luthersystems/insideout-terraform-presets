// discover_and_fetch.go
//
// DiscoverAndFetch is the in-process orchestrator that reproduces — byte
// for byte — the InsideOut backend's getServiceMetrics
// (internal/agentapi/aws_metrics.go). It is the entry point ui-core
// calls when its /observability/metrics endpoint receives an empty
// `resources` list: the caller hands over (service, filters) and this
// function performs the whole "discover the account's resources for the
// service, then fetch their CloudWatch series" pipeline that previously
// lived in reliable.
//
// The pieces this file owns are exactly the pieces reliable kept
// reliable-side after the metric-fetch core moved upstream:
//
//   - The orchestration branches (kms/secretsmanager health vs the
//     CloudWatch catalog; the #2035 resource-scoped fast path; the
//     discover-then-fetch tail).
//   - The KMS / Secrets Manager operational-health envelopes — these are
//     not CloudWatch time-series and the JSON shape is reliable-defined.
//   - The service-keyed view over upstream's observability.Observability
//     registry (metricDefinitions).
//
// The metric catalog (observability.AWSObs), the CloudWatch fetch
// (metrics.Fetch), and the per-service discovery (discovery/aws.Inspect,
// reached via runMetricsDiscovery in discover_and_fetch_discovery.go)
// already live in this repo — DiscoverAndFetch reuses them. The
// per-service ID extractors and the KMS/SM readers ported here keep
// their exact behavior so the wire result is identical to what reliable
// returned in-process.
package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// UnsupportedServiceMetricsError signals "this AWS service has no entry
// in our metric catalog yet" — a known UX gap, not an AWS operation
// failure. Surfaced by DiscoverAndFetch when a service is missing from
// metricDefinitions (e.g. CloudWatch Logs / `logs`). Reliable's call
// sites converted this into a typed empty-state envelope
// (`empty_reason=no_metrics_catalog`, `service=<svc>`) so the UI renders
// clean copy with the resource name instead of the misleading
// `aws_operation_failed: no metric definitions for service: <svc>`
// banner the user saw in reliable#1789. ui-core consumers should detect
// it via AsUnsupportedServiceMetricsError and emit the same envelope.
//
// Ported from reliable's unexported unsupportedServiceMetricsError; it is
// exported here because it crosses the repo boundary (ui-core needs to
// detect it).
type UnsupportedServiceMetricsError struct {
	Service string
}

func (e *UnsupportedServiceMetricsError) Error() string {
	return fmt.Sprintf("no metric definitions for service: %s", e.Service)
}

// AsUnsupportedServiceMetricsError extracts the sentinel from err. Because
// upstream `observability/inspect/dispatcher.go` wraps inner errors with
// `%v` (not `%w`) it breaks the error chain, so `errors.As` alone is not
// sufficient at the wire boundary — we also pattern-match the verbatim
// error string the dispatcher prefixes with `aws_operation_failed:`. The
// service name is captured from the trailing `service: <svc>` segment so
// the caller can populate `Service` on the response envelope without
// re-parsing.
//
// Returns (svc, true) when the error wraps an
// UnsupportedServiceMetricsError (direct call), or when the error's
// string matches the upstream-wrapped shape; (zero, false) otherwise.
// Ported verbatim from reliable's asUnsupportedServiceMetricsError.
func AsUnsupportedServiceMetricsError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var sentinel *UnsupportedServiceMetricsError
	if errors.As(err, &sentinel) {
		return sentinel.Service, true
	}
	// String-fallback: upstream wraps with `aws_operation_failed: %v`, so
	// the chain is broken but the message text is stable. Match on the
	// suffix so the upstream prefix (or any other wrap layer that uses
	// `%v`) doesn't defeat detection.
	const marker = "no metric definitions for service: "
	msg := err.Error()
	idx := strings.LastIndex(msg, marker)
	if idx < 0 {
		return "", false
	}
	svc := strings.TrimSpace(msg[idx+len(marker):])
	// Guard against trailing fragments — the upstream wrap doesn't add
	// any, but defend in depth so a future wrap that does (e.g.
	// `... (request id %s)`) cleanly truncates at the first whitespace
	// rather than capturing the trailing junk as the service name.
	if cut := strings.IndexAny(svc, " \t\r\n"); cut >= 0 {
		svc = svc[:cut]
	}
	if svc == "" {
		return "", false
	}
	return svc, true
}

// --- KMS/Secrets Manager operational health types ---
//
// JSON tags are identical to reliable's so consumers see the same field
// names + tag spellings as before the cross-repo move.

// KeyHealthResult holds operational health data for KMS keys.
type KeyHealthResult struct {
	Service string          `json:"service"`
	Note    string          `json:"note"`
	Keys    []KeyHealthInfo `json:"keys"`
}

// KeyHealthInfo holds health data for a single KMS key.
type KeyHealthInfo struct {
	KeyID           string `json:"key_id"`
	Alias           string `json:"alias,omitempty"`
	KeyState        string `json:"key_state"`
	KeyManager      string `json:"key_manager"`
	RotationEnabled bool   `json:"rotation_enabled"`
	CreationDate    string `json:"creation_date,omitempty"`
	Description     string `json:"description,omitempty"`
}

// SecretHealthResult holds operational health data for Secrets Manager secrets.
type SecretHealthResult struct {
	Service string             `json:"service"`
	Note    string             `json:"note"`
	Secrets []SecretHealthInfo `json:"secrets"`
}

// SecretHealthInfo holds health data for a single secret.
type SecretHealthInfo struct {
	Name             string `json:"name"`
	ARN              string `json:"arn,omitempty"`
	RotationEnabled  bool   `json:"rotation_enabled"`
	LastRotatedDate  string `json:"last_rotated_date,omitempty"`
	LastAccessedDate string `json:"last_accessed_date,omitempty"`
	NextRotationDate string `json:"next_rotation_date,omitempty"`
	VersionCount     int    `json:"version_count"`
	CreatedDate      string `json:"created_date,omitempty"`
}

func buildSecretsManagerListInput(project string) *secretsmanager.ListSecretsInput {
	input := &secretsmanager.ListSecretsInput{}
	if project != "" {
		input.Filters = []smtypes.Filter{
			{Key: smtypes.FilterNameStringTypeTagValue, Values: []string{project}},
		}
	}
	return input
}

// --- Metric Definitions ---

// metricDefinitions maps service names to upstream's AWSObs spec.
// Reliable kept a service-keyed view because observability.Observability
// is keyed by composer.ComponentKey; the inspector dispatch path joins on
// the service tag (obs.Service). The values are pointers into upstream's
// catalog — single source of truth, no copy. Ported from reliable's
// metricDefinitions (aws_metrics.go).
var metricDefinitions = func() map[string]*observability.AWSObs {
	out := make(map[string]*observability.AWSObs)
	for _, obs := range observability.Observability {
		if obs.AWS == nil || obs.Service == "" {
			continue
		}
		if _, seen := out[obs.Service]; seen {
			continue
		}
		out[obs.Service] = obs.AWS
	}
	return out
}()

// --- Main Entry Point ---

// DiscoverAndFetch retrieves CloudWatch metrics (or operational health
// data) for a service, discovering the account's resources first.
// Reproduces reliable's getServiceMetrics (aws_metrics.go) behavior
// exactly: discovery is delegated to runMetricsDiscovery (see
// discover_and_fetch_discovery.go); the metric fetch itself is delegated
// to Fetch. KMS and SecretsManager return operational-health envelopes,
// not CloudWatch time-series.
//
// The return value is `any` to mirror reliable: the CloudWatch path holds
// a MetricsResult (value), the kms path a *KeyHealthResult, the
// secretsmanager path a *SecretHealthResult.
//
// Resource-scoped fast path (reliable#2035): when the filter already
// names the service's CloudWatch dimension value (e.g.
// `{"BucketName":"<bucket>"}` for s3, keyed on obs.DimensionName), the
// resource is already resolved — we query that dimension directly and
// SKIP account-wide discovery entirely. This is the imported-resource
// path: an imported S3 bucket lives in the user's pre-existing account
// and is NOT tagged with our session's project, so the project-tag-scoped
// runMetricsDiscovery would return zero buckets and the panel would
// render "no recent samples yet". The managed/designed path passes a
// project filter (no resource-dimension key), so it keeps going through
// discovery unchanged.
func DiscoverAndFetch(ctx context.Context, cfg aws.Config, service, filters string) (any, error) {
	project := filter.Project(filters)

	switch service {
	case "kms":
		return getKMSKeyHealth(ctx, cfg, project)
	case "secretsmanager":
		return getSecretHealth(ctx, cfg, project)
	}

	obs, ok := metricDefinitions[service]
	if !ok {
		// Typed sentinel — caller converts to a `no_metrics_catalog`
		// empty-state envelope so the UI renders clean copy with the
		// resource name (reliable#1789) instead of the misleading
		// `aws_operation_failed: ...` banner.
		return nil, &UnsupportedServiceMetricsError{Service: service}
	}

	// Resource-scoped fast path: the dimension value is already in the
	// filter (imported get-metrics, reliable#2035). Skip discovery.
	if dim := resourceDimensionFromFilter(filters, obs.DimensionName); dim != "" {
		return fetchServiceMetricsForResources(ctx, cfg, service, obs, []string{dim}, filters)
	}

	resourceIDs, err := runMetricsDiscovery(ctx, cfg, service, project)
	if err != nil {
		return nil, fmt.Errorf("auto-discover failed for %s: %w", service, err)
	}
	return fetchServiceMetricsForResources(ctx, cfg, service, obs, resourceIDs, filters)
}

// clientsFromConfigForFetch is the seam fetchServiceMetricsForResourcesImpl
// uses to build a *Clients. Defaults to NewClientsFromConfig; tests swap
// it so the fetch tail runs against a mocked CloudWatch client instead of
// constructing a real SDK client from cfg.
var clientsFromConfigForFetch = NewClientsFromConfig

// fetchServiceMetricsForResources is the shared metric-fetch tail: it
// wraps the given dimension values as ResourceID (all carrying
// obs.DimensionName) and delegates the CloudWatch query to Fetch, which
// applies the per-service overrides (S3 daily period + StorageType dims,
// CloudFront us-east-1 routing). Split out so both the discovery path and
// the resource-scoped fast path (reliable#2035) share identical fetch
// semantics.
//
// Declared as a seam var so tests can assert which dimension values the
// resource-scoped fast path forwards, without a real CloudWatch call.
// Ported from reliable's fetchServiceMetricsForResources.
var fetchServiceMetricsForResources = fetchServiceMetricsForResourcesImpl

func fetchServiceMetricsForResourcesImpl(ctx context.Context, cfg aws.Config, service string, obs *observability.AWSObs, dimensionValues []string, filters string) (any, error) {
	resources := make([]ResourceID, 0, len(dimensionValues))
	for _, id := range dimensionValues {
		resources = append(resources, ResourceID{ID: id, DimensionName: obs.DimensionName})
	}

	clients, err := clientsFromConfigForFetch(cfg)
	if err != nil {
		return nil, fmt.Errorf("metrics clients: %w", err)
	}

	mf := ParseMetricsFilter(filters)
	// presets#778: Fetch takes the full namespace group list; this path
	// always queries a single component namespace.
	return Fetch(ctx, clients, service, []*observability.AWSObs{obs}, resources, mf)
}

// resourceDimensionFromFilter returns the resource's CloudWatch
// dimension value when the filter explicitly names it under dimensionName
// (e.g. `{"BucketName":"my-bucket"}` for s3) — the shape the imported
// get-metrics path produces from a binding's DimensionKey (reliable#2035).
// Returns "" when the key is absent, empty, non-string, or the filter is
// not a JSON object, in which case the caller falls back to account-wide
// discovery. dimensionName=="" (a service with no dimension) never
// matches. Ported verbatim from reliable.
func resourceDimensionFromFilter(filters, dimensionName string) string {
	if filters == "" || dimensionName == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(filters), &m); err != nil {
		return ""
	}
	if v, ok := m[dimensionName].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// --- KMS Key Health ---

// kmsHealthAPI is the subset of the KMS client getKMSKeyHealth invokes.
// Narrowed to a seam so tests inject a fake without standing up the SDK
// client (which would try real AWS auth). *kms.Client satisfies it.
type kmsHealthAPI interface {
	ListKeys(ctx context.Context, params *kms.ListKeysInput, optFns ...func(*kms.Options)) (*kms.ListKeysOutput, error)
	ListAliases(ctx context.Context, params *kms.ListAliasesInput, optFns ...func(*kms.Options)) (*kms.ListAliasesOutput, error)
	DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	ListResourceTags(ctx context.Context, params *kms.ListResourceTagsInput, optFns ...func(*kms.Options)) (*kms.ListResourceTagsOutput, error)
	GetKeyRotationStatus(ctx context.Context, params *kms.GetKeyRotationStatusInput, optFns ...func(*kms.Options)) (*kms.GetKeyRotationStatusOutput, error)
}

// newKMSClient is the seam getKMSKeyHealth uses to obtain its client.
var newKMSClient = func(cfg aws.Config) kmsHealthAPI { return kms.NewFromConfig(cfg) }

// getKMSKeyHealth reports per-key health (rotation, state, manager, age)
// for KMS keys scoped to the caller's project. When project=="" (demo
// sessions) every key is reported — including AWS-managed keys that we
// cannot tag. When project!="" only customer-managed keys tagged
// Project=<project> survive; AWS-managed keys are filtered out because
// they cannot carry our tags. Ported verbatim from reliable's
// getKMSKeyHealth (reliable#1112 fail-closed behavior preserved).
func getKMSKeyHealth(ctx context.Context, cfg aws.Config, project string) (*KeyHealthResult, error) {
	kmsClient := newKMSClient(cfg)

	listOut, err := kmsClient.ListKeys(ctx, &kms.ListKeysInput{})
	if err != nil {
		return nil, fmt.Errorf("kms ListKeys: %w", err)
	}

	aliasOut, err := kmsClient.ListAliases(ctx, &kms.ListAliasesInput{})
	if err != nil {
		return nil, fmt.Errorf("kms ListAliases: %w", err)
	}
	aliasMap := make(map[string]string)
	for _, a := range aliasOut.Aliases {
		if a.TargetKeyId != nil {
			aliasMap[aws.ToString(a.TargetKeyId)] = aws.ToString(a.AliasName)
		}
	}

	var keys []KeyHealthInfo
	for _, key := range listOut.Keys {
		keyID := aws.ToString(key.KeyId)

		descOut, descErr := kmsClient.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: key.KeyId})
		if descErr != nil {
			log.Printf("[kms DescribeKey] skip key=%s: %v", keyID, descErr)
			continue
		}
		meta := descOut.KeyMetadata
		if meta == nil {
			continue
		}

		// Project-tag gate. Customer-managed keys check their tags;
		// aws-managed keys short-circuit to "not ours" — they can't
		// carry our Project tag. For demo sessions (project==""), keep
		// everything including aws-managed keys for visibility.
		if project != "" {
			if meta.KeyManager != kmstypes.KeyManagerTypeCustomer {
				continue
			}
			tagsOut, tagErr := kmsClient.ListResourceTags(ctx, &kms.ListResourceTagsInput{KeyId: key.KeyId})
			if tagErr != nil {
				log.Printf("[kms ListResourceTags] skip key=%s: %v", keyID, tagErr)
				continue
			}
			if !hasProjectTagKMS(tagsOut.Tags, project) {
				continue
			}
		}

		info := KeyHealthInfo{
			KeyID:       keyID,
			Alias:       aliasMap[keyID],
			KeyState:    string(meta.KeyState),
			KeyManager:  string(meta.KeyManager),
			Description: aws.ToString(meta.Description),
		}
		if meta.CreationDate != nil {
			info.CreationDate = meta.CreationDate.Format(time.RFC3339)
		}
		if meta.KeyManager == kmstypes.KeyManagerTypeCustomer {
			rotOut, rotErr := kmsClient.GetKeyRotationStatus(ctx, &kms.GetKeyRotationStatusInput{KeyId: key.KeyId})
			if rotErr == nil {
				info.RotationEnabled = rotOut.KeyRotationEnabled
			}
		}
		keys = append(keys, info)
	}

	return &KeyHealthResult{
		Service: "kms",
		Note:    "Key health: rotation status, key state, creation date. Use list-keys/list-aliases for full metadata.",
		Keys:    keys,
	}, nil
}

// hasProjectTagKMS checks a KMS tag slice for Project=<project>. KMS is
// the outlier — the SDK field names are TagKey/TagValue, not the Key/Value
// pattern every other service uses.
func hasProjectTagKMS(tags []kmstypes.Tag, project string) bool {
	for _, t := range tags {
		if aws.ToString(t.TagKey) == "Project" && aws.ToString(t.TagValue) == project {
			return true
		}
	}
	return false
}

// --- Secrets Manager Secret Health ---

// smHealthAPI is the subset of the Secrets Manager client getSecretHealth
// invokes. Seam for test injection; *secretsmanager.Client satisfies it.
type smHealthAPI interface {
	ListSecrets(ctx context.Context, params *secretsmanager.ListSecretsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
	ListSecretVersionIds(ctx context.Context, params *secretsmanager.ListSecretVersionIdsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretVersionIdsOutput, error)
}

// newSecretsManagerClient is the seam getSecretHealth uses to obtain its
// client.
var newSecretsManagerClient = func(cfg aws.Config) smHealthAPI { return secretsmanager.NewFromConfig(cfg) }

// getSecretHealth reports per-secret operational health scoped to the
// caller's project. Ported verbatim from reliable's getSecretHealth.
func getSecretHealth(ctx context.Context, cfg aws.Config, project string) (*SecretHealthResult, error) {
	smClient := newSecretsManagerClient(cfg)

	listOut, err := smClient.ListSecrets(ctx, buildSecretsManagerListInput(project))
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}

	var secrets []SecretHealthInfo
	for _, s := range listOut.SecretList {
		info := SecretHealthInfo{
			Name:            aws.ToString(s.Name),
			ARN:             aws.ToString(s.ARN),
			RotationEnabled: aws.ToBool(s.RotationEnabled),
		}
		if s.LastRotatedDate != nil {
			info.LastRotatedDate = s.LastRotatedDate.Format(time.RFC3339)
		}
		if s.LastAccessedDate != nil {
			info.LastAccessedDate = s.LastAccessedDate.Format(time.RFC3339)
		}
		if s.NextRotationDate != nil {
			info.NextRotationDate = s.NextRotationDate.Format(time.RFC3339)
		}
		if s.CreatedDate != nil {
			info.CreatedDate = s.CreatedDate.Format(time.RFC3339)
		}

		// Get version count
		versOut, versErr := smClient.ListSecretVersionIds(ctx, &secretsmanager.ListSecretVersionIdsInput{
			SecretId: s.ARN,
		})
		if versErr == nil && versOut.Versions != nil {
			info.VersionCount = len(versOut.Versions)
		}

		secrets = append(secrets, info)
	}

	return &SecretHealthResult{
		Service: "secretsmanager",
		Note:    "Secret health: rotation status, last accessed/rotated dates. Use list-secrets for full metadata.",
		Secrets: secrets,
	}, nil
}
