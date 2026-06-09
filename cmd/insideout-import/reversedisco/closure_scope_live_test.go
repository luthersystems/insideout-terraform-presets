//go:build integration

// closure_scope_live_test.go is the live-AWS pre-merge repro check for the
// #739 selection-closure defects. The unit suite proves each fix against
// fakes; this harness proves the deployed behavior against a real account —
// the gap that let the original #738 wiring ship green and then break on
// staging (account-wide sweep + AccessDenied hard-fail).
//
// What it locks: selecting exactly ONE S3 bucket parent and running the same
// DiscoverClosure call the engine makes must enumerate ONLY that bucket and
// its child sub-resources. Pre-#739 this returned every bucket in the
// account (observed live: 1 selected -> all 44 buckets / 216 resources), so
// this test fails loudly on the old adapter.
//
// Like roundtrip_live_test.go it is gated three ways so it never runs in
// normal `go test ./...` or CI: the `integration` build tag, the
// RUN_LIVE_CLOSURE=1 env guard, and a credential probe that skips cleanly
// when no AWS creds are loaded. Run it with read-only creds for a test
// account (e.g. after `aws_jump <acct> <role>`):
//
//	RUN_LIVE_CLOSURE=1 go test -tags integration \
//	  ./cmd/insideout-import/reversedisco -run TestLiveClosureScope -v
package reversedisco_test

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/reversedisco"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/labels"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport"
)

func TestLiveClosureScope(t *testing.T) {
	if os.Getenv("RUN_LIVE_CLOSURE") != "1" {
		t.Skip("live closure-scope harness disabled; set RUN_LIVE_CLOSURE=1 with AWS creds for a test account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Credential probe: list buckets directly. Skip (not fail) when creds are
	// absent so the tag alone never breaks a keyless environment.
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		t.Skipf("no AWS config: %v", err)
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
	defer probeCancel()
	listOut, err := s3.NewFromConfig(cfg).ListBuckets(probeCtx, &s3.ListBucketsInput{})
	if err != nil {
		t.Skipf("no usable AWS credentials (s3:ListBuckets probe failed): %v", err)
	}
	if len(listOut.Buckets) < 2 {
		t.Skipf("need >=2 buckets to prove scoping; account has %d", len(listOut.Buckets))
	}
	selected := *listOut.Buckets[0].Name
	t.Logf("account has %d buckets; selecting only %q", len(listOut.Buckets), selected)

	// Child types from the same registry the engine's closure uses.
	var childTypes []string
	for _, ct := range labels.ChildTfTypes() {
		if pt, ok := labels.ParentTfType(ct); ok && pt == "aws_s3_bucket" {
			childTypes = append(childTypes, ct)
		}
	}
	sort.Strings(childTypes)
	if len(childTypes) == 0 {
		t.Fatal("labels registry has no aws_s3_bucket child types; closure would be a no-op and this harness is vacuous")
	}

	// Ambient creds (empty AWSAssumeRole) — this harness runs in CLI context
	// where the test-account creds are already loaded. The assume-role path is
	// locked by TestNewAWSAssumesRoleWhenAuthPresent.
	disco, cleanup, err := reversedisco.New(ctx, "aws", "us-east-1", "", "", reversedisco.AWSAssumeRole{})
	if err != nil {
		t.Fatalf("reversedisco.New: %v", err)
	}
	defer cleanup()
	closure, ok := disco.(reverseimport.ClosureDiscoverer)
	if !ok {
		t.Fatal("discoverer is not closure-capable")
	}

	start := time.Now()
	found, err := closure.DiscoverClosure(ctx, reverseimport.ClosureRequest{
		Cloud:   "aws",
		Regions: []string{"us-east-1"},
		ParentResources: []imported.ImportedResource{{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_s3_bucket",
				Address:  "aws_s3_bucket.live_scope",
				NameHint: selected,
				ImportID: selected,
			},
		}},
		ParentTypes: []string{"aws_s3_bucket"},
		ChildTypes:  childTypes,
	})
	if err != nil {
		t.Fatalf("DiscoverClosure: %v", err)
	}
	t.Logf("DiscoverClosure returned %d resources in %s", len(found), time.Since(start))

	// The #739 regression assertion: only the selected parent may appear, and
	// no resource may belong to any other bucket in the account.
	otherBuckets := map[string]struct{}{}
	for _, b := range listOut.Buckets[1:] {
		otherBuckets[*b.Name] = struct{}{}
	}
	distinctBuckets := map[string]struct{}{}
	for _, r := range found {
		if r.Identity.Type == "aws_s3_bucket" {
			distinctBuckets[r.Identity.NameHint] = struct{}{}
		}
		if _, leaked := otherBuckets[r.Identity.NameHint]; leaked {
			t.Errorf("closure leaked unselected bucket's resource: %s %s (#739 account-wide sweep)", r.Identity.Type, r.Identity.NameHint)
		}
	}
	if len(distinctBuckets) != 1 {
		t.Errorf("closure returned %d distinct buckets, want exactly the 1 selected (#739 account-wide sweep)", len(distinctBuckets))
	}
	if _, ok := distinctBuckets[selected]; len(distinctBuckets) > 0 && !ok {
		t.Errorf("closure result is missing the selected bucket %q", selected)
	}
}
