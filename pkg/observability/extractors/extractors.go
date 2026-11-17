// Package extractors normalizes per-component live-config envelopes
// returned by the cloud inspectors (the InsideOut backend's awsinspect / gcpinspect)
// into a flat map[string]string keyed by frontend-friendly camelCase
// fields. Each extractor handles one (componentKey, action) pair and
// returns nil when the envelope shape is unrecognized — callers fall
// back to design values in that case.
//
// Ported from the InsideOut backend internal/agentapi/config_extractors.go (#204).
// Inspector envelope stays `any` so callers don't need to coerce; a
// typed envelope shape is a follow-up.
package extractors

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// Extract dispatches to the per-service extractor for componentKey and
// returns the normalized config map. Returns nil when the key is not
// supported here (the frontend falls back to design values).
func Extract(componentKey composer.ComponentKey, rawResult any) map[string]string {
	switch string(componentKey) {
	case "aws_rds":
		return extractRDSConfig(rawResult)
	case "aws_ec2":
		return extractEC2Config(rawResult)
	case "aws_elasticache":
		return extractElastiCacheConfig(rawResult)
	case "aws_opensearch":
		return extractOpenSearchConfig(rawResult)
	case "aws_lambda":
		return extractLambdaConfig(rawResult)
	case "aws_msk":
		return extractMSKConfig(rawResult)
	case "aws_alb":
		return extractALBConfig(rawResult)
	case "aws_kms":
		return extractKMSConfig(rawResult)
	case "aws_s3":
		return extractS3Config(rawResult)
	case "aws_secretsmanager":
		return extractSecretsManagerConfig(rawResult)
	case "aws_vpc":
		return extractVPCConfig(rawResult)
	case "aws_bedrock":
		return extractBedrockConfig(rawResult)
	case "aws_cloudfront":
		return extractCloudFrontConfig(rawResult)
	case "aws_sqs":
		return extractSQSConfig(rawResult)
	case "aws_apigateway":
		return extractAPIGatewayConfig(rawResult)
	case "aws_cognito":
		return extractCognitoConfig(rawResult)
	case "aws_dynamodb":
		return extractDynamoDBConfig(rawResult)
	case "aws_ecs":
		return extractECSConfig(rawResult)
	case "aws_eks":
		return extractEKSConfig(rawResult)
	case "aws_waf":
		return extractWAFConfig(rawResult)
	case "aws_cloudwatch_logs", "aws_cloudwatch_monitoring":
		// Both keys share (cloudwatchlogs, describe-log-groups) in
		// componentMetricsMapping, so the same extractor serves both.
		return extractCloudWatchLogsConfig(rawResult)
	case "gcp_compute":
		return extractGCPComputeConfig(rawResult)
	case "gcp_gke":
		return extractGCPGKEConfig(rawResult)
	case "gcp_cloud_run":
		return extractGCPCloudRunConfig(rawResult)
	case "gcp_memorystore":
		return extractGCPMemorystoreConfig(rawResult)
	case "gcp_cloudsql":
		return extractGCPCloudSQLConfig(rawResult)
	case "gcp_gcs":
		return extractGCPGCSConfig(rawResult)
	case "gcp_firestore":
		return extractGCPFirestoreConfig(rawResult)
	case "gcp_pubsub":
		return extractGCPPubSubConfig(rawResult)
	case "gcp_cloud_kms":
		return extractGCPCloudKMSConfig(rawResult)
	case "gcp_secret_manager":
		return extractGCPSecretManagerConfig(rawResult)
	case "gcp_cloud_armor":
		return extractGCPCloudArmorConfig(rawResult)
	case "gcp_identity_platform":
		return extractGCPIdentityPlatformConfig(rawResult)
	case "gcp_vpc":
		return extractGCPVPCConfig(rawResult)
	case "gcp_loadbalancer":
		return extractGCPLoadBalancerConfig(rawResult)
	case "gcp_cloud_logging":
		return extractGCPCloudLoggingConfig(rawResult)
	case "gcp_cloud_build":
		return extractGCPCloudBuildConfig(rawResult)
	case "gcp_vertex_ai":
		return extractGCPVertexAIConfig(rawResult)
	case "gcp_cloud_monitoring":
		return extractGCPCloudMonitoringConfig(rawResult)
	case "gcp_cloud_functions":
		return extractGCPCloudFunctionsConfig(rawResult)
	case "gcp_api_gateway":
		return extractGCPAPIGatewayConfig(rawResult)
	case "gcp_bastion":
		return extractGCPBastionConfig(rawResult)
	case "gcp_github_actions":
		return extractGCPGitHubActionsConfig(rawResult)
	default:
		return nil
	}
}

// sliceFromEnvelope returns a slice of maps from either:
//   - a direct slice result (shape produced by the inspector handlers: e.g.
//     `out.LoadBalancers`, `out.SecretList`, a filtered []map[string]any)
//   - an object envelope with a named key (shape used by test fixtures and by
//     a few pre-existing extractors like extractRDSConfig, where the nested
//     list is keyed by e.g. "DBInstances").
//
// Either shape is acceptable so the extractors work both with real inspector
// outputs and with the JSON-envelope-style fixtures the tests use.
func sliceFromEnvelope(rawResult any, envelopeKey string) []map[string]any {
	if rawResult == nil {
		return nil
	}
	if slice := toSliceOfMaps(rawResult); len(slice) > 0 {
		return slice
	}
	if m := toMap(rawResult); m != nil && envelopeKey != "" {
		return toSliceOfMaps(m[envelopeKey])
	}
	return nil
}

