package reverseimport

import (
	"context"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

// TestMergeClosure_StampsPulledInByProvenance pins the closure-contract
// provenance half (presets#864): a child auto-included because its parent was
// selected must carry a pulled_in_by stamp naming the selection_closure reason
// and the selected parent as its consumer, so reliable's disclosure surface can
// render *why* the child appears (auto_included) without re-deriving it. The
// selected parent itself must NOT be stamped.
func TestMergeClosure_StampsPulledInByProvenance(t *testing.T) {
	selectedBucket := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.selected",
			ImportID: "io-selected",
			Region:   "us-east-1",
		},
	}
	child := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:         "aws",
			Type:          "aws_s3_bucket_versioning",
			Address:       "aws_s3_bucket_versioning.selected",
			ImportID:      "io-selected",
			Region:        "us-east-1",
			ParentAddress: "aws_s3_bucket.selected",
			NativeIDs:     map[string]string{"bucket": "io-selected"},
		},
	}

	merged, _, _ := mergeClosureResources(mergeClosureInput{
		current:         []imported.ImportedResource{selectedBucket},
		selectedParents: []imported.ImportedResource{selectedBucket},
		parentTypes:     []string{"aws_s3_bucket"},
		childTypes:      []string{"aws_s3_bucket_versioning"},
		discovered:      []imported.ImportedResource{selectedBucket, child},
	})

	var checkedChild, checkedParent bool
	for _, r := range merged {
		switch r.Identity.Address {
		case "aws_s3_bucket_versioning.selected":
			checkedChild = true
			if r.PulledInBy == nil {
				t.Fatalf("closure child missing pulled_in_by provenance")
			}
			if r.PulledInBy.Reason != imported.PulledInReasonSelectionClosure {
				t.Errorf("child provenance Reason=%q, want %q", r.PulledInBy.Reason, imported.PulledInReasonSelectionClosure)
			}
			if len(r.PulledInBy.Consumers) != 1 || r.PulledInBy.Consumers[0] != "aws_s3_bucket.selected" {
				t.Errorf("child provenance Consumers=%v, want [aws_s3_bucket.selected]", r.PulledInBy.Consumers)
			}
		case "aws_s3_bucket.selected":
			checkedParent = true
			if r.PulledInBy != nil {
				t.Errorf("selected parent carries pulled_in_by=%+v, want nil (operator-selected)", r.PulledInBy)
			}
		}
	}
	if !checkedChild {
		t.Error("closure child was not present in merged set")
	}
	if !checkedParent {
		t.Error("selected parent was not present in merged set")
	}
}

// capturingDepChase records the depchase.Options it was handed so a test can
// assert what Run threaded through.
type capturingDepChase struct {
	opts depchase.Options
}

func (c *capturingDepChase) run(_ context.Context, opts depchase.Options, resources []imported.ImportedResource) (*depchase.Result, error) {
	c.opts = opts
	return &depchase.Result{Resources: resources}, nil
}

// TestRun_BoundClosureToSelection_PassesScopePolicy pins the run.go wiring
// (presets#864): when Options.BoundClosureToSelection is set, Run must construct
// a SelectionScopePolicy from the resource set entering dep-chase and pass it as
// depchase.Options.AdoptionPolicy — and when the flag is unset, the policy must
// be nil (historical adopt-all). The scope must cover the selected resource's
// type.
func TestRun_BoundClosureToSelection_PassesScopePolicy(t *testing.T) {
	req := job.Request{
		Version: job.Version,
		Resources: []job.ResourceSpec{{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.orders",
				ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders",
				Region:   "us-east-1",
			},
			Tier:   imported.TierImportedFlat,
			Source: imported.SourceImporter,
		}},
	}

	newRunOpts := func(dir string, bound bool, cap *capturingDepChase) Options {
		return Options{
			OutputDir:               dir,
			Discoverer:              stubByIDDiscoverer{},
			BoundClosureToSelection: bound,
			deps: deps{
				runGenconfig: fakeGenconfig,
				runDriftfix:  fakeDriftfix,
				runDepChase:  cap.run,
				tf:           fakeTerraformRunner{},
			},
		}
	}

	// Flag set → non-nil SelectionScopePolicy covering the selected type.
	boundCap := &capturingDepChase{}
	if _, err := Run(context.Background(), req, newRunOpts(t.TempDir(), true, boundCap)); err != nil {
		t.Fatalf("Run (bound): %v", err)
	}
	pol, ok := boundCap.opts.AdoptionPolicy.(depchase.SelectionScopePolicy)
	if !ok {
		t.Fatalf("AdoptionPolicy=%T, want depchase.SelectionScopePolicy", boundCap.opts.AdoptionPolicy)
	}
	if _, inScope := pol.InScopeTypes["aws_sqs_queue"]; !inScope {
		t.Errorf("scope policy InScopeTypes=%v, want it to cover the selected aws_sqs_queue", pol.InScopeTypes)
	}

	// Flag unset → nil policy (historical adopt-all).
	unboundCap := &capturingDepChase{}
	if _, err := Run(context.Background(), req, newRunOpts(t.TempDir(), false, unboundCap)); err != nil {
		t.Fatalf("Run (unbound): %v", err)
	}
	if unboundCap.opts.AdoptionPolicy != nil {
		t.Errorf("AdoptionPolicy=%#v, want nil when BoundClosureToSelection is unset", unboundCap.opts.AdoptionPolicy)
	}
}
