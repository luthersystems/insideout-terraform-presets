package composer

// coherence.go owns the rules that make a (Components, Config) pair coherent:
//
//   - StripOrphanConfig clears per-component config sub-blocks whose component
//     is no longer selected. Orphan config sub-blocks otherwise outlive the
//     component that produced them and leak into deploy payloads (the NAT-on-
//     Public-VPC apply failure on sess_v2_CnqUJ6NRJnLC, luthersystems/reliable#1435).
//
//   - DeriveCrossComponentFields recomputes config fields whose correct value
//     is a function of which OTHER components are selected — today the AWS
//     VPC topology knobs whose preset HCL default of NAT=true must NOT survive
//     onto a stack that no longer needs private subnets.
//
// Both functions are pure. They are intended to be composed around
// (*Client).ApplyPresetDefaults — strip + derive BEFORE the backfill catches
// "stale-then-removed" carry-forward, and AFTER the backfill catches "fresh
// allocation" cases where ApplyPresetDefaults just landed NAT=true onto a
// Public-VPC-only stack.
//
// Tracked under luthersystems/reliable#1437.

import "reflect"

// ComponentSelected reports whether the given component is selected on the
// provided Components value. Pointer-typed components require non-nil AND
// *true (so an explicit aws_opensearch=false is treated as deselected).
// String-typed components (AWSVPC, AWSEC2, GCPCompute) require != "".
// Struct-pointer components (AWSBackups, GCPBackups) require != nil; the
// backup config itself encodes the selection.
//
// Returns false for ComponentKeys that don't appear on Components (e.g.,
// KeyAWSEKSNodeGroup — driven by KeyAWSEKS auto-include rather than a
// standalone Components field).
func ComponentSelected(c *Components, key ComponentKey) bool {
	if c == nil {
		return false
	}
	switch key {
	// String-typed selections.
	case KeyAWSVPC:
		return c.AWSVPC != ""
	case KeyAWSEC2:
		return c.AWSEC2 != ""
	case KeyGCPCompute:
		return c.GCPCompute != ""
	// AWS pointer-typed selections.
	case KeyAWSBastion:
		return boolPtrTrue(c.AWSBastion)
	case KeyAWSEKS:
		return boolPtrTrue(c.AWSEKS)
	case KeyAWSECS:
		return boolPtrTrue(c.AWSECS)
	case KeyAWSLambda:
		return boolPtrTrue(c.AWSLambda)
	case KeyAWSAppRunner:
		return boolPtrTrue(c.AWSAppRunner)
	case KeyAWSSageMaker:
		return boolPtrTrue(c.AWSSageMaker)
	case KeyAWSALB:
		return boolPtrTrue(c.AWSALB)
	case KeyAWSCloudfront:
		return boolPtrTrue(c.AWSCloudFront)
	case KeyAWSWAF:
		return boolPtrTrue(c.AWSWAF)
	case KeyAWSAPIGateway:
		return boolPtrTrue(c.AWSAPIGateway)
	case KeyAWSRDS:
		return boolPtrTrue(c.AWSRDS)
	case KeyAWSElastiCache:
		return boolPtrTrue(c.AWSElastiCache)
	case KeyAWSDynamoDB:
		return boolPtrTrue(c.AWSDynamoDB)
	case KeyAWSS3:
		return boolPtrTrue(c.AWSS3)
	case KeyAWSKMS:
		return boolPtrTrue(c.AWSKMS)
	case KeyAWSSecretsManager:
		return boolPtrTrue(c.AWSSecretsManager)
	case KeyAWSOpenSearch:
		return boolPtrTrue(c.AWSOpenSearch)
	case KeyAWSBedrock:
		return boolPtrTrue(c.AWSBedrock)
	case KeyAWSBedrockAgent:
		return boolPtrTrue(c.AWSBedrockAgent)
	case KeyAWSAgentCoreGateway:
		return boolPtrTrue(c.AWSAgentCoreGateway)
	case KeyAWSKendra:
		return boolPtrTrue(c.AWSKendra)
	case KeyAWSSQS:
		return boolPtrTrue(c.AWSSQS)
	case KeyAWSMSK:
		return boolPtrTrue(c.AWSMSK)
	case KeyAWSCloudWatchLogs:
		return boolPtrTrue(c.AWSCloudWatchLogs)
	case KeyAWSCloudWatchMonitoring:
		return boolPtrTrue(c.AWSCloudWatchMonitoring)
	case KeyAWSGrafana:
		return boolPtrTrue(c.AWSGrafana)
	case KeyAWSCognito:
		return boolPtrTrue(c.AWSCognito)
	case KeyAWSGitHubActions:
		return boolPtrTrue(c.AWSGitHubActions)
	case KeyAWSCodeBuild:
		return boolPtrTrue(c.AWSCodeBuild)
	case KeyAWSCodePipeline:
		return boolPtrTrue(c.AWSCodePipeline)
	case KeyAWSRoute53:
		return boolPtrTrue(c.AWSRoute53)
	case KeyAWSACM:
		return boolPtrTrue(c.AWSACM)
	case KeyAWSBackups:
		return c.AWSBackups != nil
	// GCP pointer-typed selections.
	case KeyGCPVPC:
		return boolPtrTrue(c.GCPVPC)
	case KeyGCPBastion:
		return boolPtrTrue(c.GCPBastion)
	case KeyGCPGKE:
		return boolPtrTrue(c.GCPGKE)
	case KeyGCPCloudRun:
		return boolPtrTrue(c.GCPCloudRun)
	case KeyGCPCloudFunctions:
		return boolPtrTrue(c.GCPCloudFunctions)
	case KeyGCPLoadbalancer:
		return boolPtrTrue(c.GCPLoadbalancer)
	case KeyGCPCloudArmor:
		return boolPtrTrue(c.GCPCloudArmor)
	case KeyGCPAPIGateway:
		return boolPtrTrue(c.GCPAPIGateway)
	case KeyGCPCloudSQL:
		return boolPtrTrue(c.GCPCloudSQL)
	case KeyGCPMemorystore:
		return boolPtrTrue(c.GCPMemorystore)
	case KeyGCPFirestore:
		return boolPtrTrue(c.GCPFirestore)
	case KeyGCPGCS:
		return boolPtrTrue(c.GCPGCS)
	case KeyGCPCloudKMS:
		return boolPtrTrue(c.GCPCloudKMS)
	case KeyGCPSecretManager:
		return boolPtrTrue(c.GCPSecretManager)
	case KeyGCPVertexAI:
		return boolPtrTrue(c.GCPVertexAI)
	case KeyGCPAgentEngine:
		return boolPtrTrue(c.GCPAgentEngine)
	case KeyGCPDocumentAI:
		return boolPtrTrue(c.GCPDocumentAI)
	case KeyGCPModelArmor:
		return boolPtrTrue(c.GCPModelArmor)
	case KeyGCPPubSub:
		return boolPtrTrue(c.GCPPubSub)
	case KeyGCPCloudLogging:
		return boolPtrTrue(c.GCPCloudLogging)
	case KeyGCPCloudMonitoring:
		return boolPtrTrue(c.GCPCloudMonitoring)
	case KeyGCPIdentityPlatform:
		return boolPtrTrue(c.GCPIdentityPlatform)
	case KeyGCPCloudBuild:
		return boolPtrTrue(c.GCPCloudBuild)
	case KeyGCPCloudDeploy:
		return boolPtrTrue(c.GCPCloudDeploy)
	case KeyGCPCloudDNS:
		return boolPtrTrue(c.GCPCloudDNS)
	case KeyGCPGitHubActions:
		return boolPtrTrue(c.GCPGitHubActions)
	case KeyGCPBackups:
		return c.GCPBackups != nil
	// External / third-party.
	case KeySplunk:
		return boolPtrTrue(c.Splunk)
	case KeyDatadog:
		return boolPtrTrue(c.Datadog)
	}
	return false
}