// stringSliceFromEnvelope handles SQS-style list-queues responses where the
// inspector returns []string (queue URLs) directly. Accepts either the raw
// slice or an envelope {QueueUrls: [...]}.
func stringSliceFromEnvelope(rawResult any, envelopeKey string) []string {
	if rawResult == nil {
		return nil
	}
	// Direct []string
	if ss, ok := rawResult.([]string); ok {
		return ss
	}
	// Direct []any of strings (post JSON round-trip)
	if sa, ok := rawResult.([]any); ok {
		out := make([]string, 0, len(sa))
		for _, v := range sa {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	// Envelope {key: [...]}
	if m := toMap(rawResult); m != nil && envelopeKey != "" {
		switch v := m[envelopeKey].(type) {
		case []string:
			return v
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

// --- Helpers for safe field access from AWS JSON responses ---

// toSliceOfMaps converts a JSON-round-tripped value to []map[string]any.
func toSliceOfMaps(v any) []map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var result []map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	return result
}

// toMap converts a JSON-round-tripped value to map[string]any.
func toMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	return result
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func getFloat64(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

// uniformValue returns the value get reads from the items, when every
// item that has a value agrees on it. Items get reports nothing for
// (the inspector skipped or failed their describe call, #927) are left
// out, so they neither hide nor fake a value. Disagreeing items give
// ("", false): one design value cannot describe them all, and claiming
// one would fire a false drift (the same rule extractCloudWatchLogsConfig
// applies to retention).
func uniformValue(items []map[string]any, get func(map[string]any) (string, bool)) (string, bool) {
	val, seen := "", false
	for _, it := range items {
		v, ok := get(it)
		if !ok {
			continue
		}
		if !seen {
			val, seen = v, true
			continue
		}
		if v != val {
			return "", false
		}
	}
	return val, seen
}

// intField reads a JSON number field as a whole-number string.
func intField(key string) func(map[string]any) (string, bool) {
	return func(m map[string]any) (string, bool) {
		f, ok := getFloat64(m, key)
		if !ok {
			return "", false
		}
		return strconv.FormatFloat(f, 'f', 0, 64), true
	}
}

// boolField reads a JSON bool field as the literal lowercase "true" /
// "false", the form the reliable-side humanizeConfigValue BOOLEAN_KEYS
// contract expects (same as extractS3Config's versioning).
func boolField(key string) func(map[string]any) (string, bool) {
	return func(m map[string]any) (string, bool) {
		b, ok := m[key].(bool)
		if !ok {
			return "", false
		}
		return strconv.FormatBool(b), true
	}
}

// gcpResourceBasename returns the last path segment of a GCP resource URL or
// full resource name. The GCP SDK returns fields like Instance.MachineType
// (`https://www.googleapis.com/compute/v1/projects/foo/zones/us-central1-a/machineTypes/e2-medium`)
// and Instance.Zone (`https://www.googleapis.com/compute/v1/projects/foo/zones/us-central1-a`)
// as full URLs; what the UI and the interactive agent actually want is the basename.
//
// Also works for Cloud Run / Memorystore service names
// (`projects/foo/locations/us-central1/services/bar` → `bar`).
//
// Returns the input unchanged if there's no path separator, so already-short
// values (e.g. a plain `"e2-medium"`) pass through.
func gcpResourceBasename(s string) string {
	if s == "" {
		return ""
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}

func boolStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	b, ok := v.(bool)
	if ok {
		if b {
			return "Yes"
		}
		return "No"
	}
	return fmt.Sprintf("%v", v)
}

// --- Per-service extractors ---

// extractRDSConfig extracts config from describe-db-instances response.
// AWS returns: { DBInstances: [ { DBInstanceClass, AllocatedStorage, Engine, MultiAZ, ... } ] }
func extractRDSConfig(rawResult any) map[string]string {
	m := toMap(rawResult)
	if m == nil {
		return nil
	}
	instances := toSliceOfMaps(m["DBInstances"])
	if len(instances) == 0 {
		return nil
	}
	inst := instances[0]
	cfg := make(map[string]string)

	if v := getString(inst, "DBInstanceClass"); v != "" {
		cfg["cpuSize"] = v
	}
	if v, ok := getFloat64(inst, "AllocatedStorage"); ok {
		cfg["storageSize"] = strconv.FormatFloat(v, 'f', 0, 64) + " GB"
	}
	if v := getString(inst, "Engine"); v != "" {
		cfg["engine"] = v
	}
	if v := boolStr(inst, "MultiAZ"); v != "" {
		cfg["multiAz"] = v
	}

	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// extractEC2Config extracts config from describe-instances response.
// AWS returns: { Reservations: [ { Instances: [ { InstanceType, ... } ] } ] }
func extractEC2Config(rawResult any) map[string]string {
	m := toMap(rawResult)
	if m == nil {
		return nil
	}
	reservations := toSliceOfMaps(m["Reservations"])
	cfg := make(map[string]string)
	count := 0

	for _, res := range reservations {
		instances := toSliceOfMaps(res["Instances"])
		for _, inst := range instances {
			state := toMap(inst["State"])
			if state != nil && getString(state, "Name") == "running" {
				count++
			}
			if v := getString(inst, "InstanceType"); v != "" && cfg["instanceType"] == "" {
				cfg["instanceType"] = v
			}
		}
	}

	if count > 0 {
		cfg["numServers"] = strconv.Itoa(count)
	}

	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// extractElastiCacheConfig extracts config from describe-cache-clusters response.
// AWS returns: { CacheClusters: [ { CacheNodeType, Engine, NumCacheNodes, ... } ] }
func extractElastiCacheConfig(rawResult any) map[string]string {
	m := toMap(rawResult)
	if m == nil {
		return nil
	}
	clusters := toSliceOfMaps(m["CacheClusters"])
	if len(clusters) == 0 {
		return nil
	}
	cl := clusters[0]
	cfg := make(map[string]string)

	if v := getString(cl, "CacheNodeType"); v != "" {
		cfg["nodeSize"] = v
	}
	if v := getString(cl, "Engine"); v != "" {
		cfg["engine"] = v
	}
	if v, ok := getFloat64(cl, "NumCacheNodes"); ok && v > 0 {
		replicas := int(v) - 1 // primary node excluded
		if replicas > 0 {
			cfg["replicas"] = strconv.Itoa(replicas)
		}
	}

	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// extractOpenSearchConfig extracts config from describe-domains response.
//
// The inspector (inspectOpenSearch → discoverOpenSearchUnion) produces a
// union of two shapes to cover both deployment styles of aws_opensearch
// (user picks deploymentType=Managed|Serverless on a single schema node):
//
//  1. Managed domain: { DomainStatusList: [ {ClusterConfig, EBSOptions, ...} ] }
//     (or a direct slice of domain-status maps, filtered by Project tag).
//  2. AOSS collection (serverless): flat slice of { Arn, Id, Name, Status, Type }
//     — no ClusterConfig / EBSOptions / DomainName; distinguished by presence
//     of Name/Id without DomainName.
//
// Priority (ordering-independent): prefer the first managed-domain item if
// any exists (managed carries richer config — instanceType, instanceCount,
// storageSize — that the UI's deployed-vs-designed panel surfaces). Only
// fall back to AOSS when no managed entry is found. Rationale: when both
// shapes appear in the same response (a degenerate case that can happen
// when a project legitimately runs both deployment styles), the UI should
// surface the one the user is most likely to care about.
//
// Returns nil when neither shape matches.
func extractOpenSearchConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "DomainStatusList")
	if len(items) == 0 {
		return nil
	}

	// First pass: prefer the first managed-domain entry (has ClusterConfig).
	for _, item := range items {
		if cc := toMap(item["ClusterConfig"]); cc != nil {
			return extractOpenSearchManagedConfig(item)
		}
	}
	// Second pass: fall back to the first AOSS collection (Name/Id without
	// DomainName).
	for _, item := range items {
		if getString(item, "Name") != "" || getString(item, "Id") != "" {
			return extractOpenSearchAOSSConfig(item)
		}
	}
	return nil
}

func extractOpenSearchManagedConfig(dom map[string]any) map[string]string {
	cfg := map[string]string{"deploymentType": "managed"}

	if cc := toMap(dom["ClusterConfig"]); cc != nil {
		if v := getString(cc, "InstanceType"); v != "" {
			cfg["instanceType"] = v
		}
		if v, ok := getFloat64(cc, "InstanceCount"); ok {
			cfg["instanceCount"] = strconv.FormatFloat(v, 'f', 0, 64)
		}
		if v := boolStr(cc, "ZoneAwarenessEnabled"); v != "" {
			cfg["multiAz"] = v
		}
	}
	if ebs := toMap(dom["EBSOptions"]); ebs != nil {
		if v, ok := getFloat64(ebs, "VolumeSize"); ok {
			cfg["storageSize"] = strconv.FormatFloat(v, 'f', 0, 64) + " GB"
		}
	}

	if len(cfg) == 1 { // only deploymentType — no real data to report
		return nil
	}
	return cfg
}

// extractOpenSearchAOSSConfig handles OpenSearch Serverless collection shape
// (frontend treats deploymentType="Serverless" as the signal; see
// component-detail-utils.ts:206-224 for the UI-side branching).
func extractOpenSearchAOSSConfig(col map[string]any) map[string]string {
	cfg := map[string]string{"deploymentType": "serverless"}

	if v := getString(col, "Name"); v != "" {
		cfg["collectionName"] = v
	}
	if v := getString(col, "Status"); v != "" {
		cfg["status"] = v
	}
	if v := getString(col, "Type"); v != "" {
		// e.g. VECTORSEARCH, SEARCH, TIMESERIES — useful drift signal for
		// the Bedrock KB use case which specifically needs VECTORSEARCH.
		cfg["collectionType"] = v
	}

	if len(cfg) == 1 { // only deploymentType
		return nil
	}
	return cfg
}

// extractLambdaConfig extracts config from list-functions response.
// AWS returns: { Functions: [ { Runtime, MemorySize, Timeout, ... } ] }
func extractLambdaConfig(rawResult any) map[string]string {
	m := toMap(rawResult)
	if m == nil {
		return nil
	}
	functions := toSliceOfMaps(m["Functions"])
	if len(functions) == 0 {
		return nil
	}
	fn := functions[0]
	cfg := make(map[string]string)

	if v := getString(fn, "Runtime"); v != "" {
		cfg["runtime"] = v
	}
	if v, ok := getFloat64(fn, "MemorySize"); ok {
		cfg["memorySize"] = strconv.FormatFloat(v, 'f', 0, 64)
	}
	if v, ok := getFloat64(fn, "Timeout"); ok {
		cfg["timeout"] = strconv.FormatFloat(v, 'f', 0, 64) + "s"
	}

	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// extractMSKConfig extracts config from list-clusters response.
// AWS returns: { ClusterInfoList: [ { BrokerNodeGroupInfo: { InstanceType }, NumberOfBrokerNodes, ... } ] }
func extractMSKConfig(rawResult any) map[string]string {
	m := toMap(rawResult)
	if m == nil {
		return nil
	}
	clusters := toSliceOfMaps(m["ClusterInfoList"])
	if len(clusters) == 0 {
		return nil
	}
	cl := clusters[0]
	cfg := make(map[string]string)

	if bng := toMap(cl["BrokerNodeGroupInfo"]); bng != nil {
		if v := getString(bng, "InstanceType"); v != "" {
			cfg["brokerInstanceType"] = v
		}
	}
	if v, ok := getFloat64(cl, "NumberOfBrokerNodes"); ok {
		cfg["brokerCount"] = strconv.FormatFloat(v, 'f', 0, 64)
	}

	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// extractALBConfig extracts config from describe-load-balancers response.
// Inspector returns either out.LoadBalancers ([]elbv2types.LoadBalancer) or
// a filtered []map[string]any. Shape:
//
//	[ { LoadBalancerName, DNSName, Scheme, Type, State:{Code}, VpcId, ... } ]
//
// frontend fields: aws_alb is a bare boolean in lib/stack/ir.ts — no design
// config today, but we surface these drift-useful fields under sensible
// camelCase names for the deployed-vs-designed panel.
func extractALBConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "LoadBalancers")
	if len(items) == 0 {
		return nil
	}
	lb := items[0]
	cfg := make(map[string]string)

	if v := getString(lb, "LoadBalancerName"); v != "" {
		cfg["loadBalancerName"] = v
	}
	if v := getString(lb, "Type"); v != "" {
		cfg["loadBalancerType"] = v
	}
	if v := getString(lb, "Scheme"); v != "" {
		cfg["scheme"] = v
	}
	if v := getString(lb, "DNSName"); v != "" {
		cfg["dnsName"] = v
	}
	if state := toMap(lb["State"]); state != nil {
		if v := getString(state, "Code"); v != "" {
			cfg["state"] = v
		}
	}
	cfg["count"] = strconv.Itoa(len(items))

	if len(cfg) == 1 { // only count — not useful on its own
		return nil
	}
	return cfg
}

// extractKMSConfig extracts config from the kms list-keys response. The
// inspector (inspectKMS → listProjectKMSKeys) returns the project's
// aliases plus one KeyOnly row for each project key without an alias:
//
//	[ { AliasName, AliasArn, TargetKeyId }, { TargetKeyId, KeyOnly: true } ]
//
// Aliases alone are not a key count: two aliases can name one key and a
// key can have no alias. So numKeys counts the distinct TargetKeyId values
// across the rows, which is the number of project keys (#927).
//
// The list-aliases response (and list-keys before #927) has only the
// alias rows. Counting distinct TargetKeyId values there gives the number
// of aliased keys, which is still a key count, just without the keys
// that have no alias.
//
// frontend field (lib/stack/ir.ts:372): numKeys (enum "1"|"3"|"5"). We
// report the actual count so drift fires when the live number of keys
// diverges from the design choice.
func extractKMSConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "Aliases")
	if len(items) == 0 {
		return nil
	}
	keys := make(map[string]struct{}, len(items))
	for _, a := range items {
		if id := getString(a, "TargetKeyId"); id != "" {
			keys[id] = struct{}{}
		}
	}
	count := len(keys)
	if count == 0 {
		// Fall back to total alias count if TargetKeyId is unavailable
		// (some fixtures / older SDK versions omit it).
		count = len(items)
	}
	return map[string]string{
		"numKeys": strconv.Itoa(count),
	}
}

// extractS3Config extracts config from list-buckets response. Shape (the
// inspector enriches each bucket with Versioning via a GetBucketVersioning
// fan-out — see filterS3BucketsByProjectTag / enrichS3Versioning):
//
//	[ { Name, CreationDate, Versioning } ]
//
// frontend field (lib/stack/ir.ts:312): aws_s3.versioning (bool). The
// reliable-side humanizeConfigValue treats "versioning" as a BOOLEAN_KEY,
// so we stringify the literal lowercase bool ("true"/"false").
//
// Multi-bucket nuance: this extractor aggregates the whole aws_s3 component
// (a list of buckets), not one bucket. Versioning is a per-bucket setting,
// so we only emit `versioning` when there is exactly ONE bucket AND its
// state was actually fetched (the inspector omits Versioning when the
// GetBucketVersioning call was skipped/failed). With multiple buckets we
// omit it rather than claim a single value across the set — the per-resource
// imported path surfaces each bucket's versioning precisely (#712).
func extractS3Config(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "Buckets")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"bucketCount": strconv.Itoa(len(items)),
	}
	if len(items) == 1 {
		if v, ok := items[0]["Versioning"]; ok {
			if b, ok := v.(bool); ok {
				cfg["versioning"] = strconv.FormatBool(b)
			}
		}
	}
	return cfg
}

// extractSecretsManagerConfig extracts config from list-secrets response.
// Shape:
//
//	[ { Name, ARN, Description, LastChangedDate, ... } ]
//
// frontend field (lib/stack/ir.ts:379): aws_secretsmanager.numSecrets
// (enum "1"|"3"|"5"). We report the actual count.
func extractSecretsManagerConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "SecretList")
	if len(items) == 0 {
		return nil
	}
	return map[string]string{
		"numSecrets": strconv.Itoa(len(items)),
	}
}

