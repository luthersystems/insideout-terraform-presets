//go:build integration

// closure_scope_gcp_live_test.go is the live-GCP pre-merge repro check for
// the #777 selection-closure scoping parity (the GCP twin of
// closure_scope_live_test.go for AWS #739). The unit suite proves the
// scoping against fakes; this harness proves the deployed behavior against a
// real project — closing the same green-then-broken-on-staging gap the AWS
// live harness closes.
//
// What it locks: selecting exactly ONE google_storage_bucket parent and
// running the same DiscoverClosure call the engine makes must scope CAI
// enumeration to that bucket — it must NOT project-wide-sweep every bucket
// in the project. Pre-#777 gcpAggAdapter.DiscoverClosure ignored
// req.ParentResources and ran a project-wide DiscoverTypes sweep, so this
// test fails loudly on the old adapter (it returns sibling buckets).
//
// Like the AWS harness it is gated three ways so it never runs in normal
// `go test ./...` or CI: the `integration` build tag, the
// RUN_LIVE_CLOSURE_GCP=1 env guard, and a credential probe that skips
// cleanly when no GCP ADC / project is available. Run it with read-only ADC
// for a test project:
//
//	RUN_LIVE_CLOSURE_GCP=1 GCP_PROJECT_ID=<proj> \
//	  go test -tags integration \
//	  ./cmd/insideout-import/reversedisco -run TestLiveClosureScopeGCP -v
package reversedisco_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/reversedisco"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport"
)

func TestLiveClosureScopeGCP(t *testing.T) {
	if os.Getenv("RUN_LIVE_CLOSURE_GCP") != "1" {
		t.Skip("live GCP closure-scope harness disabled; set RUN_LIVE_CLOSURE_GCP=1 with GCP ADC for a test project")
	}
	projectID := strings.TrimSpace(os.Getenv("GCP_PROJECT_ID"))
	if projectID == "" {
		t.Skip("GCP_PROJECT_ID unset; set it to the real GCP project ID scoping Cloud Asset Inventory")
	}
	// The selected bucket name to scope to. The operator supplies it (the
	// harness does not enumerate buckets first — that is exactly the
	// project-wide read this fix removes). Skip cleanly when absent so the
	// tag alone never breaks a project-less environment.
	selected := strings.TrimSpace(os.Getenv("GCP_CLOSURE_BUCKET"))
	if selected == "" {
		t.Skip("GCP_CLOSURE_BUCKET unset; set it to a google_storage_bucket name in the project to scope to")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// New constructs the GCP discoverer with customer-scoped ADC via
	// gcpdiscover.NewRealAssetSearcher (the #777 credential-context path —
	// mirrors the AWS mars#198 fix). Construction can fail if ADC is
	// unconfigured or the Cloud Asset API isn't enabled; skip (not fail) so
	// a keyless environment never breaks the tag.
	disco, cleanup, err := reversedisco.New(ctx, "gcp", "", projectID, "", reversedisco.AWSAssumeRole{})
	if err != nil {
		t.Skipf("reversedisco.New(gcp) (no usable GCP credentials / Cloud Asset API?): %v", err)
	}
	defer cleanup()
	closure, ok := disco.(reverseimport.ClosureDiscoverer)
	if !ok {
		t.Fatal("discoverer is not closure-capable")
	}

	// Discriminating-power probe: count every bucket in the project via an
	// UNSCOPED closure request (empty ParentResources ⇒ nil ParentScope ⇒
	// the legacy project-wide sweep). If the project has only the one
	// selected bucket, the scoped and swept results are indistinguishable
	// and a green run proves nothing — so require >=2 buckets before the
	// scoping assertion below can mean anything. This closes the
	// inherited-from-AWS false-confidence gap where "it passed live" did
	// not imply the scoping actually narrowed the read.
	probe, err := closure.DiscoverClosure(ctx, reverseimport.ClosureRequest{
		Cloud:        "gcp",
		GCPProjectID: projectID,
		ParentTypes:  []string{"google_storage_bucket"},
	})
	if err != nil {
		t.Fatalf("unscoped probe DiscoverClosure: %v", err)
	}
	probeBuckets := map[string]struct{}{}
	for _, r := range probe {
		if r.Identity.Type == "google_storage_bucket" {
			probeBuckets[r.Identity.NameHint] = struct{}{}
		}
	}
	t.Logf("project %q has %d google_storage_bucket(s) project-wide", projectID, len(probeBuckets))
	if _, has := probeBuckets[selected]; !has {
		t.Fatalf("selected bucket %q not found in the project-wide sweep — set GCP_CLOSURE_BUCKET to a real bucket name", selected)
	}
	if len(probeBuckets) < 2 {
		t.Skipf("project has %d bucket(s); need >=2 to prove scoping discriminates (the scoped and swept results would be identical)", len(probeBuckets))
	}

	start := time.Now()
	found, err := closure.DiscoverClosure(ctx, reverseimport.ClosureRequest{
		Cloud:        "gcp",
		GCPProjectID: projectID,
		ParentResources: []imported.ImportedResource{{
			Identity: imported.ResourceIdentity{
				Cloud:     "gcp",
				Type:      "google_storage_bucket",
				Address:   "google_storage_bucket.live_scope",
				NameHint:  selected,
				ImportID:  selected,
				ProjectID: projectID,
			},
		}},
		ParentTypes: []string{"google_storage_bucket"},
		// Whatever child types the registry maps to google_storage_bucket;
		// the parent sweep scoping is the #777 win regardless.
		ChildTypes: nil,
	})
	if err != nil {
		t.Fatalf("DiscoverClosure: %v", err)
	}
	t.Logf("DiscoverClosure returned %d resources in %s", len(found), time.Since(start))

	// The #777 regression assertion: only the selected bucket may appear —
	// the project-wide sweep would return every sibling bucket in the
	// project.
	distinctBuckets := map[string]struct{}{}
	for _, r := range found {
		if r.Identity.Type == "google_storage_bucket" {
			distinctBuckets[r.Identity.NameHint] = struct{}{}
			if r.Identity.NameHint != selected {
				t.Errorf("closure leaked unselected bucket: %s (#777 project-wide sweep)", r.Identity.NameHint)
			}
		}
	}
	// A vacuous empty result would pass the leak/count checks while proving
	// nothing — the scoped closure must surface the one selected bucket.
	if _, ok := distinctBuckets[selected]; !ok {
		t.Errorf("scoped closure did not return the selected bucket %q (found %d resources) — a vacuous empty result is not a pass", selected, len(found))
	}
	if len(distinctBuckets) > 1 {
		t.Errorf("closure returned %d distinct buckets, want exactly the 1 selected (#777 project-wide sweep)", len(distinctBuckets))
	}
}
