package composer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agext/levenshtein"
)

// ValidationError represents a client input validation failure (e.g., incompatible
// component combinations). Handlers use errors.As to distinguish these from
// internal errors and return HTTP 400 instead of 500.
type ValidationError struct {
	msg string
}

// NewValidationError creates a ValidationError with the given message.
func NewValidationError(msg string) *ValidationError {
	return &ValidationError{msg: msg}
}

func (e *ValidationError) Error() string { return e.msg }

// ValidationIssue describes one field-level input problem in a structured
// shape callers can use for same-turn AI correction or UI feedback.
type ValidationIssue struct {
	Field      string   `json:"field"`
	Value      string   `json:"value"`
	Allowed    []string `json:"allowed,omitempty"`
	Suggestion string   `json:"suggestion,omitempty"`
	Code       string   `json:"code"`
	Reason     string   `json:"reason"`
}

// Validate checks every known IR field in cfg/comps and returns all issues
// instead of short-circuiting on the first mapper error.
func Validate(comps *Components, cfg *Config) []ValidationIssue {
	reg, err := defaultValidationRegistry()
	if err != nil {
		return []ValidationIssue{{
			Field:  "composer.validation_registry",
			Code:   "internal_error",
			Reason: err.Error(),
		}}
	}

	var issues []ValidationIssue
	issues = append(issues, validateComponentFields(comps)...)
	for _, fv := range configFieldValidators {
		raw, ok := fv.value(cfg)
		if !ok {
			continue
		}

		normalized := raw
		if fv.normalize != nil {
			var normErr error
			normalized, normErr = fv.normalize(raw)
			if normErr != nil {
				issues = append(issues, fv.issue(raw, fv.codeForNormalizeError(), normErr.Error()))
				continue
			}
		}

		if fv.component == "" || fv.variable == "" {
			continue
		}
		if failure, ok := reg.validate(fv.component, fv.variable, normalized); !ok {
			issues = append(issues, fv.issue(raw, fv.codeForValidationFailure(failure), failure.reason))
		}
	}
	return issues
}

// AllowedValues returns the accepted values known for an IR field path.
// Closed sets are sourced from the corresponding module validation expression
// when possible, with IR-facing canonical hints layered on for translated fields.
func AllowedValues(field string) []string {
	fv, ok := validatorsByField[field]
	if !ok {
		if cv, ok := componentValidatorsByField[field]; ok {
			return cloneStrings(cv.allowed)
		}
		return nil
	}
	if len(fv.allowed) > 0 {
		return cloneStrings(fv.allowed)
	}
	reg, err := defaultValidationRegistry()
	if err != nil {
		return nil
	}
	return cloneStrings(reg.allowedValues(fv.component, fv.variable))
}