// extractVPCConfig extracts config from describe-vpcs response (inspectVPC
// → inspectVPCWithIGW → out.Vpcs with HasInternetGateway injected). Shape:
//
//	[ { VpcId, CidrBlock, IsDefault, State, Tags, HasInternetGateway, ... } ]
//
// frontend field (lib/stack/ir.ts:116): aws_vpc is an enum "Public VPC"|
// "Private VPC". We emit `deploymentType: "public"|"private"|"mixed"` based
// on whether the VPC(s) have an attached Internet Gateway — a VPC with an
// IGW is public; a VPC with only NAT gateways (private egress) is private.
// The HasInternetGateway flag is injected by inspectVPCWithIGW so the
// extractor doesn't need a second live call.
//
// Identity fields (VpcId, CIDR, state) are surfaced to keep the drift
// signal trustworthy — if the single live VPC's id / state changes, drift
// fires even if the public/private categorization is unchanged.
func extractVPCConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "Vpcs")
	if len(items) == 0 {
		return nil
	}
	vpc := items[0]
	cfg := make(map[string]string)

	if v := getString(vpc, "VpcId"); v != "" {
		cfg["vpcId"] = v
	}
	if v := getString(vpc, "CidrBlock"); v != "" {
		cfg["cidrBlock"] = v
	}
	if v := getString(vpc, "State"); v != "" {
		cfg["state"] = v
	}

	// Classify every VPC in the response as public (IGW attached) or
	// private (no IGW). If the handler didn't inject HasInternetGateway
	// on ANY entry (e.g. fixture from before the IGW-merge was added),
	// skip emitting deploymentType so drift doesn't fire spuriously.
	publicCount, privateCount, classified := 0, 0, 0
	for _, v := range items {
		if _, ok := v["HasInternetGateway"]; !ok {
			continue
		}
		classified++
		if b, ok := v["HasInternetGateway"].(bool); ok && b {
			publicCount++
		} else {
			privateCount++
		}
	}
	if classified > 0 {
		switch {
		case publicCount > 0 && privateCount == 0:
			cfg["deploymentType"] = "public"
		case privateCount > 0 && publicCount == 0:
			cfg["deploymentType"] = "private"
		default:
			cfg["deploymentType"] = "mixed"
		}
	}

	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// extractAPIGatewayConfig extracts config from the API Gateway v2 get-apis
// response (inspectAPIGateway → listAPIGatewayAPIs). Shape:
//
//	[ { ApiId, Name, ProtocolType, ApiEndpoint, Tags, CustomDomainName, CertificateArn, ... } ]
//
// The handler uses the apigatewayv2 SDK (HTTP APIs + WebSocket APIs — the
// modern replacement for classic REST API Gateway). We do NOT handle the
// classic REST shape today because the preset generates v2 HTTP APIs.
//
// frontend fields (lib/stack/ir.ts:364): aws_apigateway.domainName,
// certificateArn. Neither is on the Api record; the inspector finds the
// project's custom domain names, maps them to their API through the API
// mappings, and attaches CustomDomainName + CertificateArn to the API
// (#927). domainName is that custom domain; an API with no custom domain
// falls back to the auto-assigned *.execute-api.<region>.amazonaws.com
// endpoint, which stays a usable drift signal.
func extractAPIGatewayConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "Items")
	if len(items) == 0 {
		return nil
	}
	cfg := make(map[string]string)

	cfg["apiCount"] = strconv.Itoa(len(items))

	first := items[0]
	if v := getString(first, "ProtocolType"); v != "" {
		// HTTP | WEBSOCKET — emit the AWS-SDK uppercase form. Matches the
		// summarizer's emission and keeps the LLM's input consistent across
		// summary and drift views. The UI can lowercase at render time if
		// desired.
		cfg["protocolType"] = v
	}
	// The design config names one custom domain, so report the first API
	// that has one; the preset maps its domain to its one API.
	for _, api := range items {
		if d := getString(api, "CustomDomainName"); d != "" {
			cfg["domainName"] = d
			if c := getString(api, "CertificateArn"); c != "" {
				cfg["certificateArn"] = c
			}
			break
		}
	}
	if _, ok := cfg["domainName"]; !ok {
		if v := getString(first, "ApiEndpoint"); v != "" {
			// The auto-assigned invoke URL. A stable drift signal when no
			// custom domain is wired up yet.
			cfg["domainName"] = v
		}
	}
	// EndpointType lives on classic REST APIs; v2 HTTP/WebSocket APIs don't
	// have it. Emit if present so the field is forward-compatible with any
	// future classic-REST extension.
	if v := getString(first, "EndpointType"); v != "" {
		cfg["endpointType"] = v
	}

	return cfg
}

// extractBedrockConfig extracts config from the inspectBedrock
// list-knowledge-bases path. The handler (discoverBedrockKnowledgeBases)
// returns one of two shapes depending on deployment stage (#1042):
//
//  1. Matched KnowledgeBases:
//     [ { Name, Status, KnowledgeBaseId, KnowledgeBaseArn, UpdatedAt } ]
//  2. IAM-role fallback (pre-KB deploy — the only thing the preset
//     currently provisions): [ { Kind: "IAMRole", RoleName, Arn } ]
//
// Priority (ordering-independent): prefer the first KB item if any exists
// (a real KB represents more deployed state than the role-only stub). Only
// fall back to the first IAM-role entry when no KB is present. Rationale:
// in a fully-deployed project the response will contain only KBs; the
// role-only shape is a pre-KB deploy signal, and if both somehow coexist
// the KB is the more informative drift signal.
//
// frontend fields (lib/stack/ir.ts:396-400): knowledgeBaseName, modelId,
// embeddingModelId.
func extractBedrockConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "KnowledgeBaseSummaries")
	if len(items) == 0 {
		return nil
	}

	// First pass: prefer the first real Knowledge Base.
	for _, item := range items {
		if getString(item, "Kind") == "IAMRole" {
			continue
		}
		if getString(item, "KnowledgeBaseId") == "" && getString(item, "Name") == "" {
			continue
		}
		cfg := make(map[string]string)
		if v := getString(item, "Name"); v != "" {
			cfg["knowledgeBaseName"] = v
		}
		if v := getString(item, "Status"); v != "" {
			cfg["status"] = v
		}
		if v := getString(item, "KnowledgeBaseId"); v != "" {
			cfg["knowledgeBaseId"] = v
		}
		if len(cfg) == 0 {
			continue
		}
		return cfg
	}

	// Second pass: fall back to the first IAM-role entry (pre-KB deploy).
	for _, item := range items {
		if getString(item, "Kind") != "IAMRole" {
			continue
		}
		cfg := map[string]string{"deploymentStage": "iam-role-only"}
		if v := getString(item, "RoleName"); v != "" {
			cfg["roleName"] = v
		}
		if v := getString(item, "Arn"); v != "" {
			cfg["roleArn"] = v
		}
		return cfg
	}

	return nil
}

