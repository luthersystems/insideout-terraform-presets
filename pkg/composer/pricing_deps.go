package composer

// PriceGuidanceVersion identifies the current pricing prompt/table generation.
// Bump this when aws_prices.json, gcp_prices.json, or the pricing system prompt
// change in a way that invalidates previously-computed prices. Carry-forward is
// bypassed when the prior version's stamped guidance version differs.
const PriceGuidanceVersion = "v1"

// PricingDependencies maps a component to the components whose configuration
// or selection influences its price. Distinct from ImplicitDependencies, which
// captures compositional wiring. The classic example: CloudWatch's bill scales
// with the number of services emitting metrics and logs, so any change to
// Lambda/RDS/etc. must force CloudWatch to be repriced even though the
// CloudWatch component itself did not change.
//
// Semantics: PricingDependencies[X] = [Y...] means "X's price depends on Y".
// When Y is in the changed set, X is added to the reprice set.
//
// Start conservative. Add entries as repricing jitter reveals new couplings.
var PricingDependencies = map[ComponentKey][]ComponentKey{
	// AWS observability scales with emitting services.
	KeyAWSCloudWatchMonitoring: {
		KeyAWSLambda,
		KeyAWSRDS,
		KeyAWSECS,
		KeyAWSEKS,
		KeyAWSEC2,
		KeyAWSALB,
		KeyAWSAPIGateway,
		KeyAWSElastiCache,
		KeyAWSOpenSearch,
		KeyAWSDynamoDB,
		KeyAWSSQS,
		KeyAWSMSK,
		KeyAWSBastion,
	},
	KeyAWSCloudWatchLogs: {
		KeyAWSLambda,
		KeyAWSRDS,
		KeyAWSECS,
		KeyAWSEKS,
		KeyAWSEC2,
		KeyAWSALB,
		KeyAWSAPIGateway,
		KeyAWSBastion,
	},
	// AWS backups scale with the number and size of backed-up stores.
	KeyAWSBackups: {
		KeyAWSRDS,
		KeyAWSElastiCache,
		KeyAWSDynamoDB,
		KeyAWSS3,
		KeyAWSEC2,
	},

	// GCP equivalents.
	KeyGCPCloudMonitoring: {
		KeyGCPCloudFunctions,
		KeyGCPCloudRun,
		KeyGCPGKE,
		KeyGCPCompute,
		KeyGCPCloudSQL,
		KeyGCPLoadbalancer,
		KeyGCPAPIGateway,
		KeyGCPMemorystore,
		KeyGCPPubSub,
		KeyGCPFirestore,
		KeyGCPBastion,
	},
	KeyGCPCloudLogging: {
		KeyGCPCloudFunctions,
		KeyGCPCloudRun,
		KeyGCPGKE,
		KeyGCPCompute,
		KeyGCPCloudSQL,
		KeyGCPLoadbalancer,
		KeyGCPAPIGateway,
		KeyGCPBastion,
	},
	KeyGCPBackups: {
		KeyGCPCloudSQL,
		KeyGCPGCS,
		KeyGCPCompute,
	},
}

// reversePricingDeps builds a lookup Y -> {X : Y ∈ PricingDependencies[X]}.
// Computed once at package init since PricingDependencies is immutable at runtime.
var reversePricingDeps = func() map[ComponentKey][]ComponentKey {
	r := make(map[ComponentKey][]ComponentKey, len(PricingDependencies))
	for consumer, deps := range PricingDependencies {
		for _, dep := range deps {
			r[dep] = append(r[dep], consumer)
		}
	}
	return r
}()

// RepriceSet returns the transitive closure of components that must be
// repriced given a set of changed components. The result always contains the
// input set plus every component whose PricingDependencies list contains any
// changed component (and transitively, any component reachable via reverse
// pricing-dep edges).
//
// A nil or empty input returns an empty set.
func RepriceSet(changed map[ComponentKey]bool) map[ComponentKey]bool {
	out := make(map[ComponentKey]bool, len(changed))
	if len(changed) == 0 {
		return out
	}
	queue := make([]ComponentKey, 0, len(changed))
	for k := range changed {
		out[k] = true
		queue = append(queue, k)
	}
	for len(queue) > 0 {
		y := queue[0]
		queue = queue[1:]
		for _, consumer := range reversePricingDeps[y] {
			if !out[consumer] {
				out[consumer] = true
				queue = append(queue, consumer)
			}
		}
	}
	return out
}
