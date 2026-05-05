package observability

import (
	"slices"
	"sort"
)

// AWSServiceActions maps each canonical AWS service key to its valid
// inspector actions. Source of truth ported from the InsideOut backend's
// awsServiceActions (internal/agentapi/inspect_normalize.go:77).
//
// Drift gates: AWSServiceActions is the registry against which
// The InsideOut backend's MCP dispatcher and the per-service summarizer validate
// caller input. Adding a new action here without wiring the
// corresponding handler in the InsideOut backend's per-service inspect_*.go is the
// classic "panel renders unsupported-action error" failure mode.
var AWSServiceActions = map[string][]string{
	"ec2":            {"describe-instances", "describe-vpcs", "describe-subnets", "describe-security-groups", "get-metrics"},
	"ebs":            {"describe-volumes", "describe-snapshots"},
	"rds":            {"describe-db-instances", "describe-db-clusters", "get-metrics"},
	"vpc":            {"describe-nat-gateways", "get-metrics"},
	"s3":             {"list-buckets", "get-metrics"},
	"kms":            {"list-keys", "list-aliases", "get-metrics"},
	"secretsmanager": {"list-secrets", "get-metrics"},
	"ecs":            {"list-clusters", "list-services", "describe-services", "get-metrics"},
	// list-nodes pivots EKS metric discovery from cluster-name to EC2
	// InstanceId via the AWS-managed `eks:cluster-name` tag, so the
	// observability panel queries AWS/EC2 CPUUtilization per node
	// instead of the unpopulated AWS/EKS namespace (#231 / Option A).
	"eks":            {"list-clusters", "describe-cluster", "list-nodes", "get-metrics"},
	"cloudfront":     {"list-distributions", "get-metrics"},
	"cloudwatchlogs": {"describe-log-groups", "get-metrics"},
	"alb":            {"describe-load-balancers", "get-metrics"},
	"waf":            {"list-web-acls", "get-metrics"},
	"elasticache":    {"describe-cache-clusters", "describe-replication-groups", "get-metrics"},
	"dynamodb":       {"list-tables", "get-metrics"},
	"sqs":            {"list-queues", "get-metrics"},
	"msk":            {"list-clusters", "get-metrics"},
	"cognito":        {"list-user-pools", "get-metrics"},
	"backup":         {"list-backup-vaults"},
	"lambda":         {"list-functions", "get-metrics"},
	"apigateway":     {"get-apis", "get-domain-names", "get-metrics"},
	"opensearch":     {"list-domains", "describe-domains", "list-collections", "get-metrics"},
	"bedrock":        {"list-knowledge-bases", "describe-knowledge-base", "list-agents", "list-guardrails", "get-metrics"},
	"cost-explorer":  {"get-cost-summary", "get-cost-forecast", "get-cost-by-tag"},
	"account":        {"get-info"},
}

// AWSServiceAliases maps caller-supplied aliases to canonical service
// keys. Aliases are NOT registered in AWSServiceActions and MUST NOT
// appear in docs or list-actions output — they only normalize input at
// the dispatch boundary. Source of truth ported from the InsideOut backend's
// awsServiceAliases (internal/agentapi/inspect_normalize.go:112).
var AWSServiceAliases = map[string]string{
	"elb":     "alb",
	"redis":   "elasticache",
	"nosql":   "dynamodb",
	"kafka":   "msk",
	"auth":    "cognito",
	"billing": "cost-explorer",
	"costs":   "cost-explorer",
}

// AWSActionAliases maps service → (alias action → canonical action).
// This absorbs common LLM-guessed action names that the upstream AWS
// SDK method names don't match. Resolved BEFORE the dispatch switch so
// the caller sees a successful result instead of an unsupported-action
// error. Source of truth ported from the InsideOut backend's awsActionAliases
// (internal/agentapi/inspect_normalize.go:140).
var AWSActionAliases = map[string]map[string]string{
	"account": {
		"get-account-info":    "get-info",
		"get-account-summary": "get-info",
		"describe-account":    "get-info",
	},
	"apigateway": {
		"list-apis": "get-apis",
	},
	"rds": {
		"list-db-instances": "describe-db-instances",
		"list-databases":    "describe-db-instances",
	},
	"lambda": {
		"describe-functions": "list-functions",
	},
	"s3": {
		"describe-buckets": "list-buckets",
	},
}