// extractCloudFrontConfig extracts config from list-distributions response.
// Inspector returns either out.DistributionList (object with .Items) or a
// filtered []map[string]any. Envelope key is "Items".
//
//	{ Items: [ { Id, DomainName, Status, Enabled, DefaultCacheBehavior, Origins, ... } ] }
//
// frontend fields (lib/stack/ir.ts:281): aws_cloudfront.defaultTtl,
// originPath. ListDistributions already returns both inside each
// DistributionSummary (DefaultCacheBehavior.DefaultTTL, and OriginPath on
// the origin the default behavior targets), the same values
// GetDistributionConfig would return, so no extra call is needed (#927).
//
// defaultTtl is emitted in seconds; humanizeConfigValue turns both it and
// the design value ("1h", "1day") into the same words. DefaultTTL is
// absent when the behavior uses a cache policy instead of the legacy TTL
// fields; then defaultTtl is not emitted. originPath is emitted even when
// empty, because "" is a design choice too.
func extractCloudFrontConfig(rawResult any) map[string]string {
	// Inspector unfiltered path returns {DistributionList: {Items: [...]}}
	// or the trimmed envelope {Items: [...]}; the filtered path returns a
	// flat slice. sliceFromEnvelope handles both flat-slice and envelope
	// shapes with the same code path the other extractors use.
	items := sliceFromEnvelope(rawResult, "Items")
	if len(items) == 0 {
		return nil
	}
	d := items[0]
	cfg := make(map[string]string)

	if v := getString(d, "Id"); v != "" {
		cfg["distributionId"] = v
	}
	if v := getString(d, "DomainName"); v != "" {
		cfg["domainName"] = v
	}
	if v := getString(d, "Status"); v != "" {
		cfg["status"] = v
	}
	if v := boolStr(d, "Enabled"); v != "" {
		cfg["enabled"] = v
	}
	if v, ok := uniformValue(items, cloudFrontDefaultTTL); ok {
		cfg["defaultTtl"] = v
	}
	if v, ok := uniformValue(items, cloudFrontDefaultOriginPath); ok {
		cfg["originPath"] = v
	}

	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func cloudFrontDefaultTTL(d map[string]any) (string, bool) {
	dcb := toMap(d["DefaultCacheBehavior"])
	if dcb == nil {
		return "", false
	}
	return intField("DefaultTTL")(dcb)
}

// cloudFrontDefaultOriginPath returns the OriginPath of the origin the
// default cache behavior sends requests to. A distribution can have
// several origins; the default behavior's is the one the preset's
// origin_path sets.
func cloudFrontDefaultOriginPath(d map[string]any) (string, bool) {
	dcb := toMap(d["DefaultCacheBehavior"])
	origins := toMap(d["Origins"])
	if dcb == nil || origins == nil {
		return "", false
	}
	target := getString(dcb, "TargetOriginId")
	for _, o := range toSliceOfMaps(origins["Items"]) {
		if getString(o, "Id") != target {
			continue
		}
		if _, ok := o["OriginPath"]; !ok {
			return "", false
		}
		return getString(o, "OriginPath"), true
	}
	return "", false
}

// extractSQSConfig extracts config from either sqs action. list-queues
// returns a plain []string of URLs, which yields identity only (count
// and the URL-suffix type). describe-queues (inspectSQS →
// describeSQSQueues, #927) returns one row per queue:
//
//	[ { QueueUrl, VisibilityTimeout, FifoQueue } ]
//
// VisibilityTimeout and FifoQueue come from a GetQueueAttributes call per
// queue and are absent when that call was skipped or failed.
//
// frontend fields (lib/stack/ir.ts:322-326): aws_sqs.type (Standard|FIFO)
// and visibilityTimeout. type uses the FifoQueue attribute, or the
// ".fifo" URL suffix (FIFO queue names must end in it) when the attribute
// was not fetched. visibilityTimeout is emitted in seconds;
// humanizeConfigValue turns both it and the design value ("10 minutes")
// into the same words.
func extractSQSConfig(rawResult any) map[string]string {
	rows := sqsQueueRows(rawResult)
	if len(rows) == 0 {
		return nil
	}
	cfg := map[string]string{
		"queueCount": strconv.Itoa(len(rows)),
	}
	fifoCount := 0
	for _, r := range rows {
		fifo, ok := r["FifoQueue"].(bool)
		if !ok {
			fifo = strings.HasSuffix(getString(r, "QueueUrl"), ".fifo")
		}
		if fifo {
			fifoCount++
		}
	}
	if fifoCount == len(rows) {
		cfg["type"] = "FIFO"
	} else if fifoCount == 0 {
		cfg["type"] = "Standard"
	}
	// mixed → omit type
	if v, ok := uniformValue(rows, intField("VisibilityTimeout")); ok {
		cfg["visibilityTimeout"] = v
	}
	return cfg
}

// sqsQueueRows returns the describe-queues rows, turning the list-queues
// []string of URLs (or a {QueueUrls: [...]} fixture envelope) into
// {QueueUrl} rows.
func sqsQueueRows(rawResult any) []map[string]any {
	if urls := stringSliceFromEnvelope(rawResult, "QueueUrls"); len(urls) > 0 {
		rows := make([]map[string]any, 0, len(urls))
		for _, u := range urls {
			rows = append(rows, map[string]any{"QueueUrl": u})
		}
		return rows
	}
	return sliceFromEnvelope(rawResult, "QueueUrls")
}

// extractCognitoConfig extracts config from the list-user-pools response
// (inspectCognito → filterCognitoUserPoolsByProjectTag). Shape:
//
//	[ { Id, Name, Status, CreationDate, LambdaConfig } ]
//
// frontend fields (lib/stack/ir.ts:347): aws_cognito.signInType,
// mfaRequired, okta, auth0. mfaRequired is surfaced here; the inspector
// enriches each project-scoped pool with MfaConfiguration off the
// DescribeUserPool response it already issues for the ARN — see
// filterCognitoUserPoolsByProjectTag (#712). signInType / okta / auth0
// still need wider DescribeUserPool fields and remain design-only.
//
// MfaConfiguration is OFF|ON|OPTIONAL; mfaRequired is true only for ON
// (OPTIONAL leaves MFA up to the user, so it is not "required"). The value
// is the literal lowercase bool to match the reliable-side
// humanizeConfigValue BOOLEAN_KEYS contract.
//
// Multi-pool nuance (same as extractS3Config): MfaConfiguration is
// per-pool, so mfaRequired is only surfaced when there is exactly ONE pool
// AND its config was fetched (the inspector omits MfaConfiguration on the
// empty-project demo path); multiple pools omit it.
func extractCognitoConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "UserPools")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"userPoolCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "Name"); v != "" {
		cfg["poolName"] = v
	}
	if v := getString(first, "Status"); v != "" {
		cfg["status"] = v
	}
	if v := getString(first, "Id"); v != "" {
		cfg["poolId"] = v
	}
	if len(items) == 1 {
		if mfa := getString(first, "MfaConfiguration"); mfa != "" {
			cfg["mfaRequired"] = strconv.FormatBool(mfa == "ON")
		}
	}
	return cfg
}

// extractDynamoDBConfig extracts config from either dynamodb action.
// list-tables returns a plain []string of names (or the {TableNames:
// [...]} fixture envelope), which yields identity only. describe-tables
// (inspectDynamoDB → describeDynamoDBTables, #927) returns one row per
// table:
//
//	[ { TableName, BillingModeSummary, ProvisionedThroughput, SSEDescription } ]
//
// The three DescribeTable fields are absent when that call was skipped
// or failed.
//
// frontend field (lib/stack/ir.ts:315): aws_dynamodb.type, the billing
// mode, in the design vocabulary ("On demand" | "provisioned", ZDdbType),
// so the deployed value reads the same as the design value. The
// provisioned capacity and the encryption type are surfaced as drift
// signals with no design counterpart.
func extractDynamoDBConfig(rawResult any) map[string]string {
	rows := dynamoDBTableRows(rawResult)
	if len(rows) == 0 {
		return nil
	}
	cfg := map[string]string{
		"tableCount": strconv.Itoa(len(rows)),
	}
	if name := getString(rows[0], "TableName"); name != "" {
		cfg["tableName"] = name
	}
	if v, ok := uniformValue(rows, dynamoDBBillingType); ok {
		cfg["type"] = v
	}
	if v, ok := uniformValue(rows, dynamoDBCapacity("ReadCapacityUnits")); ok {
		cfg["readCapacity"] = v
	}
	if v, ok := uniformValue(rows, dynamoDBCapacity("WriteCapacityUnits")); ok {
		cfg["writeCapacity"] = v
	}
	if v, ok := uniformValue(rows, dynamoDBSSEType); ok {
		cfg["sseType"] = v
	}
	return cfg
}

// dynamoDBTableRows returns the describe-tables rows, turning the
// list-tables []string of names into {TableName} rows.
func dynamoDBTableRows(rawResult any) []map[string]any {
	if names := stringSliceFromEnvelope(rawResult, "TableNames"); len(names) > 0 {
		rows := make([]map[string]any, 0, len(names))
		for _, n := range names {
			rows = append(rows, map[string]any{"TableName": n})
		}
		return rows
	}
	return sliceFromEnvelope(rawResult, "TableNames")
}

// dynamoDBDescribed reports whether the row carries a DescribeTable
// answer. ProvisionedThroughput is in every DescribeTable answer, and
// BillingModeSummary may be missing from one (a table that was always
// provisioned), so it alone cannot be the marker.
func dynamoDBDescribed(r map[string]any) bool {
	return toMap(r["ProvisionedThroughput"]) != nil || toMap(r["BillingModeSummary"]) != nil
}

func dynamoDBBillingMode(r map[string]any) (string, bool) {
	if !dynamoDBDescribed(r) {
		return "", false
	}
	if bms := toMap(r["BillingModeSummary"]); bms != nil {
		if m := getString(bms, "BillingMode"); m != "" {
			return m, true
		}
	}
	// AWS omits BillingModeSummary for tables that were never switched
	// to on-demand: those are provisioned.
	return "PROVISIONED", true
}

func dynamoDBBillingType(r map[string]any) (string, bool) {
	mode, ok := dynamoDBBillingMode(r)
	if !ok {
		return "", false
	}
	switch mode {
	case "PAY_PER_REQUEST":
		return "On demand", true
	case "PROVISIONED":
		return "provisioned", true
	}
	return mode, true
}

// dynamoDBCapacity reads a provisioned capacity unit count. On-demand
// tables report 0 units, which is not a setting, so only provisioned
// tables give a value.
func dynamoDBCapacity(key string) func(map[string]any) (string, bool) {
	return func(r map[string]any) (string, bool) {
		if mode, ok := dynamoDBBillingMode(r); !ok || mode != "PROVISIONED" {
			return "", false
		}
		pt := toMap(r["ProvisionedThroughput"])
		if pt == nil {
			return "", false
		}
		return intField(key)(pt)
	}
}

// dynamoDBSSEType reads the encryption at rest type. DynamoDB always
// encrypts; SSEDescription is absent when the table uses the default
// AWS owned key, and says KMS when it uses a KMS key.
func dynamoDBSSEType(r map[string]any) (string, bool) {
	if !dynamoDBDescribed(r) {
		return "", false
	}
	sse := toMap(r["SSEDescription"])
	if sse == nil {
		return "AWS_OWNED", true
	}
	if t := getString(sse, "SSEType"); t != "" {
		return t, true
	}
	return "AWS_OWNED", true
}