// KnownFields returns the dotted IR field paths covered by the validator.
//
// The returned order is deterministic for stable consumer contract-test
// output. Callers that only care about closed-enum drift should pair this with
// AllowedValues(field): fields with no allowed values may still be validated by
// regex, numeric range, or other module validation logic.
func KnownFields() []string {
	seen := make(map[string]bool, len(componentFieldValidators)+len(configFieldValidators))
	for _, cv := range componentFieldValidators {
		seen[cv.field] = true
	}
	for _, fv := range configFieldValidators {
		seen[fv.field] = true
	}

	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

type componentFieldValidator struct {
	field   string
	value   func(*Components) (string, bool)
	allowed []string
}

var componentFieldValidators = []componentFieldValidator{
	{
		field: "cloud",
		value: func(c *Components) (string, bool) {
			if c == nil || c.Cloud == "" {
				return "", false
			}
			return c.Cloud, true
		},
		allowed: []string{"AWS", "GCP"},
	},
	{
		field: "aws_vpc",
		value: func(c *Components) (string, bool) {
			if c == nil || c.AWSVPC == "" {
				return "", false
			}
			return c.AWSVPC, true
		},
		allowed: []string{"Private VPC", "Public VPC"},
	},
	{
		field: "aws_ec2",
		value: func(c *Components) (string, bool) {
			if c == nil || c.AWSEC2 == "" {
				return "", false
			}
			return c.AWSEC2, true
		},
		allowed: []string{"Intel", "ARM"},
	},
	{
		field: "gcp_compute",
		value: func(c *Components) (string, bool) {
			if c == nil || c.GCPCompute == "" {
				return "", false
			}
			return c.GCPCompute, true
		},
		allowed: []string{"Intel", "ARM"},
	},
	{
		field: "cpu_arch",
		value: func(c *Components) (string, bool) {
			if c == nil || c.CpuArch == "" {
				return "", false
			}
			return c.CpuArch, true
		},
		allowed: []string{"Intel", "ARM"},
	},
}

var componentValidatorsByField = func() map[string]componentFieldValidator {
	out := make(map[string]componentFieldValidator, len(componentFieldValidators))
	for _, v := range componentFieldValidators {
		out[v.field] = v
	}
	return out
}()

func validateComponentFields(comps *Components) []ValidationIssue {
	var issues []ValidationIssue
	for _, cv := range componentFieldValidators {
		value, ok := cv.value(comps)
		if !ok {
			continue
		}
		if stringInAllowedFold(value, cv.allowed) {
			continue
		}
		issues = append(issues, ValidationIssue{
			Field:      cv.field,
			Value:      value,
			Allowed:    cloneStrings(cv.allowed),
			Suggestion: nearestSuggestion(value, cv.allowed),
			Code:       "invalid_enum",
			Reason:     fmt.Sprintf("%s=%q: expected one of %s", cv.field, value, strings.Join(cv.allowed, ", ")),
		})
	}
	return issues
}

type configFieldValidator struct {
	field     string
	component ComponentKey
	variable  string
	value     func(*Config) (any, bool)
	normalize func(any) (any, error)
	allowed   []string
	code      string
}

func (fv configFieldValidator) issue(raw any, code, reason string) ValidationIssue {
	allowed := fv.allowed
	if len(allowed) == 0 {
		allowed = AllowedValues(fv.field)
	}
	return ValidationIssue{
		Field:      fv.field,
		Value:      issueValue(raw),
		Allowed:    cloneStrings(allowed),
		Suggestion: nearestSuggestion(issueValue(raw), allowed),
		Code:       code,
		Reason:     reason,
	}
}

func (fv configFieldValidator) codeForNormalizeError() string {
	if fv.code != "" {
		return fv.code
	}
	if len(fv.allowed) > 0 {
		return "invalid_enum"
	}
	return "unparseable_format"
}

func (fv configFieldValidator) codeForValidationFailure(f validationFailure) string {
	// A registry-side "invalid_value" failure on a field with a known
	// allowed-set is semantically an enum miss; surface that consistently
	// regardless of which path (normalize vs HCL eval) caught it.
	if f.code == "invalid_value" {
		if len(fv.allowed) > 0 || len(AllowedValues(fv.field)) > 0 {
			return "invalid_enum"
		}
	}
	if f.code != "" {
		return f.code
	}
	if len(fv.allowed) > 0 || len(AllowedValues(fv.field)) > 0 {
		return "invalid_enum"
	}
	return "invalid_value"
}

var configFieldValidators = []configFieldValidator{
	{
		field:     "aws_ec2.diskSizePerServer",
		component: KeyAWSEC2,
		variable:  "root_volume_size",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSEC2 == nil || c.AWSEC2.DiskSizePerServer == "" {
				return nil, false
			}
			return c.AWSEC2.DiskSizePerServer, true
		},
		normalize: normalizeStrictInt("AWSEC2.DiskSizePerServer"),
	},
	{
		// The module variable is bool, not a closed-set string. Membership is
		// enforced by normalizeEKSControlPlaneVisibility before HCL ever sees
		// the value; the exemption in TestConfigFieldValidatorsHaveModuleRulesOrExplicitExemption
		// matches.
		field:     "aws_eks.controlPlaneVisibility",
		component: KeyAWSEKS,
		variable:  "eks_public_control_plane",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSEKS == nil || c.AWSEKS.ControlPlaneVisibility == "" {
				return nil, false
			}
			return c.AWSEKS.ControlPlaneVisibility, true
		},
		normalize: normalizeEKSControlPlaneVisibility,
		allowed:   []string{"Public", "Private"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_eks.desiredSize",
		component: KeyAWSEKSNodeGroup,
		variable:  "desired_size",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSEKS == nil || c.AWSEKS.DesiredSize == "" {
				return nil, false
			}
			return c.AWSEKS.DesiredSize, true
		},
		normalize: normalizeStrictInt("AWSEKS.DesiredSize"),
	},
	{
		field:     "aws_eks.minSize",
		component: KeyAWSEKSNodeGroup,
		variable:  "min_size",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSEKS == nil || c.AWSEKS.MinSize == "" {
				return nil, false
			}
			return c.AWSEKS.MinSize, true
		},
		normalize: normalizeStrictInt("AWSEKS.MinSize"),
	},
	{
		field:     "aws_eks.maxSize",
		component: KeyAWSEKSNodeGroup,
		variable:  "max_size",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSEKS == nil || c.AWSEKS.MaxSize == "" {
				return nil, false
			}
			return c.AWSEKS.MaxSize, true
		},
		normalize: normalizeStrictInt("AWSEKS.MaxSize"),
	},
	{
		field:     "aws_ecs.capacityProviders",
		component: KeyAWSECS,
		variable:  "capacity_providers",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSECS == nil || len(c.AWSECS.CapacityProviders) == 0 {
				return nil, false
			}
			return c.AWSECS.CapacityProviders, true
		},
		normalize: normalizeECSCapacityProvidersValue,
		allowed:   []string{"FARGATE", "FARGATE_SPOT"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_ecs.defaultCapacityProvider",
		component: KeyAWSECS,
		variable:  "default_capacity_provider",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSECS == nil || c.AWSECS.DefaultCapacityProvider == "" {
				return nil, false
			}
			return c.AWSECS.DefaultCapacityProvider, true
		},
		normalize: normalizeECSCapacityProviderValue,
		allowed:   []string{"FARGATE", "FARGATE_SPOT"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_rds.cpuSize",
		component: KeyAWSRDS,
		variable:  "instance_class",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSRDS == nil || c.AWSRDS.CPUSize == "" {
				return nil, false
			}
			return c.AWSRDS.CPUSize, true
		},
		normalize: normalizeStringWith(canonicalRdsInstanceClass),
		allowed:   []string{"1 vCPU", "4 vCPU", "8 vCPU"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_rds.readReplicas",
		component: KeyAWSRDS,
		variable:  "read_replica_count",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSRDS == nil || c.AWSRDS.ReadReplicas == "" {
				return nil, false
			}
			return c.AWSRDS.ReadReplicas, true
		},
		normalize: normalizeLeadingInt("AWSRDS.ReadReplicas"),
	},
	{
		field:     "aws_rds.storageSize",
		component: KeyAWSRDS,
		variable:  "allocated_storage",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSRDS == nil || c.AWSRDS.StorageSize == "" {
				return nil, false
			}
			return c.AWSRDS.StorageSize, true
		},
		normalize: normalizeStorageGB("AWSRDS.StorageSize"),
	},
	{
		field:     "aws_elasticache.nodeSize",
		component: KeyAWSElastiCache,
		variable:  "node_type",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSElastiCache == nil || c.AWSElastiCache.NodeSize == "" {
				return nil, false
			}
			return c.AWSElastiCache.NodeSize, true
		},
		normalize: normalizeStringWith(canonicalRedisNodeType),
		allowed:   []string{"1 vCPU", "4 vCPU", "8 vCPU"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_elasticache.replicas",
		component: KeyAWSElastiCache,
		variable:  "replicas",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSElastiCache == nil || c.AWSElastiCache.Replicas == "" {
				return nil, false
			}
			return c.AWSElastiCache.Replicas, true
		},
		normalize: normalizeLeadingInt("AWSElastiCache.Replicas"),
	},
	{
		field:     "aws_dynamodb.type",
		component: KeyAWSDynamoDB,
		variable:  "billing_mode",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSDynamoDB == nil || c.AWSDynamoDB.Type == "" {
				return nil, false
			}
			return c.AWSDynamoDB.Type, true
		},
		normalize: normalizeStringWith(canonicalDdbBillingMode),
		allowed:   []string{"On demand", "provisioned", "PAY_PER_REQUEST", "PROVISIONED"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_sqs.type",
		component: KeyAWSSQS,
		variable:  "queue_type",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSSQS == nil || c.AWSSQS.Type == "" {
				return nil, false
			}
			return c.AWSSQS.Type, true
		},
		normalize: normalizeSQSQueueType,
		allowed:   []string{"Standard", "FIFO"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_sqs.visibilityTimeout",
		component: KeyAWSSQS,
		variable:  "visibility_timeout_seconds",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSSQS == nil || c.AWSSQS.VisibilityTimeout == "" {
				return nil, false
			}
			return c.AWSSQS.VisibilityTimeout, true
		},
		normalize: normalizeTTLSeconds("AWSSQS.VisibilityTimeout"),
	},
	{
		field:     "aws_msk.retentionPeriod",
		component: KeyAWSMSK,
		variable:  "retention_hours",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSMSK == nil || c.AWSMSK.Retention == "" {
				return nil, false
			}
			return c.AWSMSK.Retention, true
		},
		normalize: normalizeRetentionHours("AWSMSK.Retention"),
	},
	{
		field:     "aws_cloudwatch_logs.retentionDays",
		component: KeyAWSCloudWatchLogs,
		variable:  "retention_in_days",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSCloudWatchLogs == nil || c.AWSCloudWatchLogs.RetentionDays == 0 {
				return nil, false
			}
			return c.AWSCloudWatchLogs.RetentionDays, true
		},
	},
	{
		field:     "aws_cognito.signInType",
		component: KeyAWSCognito,
		variable:  "sign_in_type",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSCognito == nil || c.AWSCognito.SignInType == "" {
				return nil, false
			}
			return c.AWSCognito.SignInType, true
		},
		normalize: normalizeCognitoSignInType,
		allowed:   []string{"email", "username", "both"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_lambda.runtime",
		component: KeyAWSLambda,
		variable:  "runtime",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSLambda == nil || c.AWSLambda.Runtime == "" {
				return nil, false
			}
			return c.AWSLambda.Runtime, true
		},
	},
	{
		field:     "aws_lambda.memorySize",
		component: KeyAWSLambda,
		variable:  "memory_size",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSLambda == nil || c.AWSLambda.MemorySize == "" {
				return nil, false
			}
			return c.AWSLambda.MemorySize, true
		},
		normalize: normalizeStrictInt("AWSLambda.MemorySize"),
	},
	{
		field:     "aws_lambda.timeout",
		component: KeyAWSLambda,
		variable:  "timeout",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSLambda == nil || c.AWSLambda.Timeout == "" {
				return nil, false
			}
			return c.AWSLambda.Timeout, true
		},
		normalize: normalizeDurationSeconds("AWSLambda.Timeout"),
	},
	{
		field:     "aws_api_gateway.certificateArn",
		component: KeyAWSAPIGateway,
		variable:  "certificate_arn",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSAPIGateway == nil || c.AWSAPIGateway.CertificateArn == "" {
				return nil, false
			}
			return c.AWSAPIGateway.CertificateArn, true
		},
	},
	{
		field:     "aws_kms.numKeys",
		component: KeyAWSKMS,
		variable:  "num_keys",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSKMS == nil || c.AWSKMS.NumKeys == "" {
				return nil, false
			}
			return c.AWSKMS.NumKeys, true
		},
		normalize: normalizeStrictInt("AWSKMS.NumKeys"),
	},
	{
		field:     "aws_secretsmanager.numSecrets",
		component: KeyAWSSecretsManager,
		variable:  "num_secrets",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSSecretsManager == nil || c.AWSSecretsManager.NumSecrets == "" {
				return nil, false
			}
			return c.AWSSecretsManager.NumSecrets, true
		},
		normalize: normalizeStrictInt("AWSSecretsManager.NumSecrets"),
	},
	{
		field:     "aws_opensearch.deploymentType",
		component: KeyAWSOpenSearch,
		variable:  "deployment_type",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSOpenSearch == nil || c.AWSOpenSearch.DeploymentType == "" {
				return nil, false
			}
			return c.AWSOpenSearch.DeploymentType, true
		},
		normalize: normalizeOpenSearchDeploymentType,
		allowed:   []string{"managed", "serverless"},
		code:      "invalid_enum",
	},
	{
		field:     "aws_opensearch.storageSize",
		component: KeyAWSOpenSearch,
		variable:  "storage_size",
		value: func(c *Config) (any, bool) {
			if c == nil || c.AWSOpenSearch == nil || c.AWSOpenSearch.StorageSize == "" {
				return nil, false
			}
			return c.AWSOpenSearch.StorageSize, true
		},
		normalize: func(v any) (any, error) {
			s, err := requireString(v, "AWSOpenSearch.StorageSize")
			if err != nil {
				return nil, err
			}
			return normalizeStorageSizeGBString(s, "AWSOpenSearch.StorageSize")
		},
	},
	{
		field:     "gcp_gke.nodeCount",
		component: KeyGCPGKE,
		variable:  "node_count",
		value: func(c *Config) (any, bool) {
			if c == nil || c.GCPGKE == nil || c.GCPGKE.NodeCount == "" {
				return nil, false
			}
			return c.GCPGKE.NodeCount, true
		},
		normalize: normalizeStrictInt("GCPGKE.NodeCount"),
	},
	{
		field:     "gcp_memorystore.tier",
		component: KeyGCPMemorystore,
		variable:  "tier",
		value: func(c *Config) (any, bool) {
			if c == nil || c.GCPMemorystore == nil || c.GCPMemorystore.Tier == "" {
				return nil, false
			}
			return c.GCPMemorystore.Tier, true
		},
		normalize: normalizeGCPMemorystoreTier,
		allowed:   []string{"BASIC", "STANDARD_HA"},
		code:      "invalid_enum",
	},
	{
		field:     "gcp_gcs.storageClass",
		component: KeyGCPGCS,
		variable:  "storage_class",
		value: func(c *Config) (any, bool) {
			if c == nil || c.GCPGCS == nil || c.GCPGCS.StorageClass == "" {
				return nil, false
			}
			return c.GCPGCS.StorageClass, true
		},
		normalize: normalizeGCPStorageClass,
		allowed:   []string{"STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE"},
		code:      "invalid_enum",
	},
	{
		field:     "gcp_pubsub.messageRetentionDuration",
		component: KeyGCPPubSub,
		variable:  "message_retention_duration",
		value: func(c *Config) (any, bool) {
			if c == nil || c.GCPPubSub == nil || c.GCPPubSub.MessageRetentionDuration == "" {
				return nil, false
			}
			return c.GCPPubSub.MessageRetentionDuration, true
		},
	},
	{
		field:     "gcp_cloud_run.memory",
		component: KeyGCPCloudRun,
		variable:  "memory",
		value: func(c *Config) (any, bool) {
			if c == nil || c.GCPCloudRun == nil || c.GCPCloudRun.Memory == "" {
				return nil, false
			}
			return c.GCPCloudRun.Memory, true
		},
	},
	{
		field:     "gcp_cloud_run.cpu",
		component: KeyGCPCloudRun,
		variable:  "cpu",
		value: func(c *Config) (any, bool) {
			if c == nil || c.GCPCloudRun == nil || c.GCPCloudRun.CPU == "" {
				return nil, false
			}
			return c.GCPCloudRun.CPU, true
		},
	},
	{
		field:     "gcp_cloud_functions.runtime",
		component: KeyGCPCloudFunctions,
		variable:  "runtime",
		value: func(c *Config) (any, bool) {
			if c == nil || c.GCPCloudFunctions == nil || c.GCPCloudFunctions.Runtime == "" {
				return nil, false
			}
			return c.GCPCloudFunctions.Runtime, true
		},
	},
	{
		field:     "gcp_cloud_cdn.defaultTtl",
		component: KeyGCPCloudCDN,
		variable:  "default_ttl",
		value: func(c *Config) (any, bool) {
			if c == nil || c.GCPCloudCDN == nil || c.GCPCloudCDN.DefaultTtl == "" {
				return nil, false
			}
			return c.GCPCloudCDN.DefaultTtl, true
		},
		normalize: normalizeTTLSeconds("GCPCloudCDN.DefaultTtl"),
	},
}

