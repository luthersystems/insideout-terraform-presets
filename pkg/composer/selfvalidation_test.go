package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Guard B — class-level "composer never self-rejects its own default output".
//
// The #393/#805 NAT-gateway bug was ONE instance of a CLASS: a cross-component
// DEFAULT value drifting out of sync with a VALIDATION rule, so the composer
// emits a default config that its OWN validator (the mapper) rejects — which
// reliable then surfaces as a red "Terraform Error". The NAT regression tests
// in coherence_test.go pin the NAT instance specifically; Guard B catches the
// WHOLE class, including future, non-NAT drift.
//
// It does so by enumerating EVERY component key programmatically
// (AllComponentKeys — components added later are auto-covered) and, for each,
// constructing a minimally-valid DEFAULT-config stack, running the exact
// default+coherence pipeline reliable applies, then sweeping the mapper over
// every selected component and asserting NONE self-rejects. The invariant under
// test is specifically: "defaulting must not INTRODUCE a validation failure."
//
// Guard B already earned its keep: it discovered a SECOND instance of the class
// (aws_lambda's bare-integer "3" Timeout default tripping the mapper's strict
// duration parser), fixed alongside it in defaults.go
// (overrideLambdaTimeoutUnitDefault).

// derivedComponentKeys lists ComponentKeys that legitimately have NO selecting
// field on the Components struct because they are auto-included by
// ResolveDependenciesForCompose rather than selected directly. Guard B cannot
// mark them on a Components value, and that is EXPECTED — not a coverage gap.
// Every entry MUST carry a comment justifying why it has no field.
//
// Keep this list MINIMAL. A new key that the harness cannot place and is NOT in
// this list fails Guard B LOUDLY (buildDefaultStackForKey t.Fatalf), forcing a
// human to wire it into selectComponentOnto or document it here — never a
// silent skip.
var derivedComponentKeys = map[ComponentKey]bool{
	// aws_eks_nodegroup has no standalone Components field; it is auto-included
	// whenever aws_eks is selected (ResolveDependenciesForCompose, issue #206),
	// and ComponentSelected(c, KeyAWSEKSNodeGroup) is false by design.
	KeyAWSEKSNodeGroup: true,
}

// selectComponentOnto marks `key` as selected on the shared Components value
// `c`, mirroring the per-key rules of selectingComponentsFor but mutating in
// place so an entire dependency closure can be assembled onto ONE Components.
// The three string-typed keys and the two backup-struct keys are handled
// explicitly; every other key is a *bool selection placed reflectively by json
// tag (setPointerToBoolTrueByTag) — so new *bool components are auto-covered.
func selectComponentOnto(c *Components, key ComponentKey) {
	switch key {
	case KeyAWSVPC:
		c.AWSVPC = "Public VPC"
	case KeyAWSEC2:
		c.AWSEC2 = "Intel"
	case KeyGCPCompute:
		c.GCPCompute = "n2-standard-2"
	case KeyAWSBackups:
		c.AWSBackups = selectingComponentsFor(KeyAWSBackups).AWSBackups
	case KeyGCPBackups:
		c.GCPBackups = selectingComponentsFor(KeyGCPBackups).GCPBackups
	default:
		setPointerToBoolTrueByTag(c, string(key))
	}
}