// extractECSConfig extracts config from the ecs describe-clusters
// response (inspectECS → filterECSClustersByProjectTag →
// enrichECSServiceConnect), or from the list-clusters response, which is
// the same SDK clusters (DescribeClusters with the TAGS and SETTINGS
// include) without the ServiceConnectEnabled field. describe-clusters
// adds that field because Service Connect takes extra per-cluster calls
// that the cheap panel-default list-clusters does not make. After the
// JSON round-trip the shape is:
//
//	[ { ClusterName, ClusterArn, Settings, CapacityProviders,
//	    DefaultCapacityProviderStrategy, ServiceConnectEnabled, ... } ]
//
// Envelope key for fixture round-trips: "Clusters" (matches the AWS
// DescribeClusters response shape).
//
// frontend fields (lib/stack/ir.ts:271-280), all read here (#927):
//
//   - enableContainerInsights: Settings[name=containerInsights]. The
//     value is enabled, enhanced or disabled; the first two are on.
//   - capacityProviders: CapacityProviders, comma-joined in AWS order.
//   - defaultCapacityProvider: the DefaultCapacityProviderStrategy item
//     with the largest base, then weight (the preset sets one item).
//   - enableServiceConnect: ServiceConnectEnabled, which describe-clusters
//     works out from the cluster's services; absent when unknown, and
//     always absent from list-clusters.
//
// Each is emitted only when every cluster that has it agrees.
func extractECSConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "Clusters")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"clusterCount": strconv.Itoa(len(items)),
	}
	if v := getString(items[0], "ClusterName"); v != "" {
		cfg["clusterName"] = v
	}
	if v, ok := uniformValue(items, ecsContainerInsights); ok {
		cfg["enableContainerInsights"] = v
	}
	if v, ok := uniformValue(items, ecsCapacityProviders); ok {
		cfg["capacityProviders"] = v
	}
	if v, ok := uniformValue(items, ecsDefaultCapacityProvider); ok {
		cfg["defaultCapacityProvider"] = v
	}
	if v, ok := uniformValue(items, boolField("ServiceConnectEnabled")); ok {
		cfg["enableServiceConnect"] = v
	}
	return cfg
}

func ecsContainerInsights(c map[string]any) (string, bool) {
	for _, st := range toSliceOfMaps(c["Settings"]) {
		if getString(st, "Name") != "containerInsights" {
			continue
		}
		switch getString(st, "Value") {
		case "enabled", "enhanced":
			return "true", true
		case "disabled":
			return "false", true
		}
	}
	return "", false
}

func ecsCapacityProviders(c map[string]any) (string, bool) {
	list, ok := c["CapacityProviders"].([]any)
	if !ok || len(list) == 0 {
		return "", false
	}
	names := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok && s != "" {
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	return strings.Join(names, ", "), true
}

func ecsDefaultCapacityProvider(c map[string]any) (string, bool) {
	best, bestBase, bestWeight := "", -1.0, -1.0
	for _, item := range toSliceOfMaps(c["DefaultCapacityProviderStrategy"]) {
		name := getString(item, "CapacityProvider")
		if name == "" {
			continue
		}
		base, _ := getFloat64(item, "Base")
		weight, _ := getFloat64(item, "Weight")
		if base > bestBase || (base == bestBase && weight > bestWeight) {
			best, bestBase, bestWeight = name, base, weight
		}
	}
	return best, best != ""
}

// extractEKSConfig extracts config from either eks cluster action.
// list-clusters returns a plain []string of names (or the {Clusters:
// [...]} fixture envelope), which yields identity only.
// describe-clusters (inspectEKS → describeEKSClusters, #927) returns one
// row per cluster:
//
//	[ { Name, Version, EndpointPublicAccess, EndpointPrivateAccess,
//	    NodeGroups: [ { Name, InstanceTypes, DesiredSize, MinSize, MaxSize } ] } ]
//
// The fields after Name come from DescribeCluster and DescribeNodegroup
// and are absent when those calls were skipped or failed.
//
// frontend fields (lib/stack/ir.ts:259-279):
//
//   - controlPlaneVisibility: "Public" when the API endpoint is public,
//     else "Private" (the preset maps the design choice to
//     endpoint_public_access, with private access always on).
//   - desiredSize, minSize, maxSize, instanceType: from the managed node
//     groups, emitted when every node group agrees.
//   - haControlPlane is not emitted: the EKS control plane always runs
//     across availability zones, and the preset has no setting for it,
//     so there is no deployed value to compare.
//
// version (the Kubernetes version) is surfaced as a drift signal.
func extractEKSConfig(rawResult any) map[string]string {
	rows := eksClusterRows(rawResult)
	if len(rows) == 0 {
		return nil
	}
	cfg := map[string]string{
		"clusterCount": strconv.Itoa(len(rows)),
	}
	if v := getString(rows[0], "Name"); v != "" {
		cfg["clusterName"] = v
	}
	if v, ok := uniformValue(rows, func(r map[string]any) (string, bool) {
		v := getString(r, "Version")
		return v, v != ""
	}); ok {
		cfg["version"] = v
	}
	if v, ok := uniformValue(rows, func(r map[string]any) (string, bool) {
		public, ok := r["EndpointPublicAccess"].(bool)
		if !ok {
			return "", false
		}
		if public {
			return "Public", true
		}
		return "Private", true
	}); ok {
		cfg["controlPlaneVisibility"] = v
	}

	var nodeGroups []map[string]any
	for _, r := range rows {
		nodeGroups = append(nodeGroups, toSliceOfMaps(r["NodeGroups"])...)
	}
	for key, field := range map[string]string{"desiredSize": "DesiredSize", "minSize": "MinSize", "maxSize": "MaxSize"} {
		if v, ok := uniformValue(nodeGroups, intField(field)); ok {
			cfg[key] = v
		}
	}
	if v, ok := uniformValue(nodeGroups, func(ng map[string]any) (string, bool) {
		list, ok := ng["InstanceTypes"].([]any)
		if !ok || len(list) == 0 {
			return "", false
		}
		types := make([]string, 0, len(list))
		for _, t := range list {
			if s, ok := t.(string); ok {
				types = append(types, s)
			}
		}
		return strings.Join(types, ", "), len(types) > 0
	}); ok {
		cfg["instanceType"] = v
	}
	return cfg
}

// eksClusterRows returns the describe-clusters rows, turning the
// list-clusters []string of names into {Name} rows.
func eksClusterRows(rawResult any) []map[string]any {
	if names := stringSliceFromEnvelope(rawResult, "Clusters"); len(names) > 0 {
		rows := make([]map[string]any, 0, len(names))
		for _, n := range names {
			rows = append(rows, map[string]any{"Name": n})
		}
		return rows
	}
	return sliceFromEnvelope(rawResult, "Clusters")
}

// extractWAFConfig extracts config from the list-web-acls response
// (inspectWAF, merged regional + CLOUDFRONT scopes, each ACL described
// with GetWebACL). Shape:
//
//	[ { Name, Id, ARN, Description, LockToken, Scope, DefaultAction, RuleNames, RuleCount } ]
//
// aws_waf is a bare boolean in lib/stack/ir.ts — no designable config — so
// this extractor emits drift signals only: the first ACL's identity and,
// from GetWebACL (#927), its scope, default action (ALLOW | BLOCK) and
// rule names in priority order. The GetWebACL fields are absent when that
// call was skipped or failed; the identity fields are still reported.
func extractWAFConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "WebACLs")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"webAclCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "Name"); v != "" {
		cfg["webAclName"] = v
	}
	if v := getString(first, "Id"); v != "" {
		cfg["webAclId"] = v
	}
	if v := getString(first, "Scope"); v != "" {
		cfg["scope"] = v
	}
	if v := getString(first, "DefaultAction"); v != "" {
		cfg["defaultAction"] = v
	}
	if v, ok := intField("RuleCount")(first); ok {
		cfg["ruleCount"] = v
	}
	if rules, ok := first["RuleNames"].([]any); ok && len(rules) > 0 {
		names := make([]string, 0, len(rules))
		for _, r := range rules {
			if s, ok := r.(string); ok {
				names = append(names, s)
			}
		}
		cfg["rules"] = strings.Join(names, ", ")
	}
	return cfg
}

// extractCloudWatchLogsConfig extracts config from the describe-log-groups
// response (inspectCloudWatchLogs). Shape:
//
//	[ { LogGroupName, RetentionInDays, StoredBytes, CreationTime, KmsKeyId } ]
//
// frontend fields (lib/stack/ir.ts:337-344): aws_cloudwatch_logs.retentionDays
// and aws_cloudwatch_monitoring.retentionDays. RetentionInDays is per-group
// and can vary across log groups in the same project — we emit it only when
// uniform across every group returned, so drift doesn't fire spuriously when
// one ad-hoc group disagrees with the project default.
//
// kmsEncrypted is "Yes" if any returned group has a KmsKeyId set (any-CMK
// signal is sufficient for the drift card; the design-time schema doesn't
// encode per-group keys today).
func extractCloudWatchLogsConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "LogGroups")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"logGroupCount": strconv.Itoa(len(items)),
	}

	// Uniform retention → emit; mixed → omit.
	var firstRetention float64
	firstSeen := false
	uniform := true
	anyHasRetention := false
	kmsEncrypted := false
	for _, g := range items {
		if v, ok := getFloat64(g, "RetentionInDays"); ok {
			anyHasRetention = true
			if !firstSeen {
				firstRetention = v
				firstSeen = true
			} else if v != firstRetention {
				uniform = false
			}
		}
		if getString(g, "KmsKeyId") != "" {
			kmsEncrypted = true
		}
	}
	if anyHasRetention && uniform {
		cfg["retentionDays"] = strconv.FormatFloat(firstRetention, 'f', 0, 64)
	}
	if kmsEncrypted {
		cfg["kmsEncrypted"] = "Yes"
	}

	return cfg
}

// --- GCP extractors ---
//
// GCP inspectors (internal/agentapi/gcp_inspect.go) return typed proto
// pointers (e.g. []*computepb.Instance, []*containerpb.Cluster,
// []*runpb.Service, []*redispb.Instance). After JSON round-trip through
// `toSliceOfMaps` the keys are lowerCamelCase per the proto JSON mapping
// — e.g. `machineType`, `currentNodeCount`, `memorySizeGb`. The existing
// getString / getFloat64 / boolStr helpers work unchanged on this shape.
//
// Identity fields come from the SDK-returned list-* response only; any
// deeper field that would require a describe-* per-resource call is
// flagged with a TODO(#928) and left for a follow-up.