var validatorsByField = func() map[string]configFieldValidator {
	out := make(map[string]configFieldValidator, len(configFieldValidators))
	for _, v := range configFieldValidators {
		out[v.field] = v
	}
	return out
}()

func issueValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func nearestSuggestion(value string, allowed []string) string {
	if len(allowed) == 0 || value == "" {
		return ""
	}
	needle := strings.ToLower(strings.TrimSpace(value))
	best := ""
	bestDistance := 4
	for _, candidate := range allowed {
		d := levenshtein.Distance(needle, strings.ToLower(candidate), nil)
		if d < bestDistance {
			bestDistance = d
			best = candidate
		}
	}
	return best
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func stringInAllowedFold(value string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), a) {
			return true
		}
	}
	return false
}

// ValidateComputeExclusivity checks that the selected component keys do not
// contain incompatible compute combinations. For example, Lambda (serverless)
// and EKS (container orchestration) cannot coexist in the same stack.
//
// Returns a descriptive error listing the conflicting keys, or nil if valid.
func ValidateComputeExclusivity(keys []ComponentKey) error {
	set := make(map[ComponentKey]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}

	// AWS serverless keys
	awsServerless := filterPresent(set,
		KeyAWSLambda,
	)
	// AWS container/VM keys
	awsContainer := filterPresent(set,
		KeyAWSEKSControlPlane, KeyAWSEKS, KeyAWSECS,
		KeyAWSEKSNodeGroup, KeyAWSEC2,
	)

	if len(awsServerless) > 0 && len(awsContainer) > 0 {
		return &ValidationError{msg: fmt.Sprintf(
			"incompatible AWS compute components: serverless [%s] cannot be combined with container/VM compute [%s] — choose either a serverless (Lambda) or container (EKS/ECS) architecture",
			joinKeys(awsServerless), joinKeys(awsContainer),
		)}
	}

	// GCP serverless keys
	gcpServerless := filterPresent(set,
		KeyGCPCloudFunctions,
		KeyGCPCloudRun,
	)
	// GCP container keys
	gcpContainer := filterPresent(set,
		KeyGCPGKE,
	)

	if len(gcpServerless) > 0 && len(gcpContainer) > 0 {
		return &ValidationError{msg: fmt.Sprintf(
			"incompatible GCP compute components: serverless [%s] cannot be combined with container compute [%s] — choose either a serverless (Cloud Functions/Cloud Run) or container (GKE) architecture",
			joinKeys(gcpServerless), joinKeys(gcpContainer),
		)}
	}

	return nil
}