// StackNeedsPrivateSubnets reports whether the stack includes any AWS
// component that requires private subnets — and therefore NAT egress. The
// mapper enforces the same predicate to coerce NAT off on Public-VPC stacks
// (see KeyAWSVPC branch in BuildModuleValues). Exporting it lets persistence-
// layer callers (luthersystems/reliable) make the same decision upstream of
// the mapper so cfg.AWSVPC.EnableNATGateway never gets persisted as &true
// on a stack that doesn't need NAT.
func StackNeedsPrivateSubnets(c *Components) bool {
	return stackNeedsPrivateSubnets(c)
}

// StripOrphanConfig clears every cfg.<component> sub-block whose
// corresponding component is NOT selected in `comps`. Idempotent in-place
// mutation. A no-op when cfg is nil or comps is nil (an unknown components
// state cannot safely conclude orphanhood).
//
// Treats "non-nil but empty" sub-structs as orphan when their component is
// unselected — an empty &AWSOpenSearch{} conveys no actual configuration and
// must not survive when opensearch is dropped.
//
// The set of stripped fields is determined reflectively: any *struct sub-
// field on Config whose json tag matches a known ComponentKey is in scope.
// Adding a new component to Components + Config + KeyXxx requires no edit
// here.
//
// **Granularity**: orphan-strip operates at the component-key level. The
// sub-component selections inside cfg.AWSBackups / cfg.GCPBackups (per-store
// frequency/retention entries) are NOT enforced — if comps.AWSBackups is
// non-nil the whole cfg.AWSBackups sub-block survives, even if some inner
// stores (e.g. cfg.AWSBackups.RDS) have no corresponding selection in
// comps.AWSBackups.RDS. Sub-component coherence is a higher-layer concern.
func StripOrphanConfig(comps *Components, cfg *Config) {
	if comps == nil || cfg == nil {
		return
	}
	cfgVal := reflect.ValueOf(cfg).Elem()
	cfgType := cfgVal.Type()
	for i := 0; i < cfgType.NumField(); i++ {
		ft := cfgType.Field(i)
		if ft.Type.Kind() != reflect.Pointer || ft.Type.Elem().Kind() != reflect.Struct {
			continue
		}
		tag := jsonTagName(ft.Tag.Get("json"))
		if tag == "" {
			continue
		}
		key := configTagToKey(tag)
		if !isOrphanStrippableKey(key) {
			continue
		}
		if !ComponentSelected(comps, key) {
			fv := cfgVal.Field(i)
			if fv.CanSet() && !fv.IsNil() {
				fv.Set(reflect.Zero(fv.Type()))
			}
		}
	}
}