// extractGCPComputeConfig extracts config from the list-instances response
// (inspectGCPCompute → compute.InstancesClient.AggregatedList). Shape
// (after JSON round-trip of []*computepb.Instance):
//
//	[ { name, machineType, zone, status, networkInterfaces[...], ... } ]
//
// machineType and zone come back as full resource URLs
// (`.../zones/us-central1-a/machineTypes/e2-medium`); the basename is what
// the UI / design card actually compares against. AggregatedList fans out
// across every zone in the project, so we also surface a count.
//
// frontend field (lib/stack/ir.ts: gcp_compute has `machineType` + count).
// TODO(#928): when the UI decides to surface boot-disk size / service
// account, fan out to describe-instance per first instance — list-instances
// does already include `disks[]` and `serviceAccounts[]` so both could be
// surfaced directly without a second call.
func extractGCPComputeConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "instances")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"instanceCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "name"); v != "" {
		cfg["instanceName"] = v
	}
	if v := gcpResourceBasename(getString(first, "machineType")); v != "" {
		cfg["machineType"] = v
	}
	if v := gcpResourceBasename(getString(first, "zone")); v != "" {
		cfg["zone"] = v
	}
	if v := getString(first, "status"); v != "" {
		cfg["status"] = v
	}
	return cfg
}

// extractGCPGKEConfig extracts config from the list-clusters response
// (inspectGCPGKE → container.ClusterManagerClient.ListClusters across
// `locations/-`). Shape (after JSON round-trip of []*containerpb.Cluster):
//
//	[ { name, status, location, currentNodeCount, currentMasterVersion,
//	    autopilot: {enabled}, privateClusterConfig: {enablePrivateNodes}, ... } ]
//
// frontend fields (lib/stack/ir.ts: gcp_gke): clusterVersion, nodeCount,
// autopilot, privateCluster. All four are on the list-clusters summary so
// no describe-cluster fan-out is needed for the design-vs-deployed panel.
// Surface them + identity fields.
//
// Multi-location flattening: ListClusters('locations/-') returns clusters
// from every region, so `location` in the response is the per-cluster
// location, not a filter. Emit the first cluster's location so the UI can
// show "deployed to us-central1" next to the design-time region.
func extractGCPGKEConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "clusters")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"clusterCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "name"); v != "" {
		cfg["clusterName"] = v
	}
	if v := getString(first, "status"); v != "" {
		cfg["status"] = v
	}
	if v := getString(first, "location"); v != "" {
		cfg["location"] = v
	}
	if v, ok := getFloat64(first, "currentNodeCount"); ok {
		cfg["nodeCount"] = strconv.FormatFloat(v, 'f', 0, 64)
	}
	if v := getString(first, "currentMasterVersion"); v != "" {
		cfg["clusterVersion"] = v
	}
	if ap := toMap(first["autopilot"]); ap != nil {
		if v := boolStr(ap, "enabled"); v != "" {
			cfg["autopilot"] = v
		}
	}
	if pcc := toMap(first["privateClusterConfig"]); pcc != nil {
		if v := boolStr(pcc, "enablePrivateNodes"); v != "" {
			cfg["privateCluster"] = v
		}
	}
	return cfg
}

// extractGCPCloudRunConfig extracts config from the list-services response
// (inspectGCPCloudRun → run.ServicesClient.ListServices across
// `locations/-`, Cloud Run v2 API). Shape (after JSON round-trip of
// []*runpb.Service):
//
//	[ { name, uri, creator,
//	    template: { containers: [ { resources: { limits: {cpu, memory} } } ],
//	                scaling: { minInstanceCount, maxInstanceCount },
//	                maxInstanceRequestConcurrency },
//	    traffic: [...], ... } ]
//
// The `name` field is the full resource name
// (`projects/<id>/locations/<region>/services/<svc>`); the basename is what
// the UI / LLM actually compares against a design-time service name.
//
// frontend fields (lib/stack/ir.ts: gcp_cloud_run): cpu, memory, minInstances,
// maxInstances, concurrency. All of them live under `template` — surface them
// from the first service so the design-vs-deployed card can compare.
func extractGCPCloudRunConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "services")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"serviceCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["serviceName"] = v
	}
	if v := getString(first, "uri"); v != "" {
		cfg["uri"] = v
	}
	// Location is embedded in the resource name between `locations/` and
	// `/services/`; surface it for region drift.
	if loc := extractGCPLocationFromName(getString(first, "name")); loc != "" {
		cfg["location"] = loc
	}
	if tmpl := toMap(first["template"]); tmpl != nil {
		// First container's resource limits.
		if containers := toSliceOfMaps(tmpl["containers"]); len(containers) > 0 {
			if res := toMap(containers[0]["resources"]); res != nil {
				if limits := toMap(res["limits"]); limits != nil {
					if v := getString(limits, "cpu"); v != "" {
						cfg["cpu"] = v
					}
					if v := getString(limits, "memory"); v != "" {
						cfg["memory"] = v
					}
				}
			}
		}
		if scaling := toMap(tmpl["scaling"]); scaling != nil {
			if v, ok := getFloat64(scaling, "minInstanceCount"); ok {
				cfg["minInstances"] = strconv.FormatFloat(v, 'f', 0, 64)
			}
			if v, ok := getFloat64(scaling, "maxInstanceCount"); ok {
				cfg["maxInstances"] = strconv.FormatFloat(v, 'f', 0, 64)
			}
		}
		if v, ok := getFloat64(tmpl, "maxInstanceRequestConcurrency"); ok {
			cfg["concurrency"] = strconv.FormatFloat(v, 'f', 0, 64)
		}
	}
	return cfg
}

// extractGCPLocationFromName pulls the region out of a Cloud Run v2 resource
// name of the form `projects/<id>/locations/<region>/services/<svc>`. Used
// by extractGCPCloudRunConfig; isolated so test fixtures don't need to
// replicate the SDK's full URL shape to exercise location drift.
// extractGCPLocationFromName pulls the location/region segment out of a
// GCP resource full name of the form
// `projects/<id>/locations/<loc>/<resource-kind>/<name>`. Used across
// Cloud Run, Memorystore, Secret Manager, Vertex AI, etc. — every
// GCP proto API that scopes by location embeds it in the resource name
// the same way.
func extractGCPLocationFromName(fullName string) string {
	_, rest, ok := strings.Cut(fullName, "/locations/")
	if !ok {
		return ""
	}
	if loc, _, ok := strings.Cut(rest, "/"); ok {
		return loc
	}
	return rest
}

// extractGCPMemorystoreConfig extracts config from the list-instances
// response (inspectGCPMemorystore → redis.CloudRedisClient.ListInstances
// across `locations/-`). Shape (after JSON round-trip of
// []*redispb.Instance):
//
//	[ { name, tier, memorySizeGb, redisVersion, state, locationId,
//	    authorizedNetwork, host, port, ... } ]
//
// frontend fields (lib/stack/ir.ts: gcp_memorystore): tier, memorySizeGb,
// redisVersion. All three are on the list summary so no describe-instance
// fan-out is needed. Emit drift signals for state + location identity too.
//
// Multi-region: ListInstances('locations/-') aggregates across regions;
// `locationId` in the response is the per-instance region (not a filter).
func extractGCPMemorystoreConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "instances")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"instanceCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["instanceName"] = v
	}
	if v := getString(first, "tier"); v != "" {
		cfg["tier"] = v
	}
	if v, ok := getFloat64(first, "memorySizeGb"); ok {
		cfg["memorySizeGb"] = strconv.FormatFloat(v, 'f', 0, 64)
	}
	if v := getString(first, "redisVersion"); v != "" {
		cfg["redisVersion"] = v
	}
	if v := getString(first, "state"); v != "" {
		cfg["state"] = v
	}
	if v := getString(first, "locationId"); v != "" {
		cfg["location"] = v
	}
	return cfg
}

// extractGCPCloudSQLConfig extracts config from the list-instances response
// (inspectGCPCloudSQL → sqladmin.Service.Instances.List, returning
// []*sqladmin.DatabaseInstance). Shape:
//
//	[ { name, databaseVersion, state, region, connectionName,
//	    settings: { tier, dataDiskSizeGb, availabilityType,
//	                backupConfiguration: { enabled } }, ... } ]
//
// frontend fields (lib/stack/ir.ts:471-479): tier, diskSizeGb,
// highAvailability. All three come from the list response's settings
// sub-object so no describe-instance fan-out is needed.
//
// availabilityType encodes HA: "REGIONAL" = highly available, "ZONAL" =
// single zone. Emit `highAvailability: "Yes"|"No"` to match the bool
// shape of the design-time field.
func extractGCPCloudSQLConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "items")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"instanceCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "name"); v != "" {
		cfg["instanceName"] = v
	}
	if v := getString(first, "databaseVersion"); v != "" {
		cfg["databaseVersion"] = v
	}
	if v := getString(first, "state"); v != "" {
		cfg["state"] = v
	}
	if v := getString(first, "region"); v != "" {
		cfg["region"] = v
	}
	if settings := toMap(first["settings"]); settings != nil {
		if v := getString(settings, "tier"); v != "" {
			cfg["tier"] = v
		}
		if v, ok := getFloat64(settings, "dataDiskSizeGb"); ok {
			// The SQL Admin Discovery SDK marshals int64 fields as JSON
			// numbers (the same encoding/json contract every other
			// Discovery struct uses).
			cfg["diskSizeGb"] = strconv.FormatFloat(v, 'f', 0, 64)
		}
		switch getString(settings, "availabilityType") {
		case "REGIONAL":
			cfg["highAvailability"] = "Yes"
		case "ZONAL":
			cfg["highAvailability"] = "No"
		}
	}
	return cfg
}

