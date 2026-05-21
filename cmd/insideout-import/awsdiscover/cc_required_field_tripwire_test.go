package awsdiscover

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestCloudControlRequiredFieldTripwire is a static CI gate for the
// "CloudControl cannot read back a required field" bug class — the
// class behind #661 / #662 / #664 and tracked by #665.
//
// The bug: a Terraform type routed through the generic CloudControl
// enricher has a REQUIRED argument that cloudcontrol:GetResource does
// not return (the CFN handler treats it as create-time input). The
// enricher fail-opens, the composed HCL ships an empty required arg,
// and `terraform plan` blows up at the customer — not at compose time.
//
// This bug is NOT statically declarable: the CFN registry schema for
// AWS::IAM::ManagedPolicy does not mark PolicyDocument write-only, yet
// the handler never returns it. So no check can PROVE correctness from
// static data — only a live GetResource probe can (see the
// build-tagged live probe in cc_required_field_live_test.go).
//
// What this test DOES do is freeze the at-risk surface. It enumerates
// every CloudControl-routed type that (a) has no hand-rolled enricher
// override and (b) has at least one REQUIRED schema field, and diffs
// that set against testdata/cc_required_fields.golden. When a new
// CloudControl type is added — or an existing one gains a required
// field — this test fails, forcing a human to answer: "does
// GetResource actually return this required field?" If yes, re-seed
// the golden. If no, the type needs a hand-rolled enricher (the #661
// fix shape). Either way the gap can no longer ship silently, which is
// exactly how #661 shipped.
//
// Re-seed after an intentional change:
//
//	UPDATE_GOLDEN=1 go test ./cmd/insideout-import/awsdiscover/ \
//	    -run TestCloudControlRequiredFieldTripwire
func TestCloudControlRequiredFieldTripwire(t *testing.T) {
	t.Parallel()

	// NewAWSDiscoverer issues no AWS calls; it just wires the registry.
	d := NewAWSDiscoverer(aws.Config{Region: "us-east-1"})

	var lines []string
	for _, cfg := range cloudControlTypeConfigs {
		tf := cfg.TFType
		enr, ok := d.byTypeEnricher[tf]
		if !ok {
			continue
		}
		// A hand-rolled override (or the lambda composite) owns its own
		// required-field correctness and is covered by its own tests —
		// only the generic CloudControl path is at risk here.
		if _, isGenericCC := enr.(*cloudControlEnricher); !isGenericCC {
			continue
		}
		_, schema, found := generated.Lookup(tf)
		if !found {
			continue
		}
		var req []string
		for name, fs := range schema {
			if fs.Required {
				req = append(req, name)
			}
		}
		if len(req) == 0 {
			continue
		}
		sort.Strings(req)
		lines = append(lines, tf+": "+strings.Join(req, ", "))
	}
	sort.Strings(lines)
	current := strings.Join(lines, "\n") + "\n"

	goldenPath := filepath.Join("testdata", "cc_required_fields.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(current), 0o644))
		t.Logf("wrote golden: %s (%d CloudControl-routed types with required fields)", goldenPath, len(lines))
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err,
		"golden missing — seed with `UPDATE_GOLDEN=1 go test ./cmd/insideout-import/awsdiscover/ -run TestCloudControlRequiredFieldTripwire`")
	require.Equal(t, string(want), current,
		"The set of CloudControl-routed types with REQUIRED fields and no hand-rolled "+
			"enricher override changed.\n\n"+
			"This is the #661/#665 bug-class tripwire. For every ADDED line below, verify "+
			"that cloudcontrol:GetResource actually returns the listed required field(s) "+
			"against a real resource — the CFN schema does NOT reliably declare this (see "+
			"AWS::IAM::ManagedPolicy.PolicyDocument). If GetResource omits a required field, "+
			"the type needs a hand-rolled enricher (the #661 fix shape: a per-type "+
			"AttributeEnricher in byTypeEnricher). If GetResource does return it, re-seed "+
			"the golden:\n\n"+
			"  UPDATE_GOLDEN=1 go test ./cmd/insideout-import/awsdiscover/ -run TestCloudControlRequiredFieldTripwire\n\n"+
			"golden: "+goldenPath)
}