// filterPresent returns the subset of candidates that exist in the set.
func filterPresent(set map[ComponentKey]bool, candidates ...ComponentKey) []ComponentKey {
	var found []ComponentKey
	for _, k := range candidates {
		if set[k] {
			found = append(found, k)
		}
	}
	return found
}

// ValidateRemovals checks whether removing the given components would break
// dependencies of the remaining components. Returns a descriptive error for
// each problematic removal, or nil if all removals are safe.
//
// Both removed and remaining should use cloud-prefixed keys (aws_*, gcp_*).
func ValidateRemovals(removed, remaining []ComponentKey) []RemovalWarning {
	if len(removed) == 0 {
		return nil
	}

	// Build reverse dependency map: "aws_vpc" → ["aws_alb", "aws_rds", ...]
	reverse := make(map[ComponentKey][]ComponentKey)
	for consumer, deps := range ImplicitDependencies {
		for _, dep := range deps {
			reverse[dep] = append(reverse[dep], consumer)
		}
	}

	remainSet := make(map[ComponentKey]bool, len(remaining))
	for _, k := range remaining {
		remainSet[k] = true
	}

	var warnings []RemovalWarning
	for _, r := range removed {
		dependents := reverse[r]
		var broken []ComponentKey
		for _, d := range dependents {
			if remainSet[d] {
				broken = append(broken, d)
			}
		}
		if len(broken) > 0 {
			warnings = append(warnings, RemovalWarning{
				Removed:    r,
				DependedBy: broken,
			})
		}
	}
	return warnings
}

// RemovalWarning describes a component removal that would break dependents.
type RemovalWarning struct {
	Removed    ComponentKey   `json:"removed"`
	DependedBy []ComponentKey `json:"depended_by"`
}

// FormatRemovalWarnings returns a human-readable string for a set of warnings.
func FormatRemovalWarnings(warnings []RemovalWarning) string {
	if len(warnings) == 0 {
		return ""
	}
	var parts []string
	for _, w := range warnings {
		deps := make([]string, len(w.DependedBy))
		for i, d := range w.DependedBy {
			deps[i] = string(d)
		}
		parts = append(parts, fmt.Sprintf(
			"cannot remove %s — still required by %s",
			string(w.Removed), strings.Join(deps, ", "),
		))
	}
	return strings.Join(parts, "; ")
}

func joinKeys(keys []ComponentKey) string {
	s := make([]string, len(keys))
	for i, k := range keys {
		s[i] = string(k)
	}
	return strings.Join(s, ", ")
}