// extractGCPGCSConfig extracts config from the list-buckets response. The
// inspector (inspectGCPGCS) PRE-FLATTENS each bucket to a 5-key map:
//
//	[ { name, location, storageClass, created, versioning } ]
//
// — so this extractor is the simplest of the bundle: no proto parsing
// required, straight field lookups.
//
// frontend fields (lib/stack/ir.ts:493-494): storageClass, versioning.
// versioning comes straight off BucketAttrs.VersioningEnabled — list-buckets
// already fetches full attrs, so (unlike AWS S3) no second SDK call is
// needed. It is stringified as the literal lowercase bool ("true"/"false")
// to match the reliable-side humanizeConfigValue BOOLEAN_KEYS contract.
//
// Multi-bucket nuance (same as extractS3Config): versioning is per-bucket,
// so we only surface it when there is exactly ONE bucket; multiple buckets
// omit it rather than claim a single value across the set (#712).
func extractGCPGCSConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "buckets")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"bucketCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "name"); v != "" {
		cfg["bucketName"] = v
	}
	if v := getString(first, "location"); v != "" {
		cfg["location"] = v
	}
	if v := getString(first, "storageClass"); v != "" {
		cfg["storageClass"] = v
	}
	if len(items) == 1 {
		if v, ok := first["versioning"]; ok {
			if b, ok := v.(bool); ok {
				cfg["versioning"] = strconv.FormatBool(b)
			}
		}
	}
	return cfg
}

// extractGCPFirestoreConfig extracts config from the list-collections
// response (inspectGCPFirestore → firestore.Client.Collections). Inspector
// returns []string of collection IDs. Envelope key for fixture round-trips:
// "collections".
//
// gcp_firestore is a bare boolean in lib/stack/ir.ts — no designable config.
// Extractor emits drift identity only (count + first-collection name) so
// the UI can render a "deployed, N collections" card.
func extractGCPFirestoreConfig(rawResult any) map[string]string {
	names := stringSliceFromEnvelope(rawResult, "collections")
	if len(names) == 0 {
		return nil
	}
	cfg := map[string]string{
		"collectionCount": strconv.Itoa(len(names)),
	}
	if names[0] != "" {
		cfg["collectionName"] = names[0]
	}
	return cfg
}

// extractGCPPubSubConfig extracts config from the list-topics response
// (inspectGCPPubSub → pubsubadmin.TopicAdminClient.ListTopics,
// []*pubsubpb.Topic). Shape:
//
//	[ { name, messageRetentionDuration, kmsKeyName,
//	    messageStoragePolicy: { allowedPersistenceRegions } } ]
//
// `name` is the full resource path (projects/<id>/topics/<topic>); the
// basename is what the UI compares. messageRetentionDuration is a
// proto duration string (e.g. "604800s" = 7 days).
//
// frontend field (lib/stack/ir.ts:496-502): messageRetentionDuration.
func extractGCPPubSubConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "topics")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"topicCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["topicName"] = v
	}
	if v := getString(first, "messageRetentionDuration"); v != "" {
		cfg["messageRetentionDuration"] = v
	}
	if v := getString(first, "kmsKeyName"); v != "" {
		cfg["kmsKeyName"] = v
	}
	return cfg
}

// extractGCPCloudKMSConfig extracts config from the list-keyrings response
// (inspectGCPKMS → kms.NewKeyManagementClient.ListKeyRings, default
// location "global"). Shape (after JSON round-trip of []*kmspb.KeyRing):
//
//	[ { name, createTime } ]
//
// gcp_cloud_kms is a bare boolean in lib/stack/ir.ts — no designable
// config. Emit drift identity: keyring count + first ring name.
//
// TODO(#928): surface `numKeys` by fanning out ListCryptoKeys
// per key ring. Each extra call adds latency but would give the UI a
// meaningful count-of-keys card. Current implementation reports key
// RINGS only (kmspb.KeyRing has just {name, createTime}).
func extractGCPCloudKMSConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "keyRings")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"keyringCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["keyringName"] = v
	}
	return cfg
}

// extractGCPSecretManagerConfig extracts config from the list-secrets
// response (inspectGCPSecretManager →
// secretmanager.NewClient.ListSecrets). Shape (after JSON round-trip of
// []*secretmanagerpb.Secret):
//
//	[ { name, replication: { automatic | userManaged: {...} },
//	    createTime, labels, rotation: {...} } ]
//
// gcp_secret_manager is a bare boolean in lib/stack/ir.ts — no
// designable config. Emit secretCount + first secret name + replication
// policy so the LLM can answer "is this multi-region?" without a second
// describe call. replication is an oneof; pick whichever sub-key is
// present to surface "automatic" or "user_managed".
func extractGCPSecretManagerConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "secrets")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"secretCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["secretName"] = v
	}
	if rep := toMap(first["replication"]); rep != nil {
		switch {
		case rep["automatic"] != nil:
			cfg["replication"] = "automatic"
		case rep["userManaged"] != nil:
			cfg["replication"] = "user-managed"
		}
	}
	return cfg
}

// extractGCPCloudArmorConfig extracts config from the list-policies
// response (inspectGCPCloudArmor →
// computeapi.Service.SecurityPolicies.List). Shape (after JSON round-
// trip of []*computeapi.SecurityPolicy):
//
//	[ { name, description, type, rules: [ ... ] } ]
//
// gcp_cloud_armor is a bare boolean in lib/stack/ir.ts — no designable
// config. Emit policyCount + first policy name + type (CLOUD_ARMOR /
// CLOUD_ARMOR_EDGE / CLOUD_ARMOR_NETWORK). rules[] is included on the
// list summary; surface the count as a drift signal.
//
// TODO(#928): fan out describe-policy per name to surface
// the rule set and default action. Current extractor surfaces drift
// identity + rule count only.
func extractGCPCloudArmorConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "items")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"policyCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "name"); v != "" {
		cfg["policyName"] = v
	}
	if v := getString(first, "type"); v != "" {
		cfg["policyType"] = v
	}
	if rules := toSliceOfMaps(first["rules"]); len(rules) > 0 {
		cfg["ruleCount"] = strconv.Itoa(len(rules))
	}
	return cfg
}

// extractGCPIdentityPlatformConfig extracts config from the list-tenants
// response (inspectGCPIdentityPlatform →
// identitytoolkit.Projects.Tenants.List, paginated, capped at
// identityPlatformMaxTenants=1000 with a TRUNCATED log). Shape (after
// JSON round-trip of []*identitytoolkit.GoogleCloudIdentitytoolkitAdminV2Tenant):
//
//	[ { name, displayName, allowPasswordSignup, enableEmailLinkSignin,
//	    mfaConfig: { state, enabledProviders[] }, testPhoneNumbers, ... } ]
//
// frontend fields (lib/stack/ir.ts:510-517): signInMethods[], mfaRequired.
// signInMethods isn't on the tenant shape as a single array — it's
// distributed across booleans (allowPasswordSignup, enableEmailLinkSignin)
// plus the project-level list-providers response. Surface the tenant-
// level booleans so drift fires when operators flip them.
//
// mfaRequired maps to mfaConfig.state == "ENABLED" (the proto enum has
// STATE_UNSPECIFIED | DISABLED | ENABLED).
func extractGCPIdentityPlatformConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "tenants")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"tenantCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["tenantName"] = v
	}
	if v := getString(first, "displayName"); v != "" {
		cfg["displayName"] = v
	}
	if v := boolStr(first, "allowPasswordSignup"); v != "" {
		cfg["allowPasswordSignup"] = v
	}
	if v := boolStr(first, "enableEmailLinkSignin"); v != "" {
		cfg["enableEmailLinkSignin"] = v
	}
	if mfa := toMap(first["mfaConfig"]); mfa != nil {
		switch getString(mfa, "state") {
		case "ENABLED":
			cfg["mfaRequired"] = "Yes"
		case "DISABLED":
			cfg["mfaRequired"] = "No"
		}
	}
	return cfg
}

// extractGCPVPCConfig extracts config from the list-networks response
// (inspectGCPVPC → computeapi.Service.Networks.List). Shape (after JSON
// round-trip of []*computeapi.Network):
//
//	[ { name, autoCreateSubnetworks, routingConfig: {routingMode},
//	    subnetworks: [ <url>... ], id } ]
//
// gcp_vpc is a bare boolean in lib/stack/ir.ts — no designable config.
// Unlike AWS (where extractVPCConfig surfaces public/private via a
// second describe-internet-gateways call), GCP networks don't have a
// trivial public/private flip — IGW-equivalent is per-subnetwork and
// per-firewall. Emit drift identity: networkCount + first name +
// autoCreateSubnetworks + routingMode + subnetwork count.
//
// The subnetworks[] array on the Network summary is a list of resource
// URLs (not full subnet objects); surface the count as a drift signal.
func extractGCPVPCConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "items")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"networkCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "name"); v != "" {
		cfg["networkName"] = v
	}
	if v := boolStr(first, "autoCreateSubnetworks"); v != "" {
		cfg["autoCreateSubnetworks"] = v
	}
	if rc := toMap(first["routingConfig"]); rc != nil {
		if v := getString(rc, "routingMode"); v != "" {
			cfg["routingMode"] = v
		}
	}
	if subs, ok := first["subnetworks"].([]any); ok {
		cfg["subnetworkCount"] = strconv.Itoa(len(subs))
	}
	return cfg
}

// extractGCPLoadBalancerConfig extracts config from the list-url-maps
// response (inspectGCPLoadBalancer →
// computeapi.Service.UrlMaps.List). Shape (after JSON round-trip of
// []*computeapi.UrlMap):
//
//	[ { name, defaultService, hostRules: [ ... ], pathMatchers: [ ... ],
//	    id, creationTimestamp } ]
//
// gcp_loadbalancer is a bare boolean in lib/stack/ir.ts — no designable
// config. Emit drift identity: urlMapCount + first map name +
// defaultService (basename) + host-rule count so the LLM can answer
// "how many domains are wired up?" without a second API call.
//
// TODO(#928): fan out list-backend-services to surface
// protocol + scheme + ssl policy per URL map. Current extractor
// surfaces URL-map identity only.
func extractGCPLoadBalancerConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "items")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"urlMapCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "name"); v != "" {
		cfg["urlMapName"] = v
	}
	if v := gcpResourceBasename(getString(first, "defaultService")); v != "" {
		cfg["defaultService"] = v
	}
	if hrs, ok := first["hostRules"].([]any); ok {
		cfg["hostRuleCount"] = strconv.Itoa(len(hrs))
	}
	return cfg
}