// configTagToKey maps a json tag on a Config *struct sub-field to its
// canonical ComponentKey. For most components the tag IS the key
// (cloud-prefixed json tag == ComponentKey string), so the conversion is
// a direct cast. A handful of historical inconsistencies between the
// Config json schema and the ComponentKey enum need an explicit alias —
// listed here so the reflection-based orphan strip recognises them.
//
// Known aliases:
//   - Config.AWSAPIGateway uses json tag "aws_api_gateway" (underscore between
//     "api" and "gateway") to preserve the on-the-wire schema users have
//     persisted, while KeyAWSAPIGateway = "aws_apigateway" (no underscore).
//     Without this alias the orphan-strip silently skips
//     cfg.AWSAPIGateway and leaves stale config behind when API Gateway is
//     removed from the stack.
//
// Add to this map (do NOT change the json tag, which would break persisted
// snapshots) when a new drift is discovered.
func configTagToKey(tag string) ComponentKey {
	switch tag {
	case "aws_api_gateway":
		return KeyAWSAPIGateway
	}
	return ComponentKey(tag)
}

// DeriveCrossComponentFields re-derives cfg fields whose correct value is a
// function of which OTHER components are selected. Today covers the AWS VPC
// topology knobs:
//
//   - cfg.AWSVPC.EnableNATGateway
//   - cfg.AWSVPC.SingleNATGateway
//   - cfg.AWSVPC.AZCount
//
// When no selected component needs private subnets, the three fields are
// cleared. If every field of the AWSVPC sub-block ends up nil, the
// sub-block pointer itself is cleared so json:",omitempty" hides it.
//
// Why both this AND StripOrphanConfig: AWSVPC is selected (the user keeps a
// Public VPC) even when no DOWNSTREAM component needs private subnets. The
// orphan strip cannot help — the VPC component itself is in scope. Cross-
// component derive is the rule that catches "VPC stays, but its NAT-related
// sub-fields no longer have a justification."
//
// ASYMMETRY — derive only ACTS on the !needsPrivate case (it clears NAT). It
// deliberately does NOT force EnableNATGateway=true for the needsPrivate case.
// Three-way contract for EnableNATGateway on a needs-private stack:
//
//   - user-explicit false → preserved here, and the mapper fail-fast rejects it
//     (mapper.go, KeyAWSVPC). Forcing true here would silently flip an explicit
//     false to true AND suppress that intentional error — so we must not.
//   - user-explicit true → preserved here; the mapper emits enable_nat_gateway
//     =true.
//   - user-unset (nil) → the needs-private default of true is injected upstream
//     by ComputePresetDefaults' component-aware overlay
//     (overrideNATGatewayDefaultForPrivateSubnets, #393), NOT here. That layer
//     is zero-only, so it can only ever fill the user's nil field — it can
//     never clobber an explicit value. By the time this derive re-runs after
//     MergeConfigs the field is already &true and this function leaves it alone.
//
// In short: derive turns NAT OFF when nothing needs it; the preset-default
// overlay turns NAT ON (by default, never over an explicit value) when
// something does. Keeping the "on" decision in the overlay is what preserves
// the user-explicit-false → fail-fast guarantee.
//
// Idempotent in-place mutation. Not a pure function in the strict sense
// (it writes through *cfg); calling it twice on the same inputs is a
// no-op on the second call.
func DeriveCrossComponentFields(comps *Components, cfg *Config) {
	if comps == nil || cfg == nil || cfg.AWSVPC == nil {
		return
	}
	if !stackNeedsPrivateSubnets(comps) {
		cfg.AWSVPC.EnableNATGateway = nil
		cfg.AWSVPC.SingleNATGateway = nil
		cfg.AWSVPC.AZCount = nil
		if cfg.AWSVPC.EnableNATGateway == nil &&
			cfg.AWSVPC.SingleNATGateway == nil &&
			cfg.AWSVPC.AZCount == nil {
			cfg.AWSVPC = nil
		}
	}
}

