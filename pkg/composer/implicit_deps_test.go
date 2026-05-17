package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImplicitDependencies_GCPCompute_AutoWiresVPC pins the GCP services
// that consume the VPC at apply time. Each entry below was a silent
// apply-time failure before issue #600: selecting the service without
// also selecting gcp/vpc left dangling network references that only
// surfaced once terraform reached the cloud API.
//
// KeyGCPGKE → KeyGCPVPC was the existing template; the rest were
// backfilled in #600. KeyGCPCloudArmor → KeyGCPLoadbalancer is the
// parallel for the LB-only attachment point.
func TestImplicitDependencies_GCPServices_AutoWireVPC(t *testing.T) {
	t.Parallel()

	cases := []struct {
		consumer ComponentKey
		need     ComponentKey
		reason   string
	}{
		// Templates (pre-existing) — pinned so the table reads as a complete
		// contract for every GCP service that auto-includes the VPC, not
		// just the new #600 rows. Deleting any row in
		// `ImplicitDependencies` here would survive without these guards.
		{KeyGCPGKE, KeyGCPVPC, "GKE clusters require a VPC for the cluster network"},
		{KeyGCPCompute, KeyGCPVPC, "GCE VM instances attach to a subnetwork in the VPC"},

		// Issue #600 backfill.
		{KeyGCPVertexAI, KeyGCPVPC, "Vertex AI private endpoints peer with the VPC via servicenetworking"},
		{KeyGCPCloudFunctions, KeyGCPVPC, "Cloud Functions Gen 2 with VPC egress needs the serverless VPC connector"},
		{KeyGCPCloudRun, KeyGCPVPC, "Cloud Run with vpc_access_connector needs the serverless VPC connector"},
		{KeyGCPCloudBuild, KeyGCPVPC, "Cloud Build private worker pools peer with the customer VPC"},
		{KeyGCPCloudArmor, KeyGCPLoadbalancer, "Cloud Armor only attaches to backend services on an HTTPS LB"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.consumer)+"_needs_"+string(tc.need), func(t *testing.T) {
			t.Parallel()

			deps, ok := ImplicitDependencies[tc.consumer]
			require.True(t, ok,
				"ImplicitDependencies[%s] must be declared (%s)", tc.consumer, tc.reason)
			assert.Contains(t, deps, tc.need,
				"%s must implicitly depend on %s — %s", tc.consumer, tc.need, tc.reason)

			// ResolveDependencies must hand back tc.need alongside tc.consumer
			// when only the consumer is selected — this is the user-visible
			// contract.
			resolved := ResolveDependencies([]ComponentKey{tc.consumer})
			assert.Contains(t, resolved, tc.need,
				"ResolveDependencies([%s]) must auto-include %s", tc.consumer, tc.need)
			assert.Contains(t, resolved, tc.consumer,
				"ResolveDependencies([%s]) must preserve the original consumer", tc.consumer)
		})
	}
}

// TestImplicitDependencies_ComposeOrderRespected verifies that for every
// (consumer → dependency) entry, the dependency appears before the
// consumer in ComposeOrder. Terraform composes modules in declared
// order; if a dependency landed after its consumer, the consumer's
// module block would reference a not-yet-declared module.
//
// This is the structural invariant that backstops the new #600 entries —
// any future addition to ImplicitDependencies that violates ordering
// fails this test instead of failing silently at apply time.
func TestImplicitDependencies_ComposeOrderRespected(t *testing.T) {
	t.Parallel()

	position := make(map[ComponentKey]int, len(ComposeOrder))
	for i, k := range ComposeOrder {
		position[k] = i
	}

	for consumer, deps := range ImplicitDependencies {
		consumerPos, consumerInOrder := position[consumer]
		if !consumerInOrder {
			// Not every ComponentKey participates in ComposeOrder (e.g. the
			// pseudo-components Arch/Cloud/Composer). Skip — ordering is
			// only meaningful for keys the composer actually emits.
			continue
		}
		for _, dep := range deps {
			depPos, depInOrder := position[dep]
			if !depInOrder {
				t.Errorf("ImplicitDependencies[%s] references %s which is missing from ComposeOrder",
					consumer, dep)
				continue
			}
			assert.Less(t, depPos, consumerPos,
				"ComposeOrder violation: %s (pos=%d) must precede consumer %s (pos=%d) — implicit deps must compose first",
				dep, depPos, consumer, consumerPos)
		}
	}
}