// GCPServiceActions is the GCP analog of AWSServiceActions. Source of
// truth ported from the InsideOut backend's gcpServiceActions
// (internal/agentapi/inspect_normalize.go:262).
var GCPServiceActions = map[string][]string{
	"compute":          {"list-instances", "describe-instance", "get-metrics"},
	"gke":              {"list-clusters", "describe-cluster"},
	"cloudrun":         {"list-services", "describe-service", "get-metrics"},
	"cloudsql":         {"list-instances", "describe-instance", "get-metrics"},
	"gcs":              {"list-buckets", "get-metrics"},
	"cloudkms":         {"list-keyrings", "list-keys", "get-metrics"},
	"secretmanager":    {"list-secrets", "get-metrics"},
	"pubsub":           {"list-topics", "list-subscriptions", "get-metrics"},
	"cloudlogging":     {"list-logs"},
	"loadbalancer":     {"list-backend-services", "list-url-maps", "list-target-http-proxies", "list-target-https-proxies", "list-forwarding-rules", "get-metrics"},
	"memorystore":      {"list-instances", "describe-instance", "get-metrics"},
	"cloudarmor":       {"list-policies", "describe-policy", "get-metrics"},
	"cloudbuild":       {"list-triggers", "list-builds", "get-metrics"},
	"identityplatform": {"list-tenants", "list-providers", "get-metrics"},
	"vertexai":         {"list-datasets", "list-endpoints", "list-models", "get-metrics"},
	"firestore":        {"list-collections", "describe-database", "get-metrics"},
	"vpc":              {"list-networks", "list-subnets", "list-firewalls", "list-routes", "get-metrics"},
	"cloudfunctions":   {"list-functions", "get-metrics"},
	"apigateway":       {"list-apis", "get-metrics"},
	"cloudcdn":         {"list-backend-services-cdn", "get-metrics"},
	"bastion":          {"list-bastion-instances", "get-metrics"},
	// Cloud Monitoring has no useful self-metric series; list-only by
	// design (cloudlogging follows the same pattern).
	"cloudmonitoring": {"list-alert-policies"},
	"billing":         {"get-billing-info", "get-budgets"},
}

// GCPServiceAliases is the GCP analog of AWSServiceAliases.
var GCPServiceAliases = map[string]string{
	"kms":       "cloudkms",
	"logging":   "cloudlogging",
	"lb":        "loadbalancer",
	"armor":     "cloudarmor",
	"network":   "vpc",
	"functions": "cloudfunctions",
	"cdn":       "cloudcdn",
}

// CanonicalAWSService resolves an alias to its canonical AWS service
// key. Unknown inputs are returned unchanged so downstream error paths
// can report the real caller input.
func CanonicalAWSService(s string) string {
	if c, ok := AWSServiceAliases[s]; ok {
		return c
	}
	return s
}

// CanonicalAWSAction resolves an aliased action to its canonical form
// for a given service. Unknown services and unknown actions are passed
// through unchanged.
func CanonicalAWSAction(service, action string) string {
	aliases, ok := AWSActionAliases[service]
	if !ok {
		return action
	}
	if c, ok := aliases[action]; ok {
		return c
	}
	return action
}

// CanonicalGCPService resolves an alias to its canonical GCP service
// key. Unknown inputs are returned unchanged.
func CanonicalGCPService(s string) string {
	if c, ok := GCPServiceAliases[s]; ok {
		return c
	}
	return s
}

// AWSServiceNames returns the sorted list of canonical AWS service keys.
func AWSServiceNames() []string {
	out := make([]string, 0, len(AWSServiceActions))
	for k := range AWSServiceActions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GCPServiceNames returns the sorted list of canonical GCP service keys.
func GCPServiceNames() []string {
	out := make([]string, 0, len(GCPServiceActions))
	for k := range GCPServiceActions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ServiceSupportsGetMetrics reports whether the given service registers
// "get-metrics" in its cloud's action registry. Used to gate the
// secondary metrics fetch so panels for services without
// CloudWatch/Cloud-Monitoring support render a clean "no metrics" state
// instead of a user-facing "unsupported action" error.
func ServiceSupportsGetMetrics(service string, isGCP bool) bool {
	registry := AWSServiceActions
	if isGCP {
		registry = GCPServiceActions
	}
	return slices.Contains(registry[service], "get-metrics")
}