// buildDefaultStackForKey assembles a minimally-valid, DEFAULT-config stack
// that selects `primary` plus its mandatory dependency closure (a VPC/network
// where the component requires one — supplied by ResolveDependenciesForCompose
// via ImplicitDependencies), then runs the EXACT default+coherence pipeline
// reliable applies before composing a stack:
//
//	StripOrphanConfig -> DeriveCrossComponentFields -> ComputePresetDefaults ->
//	MergeConfigs -> DeriveCrossComponentFields
//
// cfg starts EMPTY (no user-authored fields) so ONLY the composer's own
// defaults populate it — that is the whole point: we are testing the composer's
// own output, not a user's. It returns the resolved comps, cfg, and the full
// dependency-expanded selected key set, ready for the mapper sweep.
//
// Loud-failure contract: every resolved key must be placeable onto Components
// (or be a documented derivedComponentKeys entry). A NEW key the harness cannot
// place is a t.Fatalf, never a silent skip.
func buildDefaultStackForKey(t *testing.T, primary ComponentKey) (*Components, *Config, []ComponentKey) {
	t.Helper()

	cloud := strings.ToUpper(CloudFor(primary)) // "AWS" / "GCP"
	selected := ResolveDependenciesForCompose([]ComponentKey{primary}, nil)

	comps := &Components{Cloud: cloud}
	for _, k := range selected {
		selectComponentOnto(comps, k)
	}
	for _, k := range selected {
		if !ComponentSelected(comps, k) && !derivedComponentKeys[k] {
			t.Fatalf("Guard B cannot place component %q onto a Components value "+
				"(while building the default stack for %q): wire it into "+
				"selectComponentOnto, or — if it is genuinely field-less and "+
				"dependency-derived — document it in derivedComponentKeys. "+
				"An unplaceable key is a coverage failure, not a pass.", k, primary)
		}
	}

	cfg := &Config{Cloud: cloud}
	c := New() // real embedded preset FS, so real HCL defaults flow through
	StripOrphanConfig(comps, cfg)
	DeriveCrossComponentFields(comps, cfg)
	overlay, err := c.ComputePresetDefaults(*cfg, comps, selected)
	require.NoErrorf(t, err,
		"ComputePresetDefaults must not error for a default-config stack selecting %q", primary)
	MergeConfigs(cfg, &overlay)
	DeriveCrossComponentFields(comps, cfg)
	return comps, cfg, selected
}

// TestComposer_DefaultOutput_NeverSelfRejects is GUARD B. For every component
// key in AllComponentKeys it builds a default-config stack (key + minimal
// deps), runs the full default+coherence pipeline, and sweeps the mapper over
// every selected component — asserting the composer never produces a default
// its own validator rejects.
//
// To prove this is a real guard (not a vacuous pass): comment out the
// overrideNATGatewayDefaultForPrivateSubnets call in defaults.go and the seven
// needs-private subtests (aws_eks/ecs/rds/elasticache/opensearch/ec2 +
// nodegroup) fail with the EnableNATGateway=false self-reject; comment out
// overrideLambdaTimeoutUnitDefault and the aws_lambda / aws_bedrock_agent /
// aws_agentcore_gateway subtests fail with the bare-integer Timeout self-reject.
func TestComposer_DefaultOutput_NeverSelfRejects(t *testing.T) {
	t.Parallel()

	for _, primary := range AllComponentKeys {
		primary := primary
		t.Run(string(primary), func(t *testing.T) {
			t.Parallel()

			comps, cfg, selected := buildDefaultStackForKey(t, primary)

			// Sweep the mapper over EVERY selected component. The NAT bug was a
			// KeyAWSVPC self-reject; the Lambda-timeout bug a KeyAWSLambda one.
			// Any component whose mapper rejects a value the composer's OWN
			// defaulting produced is the class under test.
			for _, k := range selected {
				_, err := (DefaultMapper{}).BuildModuleValues(k, comps, cfg, "test", "us-east-1")
				require.NoErrorf(t, err,
					"composer self-rejected its OWN default output: the default-config "+
						"stack %v (primary %q) produced a config that component %q's mapper "+
						"rejects. This is a cross-component default/validation drift "+
						"(class of #393/#805) — fix the COMPUTED default in defaults.go, "+
						"do NOT relax the validator.",
					selected, primary, k)
			}

			// Targeted NAT-class tripwire: if the resolved default ends with
			// EnableNATGateway set, it must be consistent with the validator —
			// never false while the stack needs private subnets. This trips if
			// someone adds a key to the needs-private set (stackNeedsPrivate
			// Subnets) without wiring the component-aware NAT default.
			if cfg.AWSVPC != nil && cfg.AWSVPC.EnableNATGateway != nil && stackNeedsPrivateSubnets(comps) {
				assert.Truef(t, *cfg.AWSVPC.EnableNATGateway,
					"needs-private stack %v resolved its OWN EnableNATGateway default to "+
						"false — the mapper fail-fast would reject it (#393/#805)", selected)
			}
		})
	}
}