// extractGCPCloudLoggingConfig extracts config from the list-logs
// response (inspectGCPLogging → logadmin.NewClient.Logs). Inspector
// returns []string of log names. Envelope key for fixture round-trips:
// "logs".
//
// frontend field (lib/stack/ir.ts:503-509): retentionDays. Retention
// lives on log buckets (`_Default`, `_Required`, custom), which is a
// DIFFERENT API (list-buckets, not list-logs) — logadmin.NewClient
// doesn't surface it. TODO(#928): add a list-log-buckets
// action to inspectGCPLogging and fan out to surface retention.
//
// Current extractor emits drift identity only: logCount + first log name.
func extractGCPCloudLoggingConfig(rawResult any) map[string]string {
	names := stringSliceFromEnvelope(rawResult, "logs")
	if len(names) == 0 {
		return nil
	}
	cfg := map[string]string{
		"logCount": strconv.Itoa(len(names)),
	}
	if names[0] != "" {
		cfg["logName"] = names[0]
	}
	return cfg
}

// extractGCPCloudBuildConfig extracts config from the list-triggers
// response (inspectGCPCloudBuild →
// cloudbuild.NewClient.ListBuildTriggers). Shape (after JSON round-
// trip of []*cloudbuildpb.BuildTrigger):
//
//	[ { name, description, github: {owner, name, push: {branch}},
//	    filename, disabled, createTime, ... } ]
//
// gcp_cloud_build is a bare boolean in lib/stack/ir.ts — no designable
// config. Emit drift identity: triggerCount, first trigger name +
// filename, and github-repo identity when present (owner + repo) so
// the LLM can answer "what repo is CI wired up to?" without a second
// describe call. disabled is useful as a drift signal when a trigger
// was accidentally paused.
//
// Note on list-builds default (NOT this action): inspectGCPCloudBuild
// also supports list-builds which paginates capped at
// cloudBuildMaxBuilds=100 (newest-first). list-triggers has no such
// cap — the ListBuildTriggers API returns all triggers for the
// project in one iterator.
func extractGCPCloudBuildConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "triggers")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"triggerCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	// Basename mirrors every other GCP extractor's identity emission —
	// the trigger's full path (projects/<id>/triggers/<name>) is noisy
	// in the UI and the basename is what design-time configs reference.
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["triggerName"] = v
	}
	if v := getString(first, "filename"); v != "" {
		cfg["filename"] = v
	}
	if v := boolStr(first, "disabled"); v != "" {
		cfg["disabled"] = v
	}
	if gh := toMap(first["github"]); gh != nil {
		if owner := getString(gh, "owner"); owner != "" {
			if repo := getString(gh, "name"); repo != "" {
				cfg["githubRepo"] = owner + "/" + repo
			} else {
				cfg["githubRepo"] = owner
			}
		}
	}
	return cfg
}

// extractGCPVertexAIConfig extracts config from the list-endpoints
// response (inspectGCPVertexAI → aiplatform.NewEndpointClient.ListEndpoints).
// Shape (after JSON round-trip of []*aiplatformpb.Endpoint):
//
//	[ { name (projects/<id>/locations/<region>/endpoints/<id>),
//	    displayName, deployedModels: [...], trafficSplit: {...},
//	    etag, createTime, updateTime } ]
//
// gcp_vertex_ai is a bare boolean in lib/stack/ir.ts — no designable
// config. The handler is REGION-SCOPED: it defaults to us-central1 and
// warns when the default returns empty. Emit the region (parsed from
// the first endpoint's full name) so drift fires when an operator
// deployed to a non-default region without updating the design-time
// expectation.
//
// Also surface deployedModelCount — a Vertex AI endpoint serves 0..N
// deployed models, and "deployed 0 models" is the most common silent
// failure mode (endpoint provisioned but model upload failed).
func extractGCPVertexAIConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "endpoints")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"endpointCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	fullName := getString(first, "name")
	if v := gcpResourceBasename(fullName); v != "" {
		cfg["endpointName"] = v
	}
	if v := extractGCPLocationFromName(fullName); v != "" {
		cfg["region"] = v
	}
	if v := getString(first, "displayName"); v != "" {
		cfg["displayName"] = v
	}
	if dms, ok := first["deployedModels"].([]any); ok {
		cfg["deployedModelCount"] = strconv.Itoa(len(dms))
	}
	return cfg
}

// extractGCPCloudMonitoringConfig extracts config from the
// list-alert-policies response (inspectGCPCloudMonitoring →
// monitoring.AlertPolicyClient.ListAlertPolicies). Shape (after JSON
// round-trip of []*monitoringpb.AlertPolicy):
//
//	[ { name (projects/<id>/alertPolicies/<id>), displayName, enabled (bool),
//	    combiner, conditions[], notificationChannels[] } ]
//
// gcp_cloud_monitoring is a bare boolean in lib/stack/ir.ts — no designable
// config — but the panel still wants live signal. policyCount answers "is
// the user actually using monitoring?", and enabledCount catches the silent
// regression where a policy gets disabled but never deleted.
func extractGCPCloudMonitoringConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "alertPolicies")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"policyCount": strconv.Itoa(len(items)),
	}
	enabled := 0
	for _, p := range items {
		if boolStr(p, "enabled") == "Yes" {
			enabled++
		}
	}
	cfg["enabledCount"] = strconv.Itoa(enabled)
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["policyName"] = v
	}
	if v := getString(first, "displayName"); v != "" {
		cfg["displayName"] = v
	}
	return cfg
}

// extractGCPCloudFunctionsConfig extracts config from the list-functions
// response (inspectGCPCloudFunctions → functions.NewFunctionClient.
// ListFunctions across `locations/-`). Shape (after JSON round-trip of
// []*functionspb.Function):
//
//	[ { name (projects/<p>/locations/<l>/functions/<n>), state,
//	    buildConfig: { runtime, entryPoint, source: {...} },
//	    serviceConfig: { uri, availableMemory, timeoutSeconds, ... } } ]
//
// gcp_cloud_functions in lib/stack/ir.ts has fields runtime, memoryMb,
// timeoutSeconds. Surface those + identity so drift fires on a runtime
// downgrade or an OOM-tighten that operators forgot to bring back to
// design time.
func extractGCPCloudFunctionsConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "functions")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"functionCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["functionName"] = v
	}
	if v := getString(first, "state"); v != "" {
		cfg["state"] = v
	}
	if bc := toMap(first["buildConfig"]); bc != nil {
		if v := getString(bc, "runtime"); v != "" {
			cfg["runtime"] = v
		}
	}
	return cfg
}

// extractGCPAPIGatewayConfig extracts config from the list-apis response
// (inspectGCPAPIGateway → apigateway.Client.ListApis under
// `locations/global`). Shape (after JSON round-trip of []*apigatewaypb.Api):
//
//	[ { name (projects/<p>/locations/global/apis/<id>), displayName,
//	    state, managedService, createTime, updateTime } ]
//
// gcp_api_gateway in lib/stack/ir.ts is a bare boolean — no designable
// config — but the panel still wants live signal. Surface identity + state
// so drift fires when a deployed API ends up DELETING / FAILED.
func extractGCPAPIGatewayConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "apis")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"apiCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["apiName"] = v
	}
	if v := getString(first, "displayName"); v != "" {
		cfg["displayName"] = v
	}
	if v := getString(first, "state"); v != "" {
		cfg["state"] = v
	}
	return cfg
}

// extractGCPGitHubActionsConfig extracts config from the
// list-workload-identity-pools response (inspectIAM →
// iam.NewService.Projects.Locations.WorkloadIdentityPools.List). Shape
// (after JSON round-trip of []*iam.WorkloadIdentityPool):
//
//	[ { name (projects/<p>/locations/global/workloadIdentityPools/<id>),
//	    displayName, description, state, disabled } ]
//
// gcp_github_actions in lib/stack/ir.ts is a bare boolean — no
// designable config — but the panel still wants live signal. Surface
// identity + state + the security-load-bearing disabled flag so drift
// fires when a deployed WIF pool is disabled out-of-band (the global
// kill-switch every downstream federation workflow depends on).
//
// Bundle (#606): part of the gcp/github_actions full-fidelity
// follow-up for the v1 preset (#605). Replaces the
// configExtractorAllowlist [no-inspector] entry for gcp_github_actions.
func extractGCPGitHubActionsConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "workloadIdentityPools")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"poolCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := gcpResourceBasename(getString(first, "name")); v != "" {
		cfg["poolName"] = v
	}
	if v := getString(first, "displayName"); v != "" {
		cfg["displayName"] = v
	}
	if v := getString(first, "state"); v != "" {
		cfg["state"] = v
	}
	if v := boolStr(first, "disabled"); v != "" {
		cfg["disabled"] = v
	}
	return cfg
}

// extractGCPBastionConfig extracts config from the list-bastion-instances
// response (inspectGCPBastion → compute.InstancesRESTClient.AggregatedList
// filtered to labels.role=bastion). Shape mirrors gcp_compute since
// bastions ARE GCE instances; reuse the compute extractor's field reads
// to keep the deployed-vs-designed surface consistent.
func extractGCPBastionConfig(rawResult any) map[string]string {
	items := sliceFromEnvelope(rawResult, "instances")
	if len(items) == 0 {
		return nil
	}
	cfg := map[string]string{
		"instanceCount": strconv.Itoa(len(items)),
	}
	first := items[0]
	if v := getString(first, "name"); v != "" {
		cfg["instanceName"] = v
	}
	if v := gcpResourceBasename(getString(first, "machineType")); v != "" {
		cfg["machineType"] = v
	}
	if v := gcpResourceBasename(getString(first, "zone")); v != "" {
		cfg["zone"] = v
	}
	if v := getString(first, "status"); v != "" {
		cfg["status"] = v
	}
	return cfg
}