// isOrphanStrippableKey reports whether the given ComponentKey has a per-
// component sub-block on Config that participates in the orphan strip. The
// list mirrors the *struct sub-fields of Config with cloud-prefixed json
// tags. KeyAWSEKSNodeGroup (driven by KeyAWSEKS auto-include) and pure
// non-config components (KeyAWSALB / KeyAWSGrafana / KeyAWSWAF /
// KeySplunk / KeyDatadog / KeyGitHubActions etc.) are NOT in scope —
// there is no cfg.<key> sub-block to strip.
func isOrphanStrippableKey(key ComponentKey) bool {
	switch key {
	case KeyAWSEC2, KeyAWSEKS, KeyAWSECS, KeyAWSVPC,
		KeyAWSCloudfront, KeyAWSRDS, KeyAWSElastiCache,
		KeyAWSS3, KeyAWSDynamoDB, KeyAWSSQS, KeyAWSMSK,
		KeyAWSCloudWatchLogs, KeyAWSCloudWatchMonitoring,
		KeyAWSCognito, KeyAWSLambda, KeyAWSAppRunner, KeyAWSSageMaker, KeyAWSCodeBuild, KeyAWSAPIGateway,
		KeyAWSKMS, KeyAWSSecretsManager, KeyAWSOpenSearch,
		KeyAWSBedrock, KeyAWSBedrockAgent, KeyAWSAgentCoreGateway, KeyAWSKendra, KeyAWSBackups, KeyAWSRoute53,
		KeyAWSACM,
		KeyGCPCompute, KeyGCPGKE, KeyGCPCloudRun,
		KeyGCPCloudFunctions, KeyGCPLoadbalancer,
		KeyGCPCloudSQL, KeyGCPMemorystore, KeyGCPGCS,
		KeyGCPVertexAI, KeyGCPAgentEngine,
		KeyGCPDocumentAI, KeyGCPModelArmor,
		KeyGCPPubSub, KeyGCPCloudLogging,
		KeyGCPIdentityPlatform, KeyGCPAPIGateway, KeyGCPBackups,
		KeyGCPCloudDNS, KeyGCPGitHubActions, KeyGCPCloudDeploy:
		return true
	}
	return false
}
