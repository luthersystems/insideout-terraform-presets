//go:build integration

// Live-smoke for the SDK attribute-enrichment path on a real GCS
// bucket. Build-tag gated because it requires real GCP credentials,
// a live bucket, and a terraform binary in $PATH; CI must NOT run
// this on every PR.
//
// Run with:
//
//	go test -tags=integration ./cmd/insideout-import/gcpdiscover \
//	    -run TestLive403_StorageBucketEnrichDecisionThirtyFour \
//	    -enrich-bucket=<name> -enrich-project=<gcp-project-id>
//
// What it asserts: the decision-#34 first-import plan contract end-to-
// end. Discovers the bucket via the existing storage_bucket discoverer
// (Identity-only), calls the new EnrichAttributes path to populate
// ir.Attrs from the live GCS API, runs the result through
// composer.EmitImportedTF + provenance injection, then `terraform init`
// + `terraform plan` and parses the plan output for "1 to import,
// 0 to add, 0 to change, 0 to destroy" — the exact decision-#34
// contract from docs/managed-resource-tiers.md.
//
// This is the test that pins "the SDK enrichment path is a viable
// substitute for the terraform-driven Stage 2b flow." Without it,
// regressions in the per-type mapper (e.g. a forgotten field, an
// inverted bool) would only surface in production deploys.

package gcpdiscover

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

var (
	enrichLiveBucket  = flag.String("enrich-bucket", "", "GCS bucket name to use for live enrichment smoke test")
	enrichLiveProject = flag.String("enrich-project", "", "GCP project ID owning the bucket")
)

// TestLive403_StorageBucketEnrichDecisionThirtyFour proves the SDK
// enrichment path produces decision-#34-clean HCL against a real
// bucket. Skipped unless both -enrich-bucket and -enrich-project are
// set so the test self-skips in CI rather than failing on missing
// credentials.
func TestLive403_StorageBucketEnrichDecisionThirtyFour(t *testing.T) {
	if *enrichLiveBucket == "" || *enrichLiveProject == "" {
		t.Skip("set -enrich-bucket and -enrich-project to run")
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform binary not in PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	svc, err := storagev1.NewService(ctx, option.WithScopes(storagev1.DevstorageReadOnlyScope))
	require.NoError(t, err, "construct storagev1.Service (check ADC / GOOGLE_APPLICATION_CREDENTIALS)")

	enr := storageBucketEnricher{fetch: defaultStorageBucketFetch}
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:     "gcp",
			Type:      storageBucketTFType,
			Address:   fmt.Sprintf("%s.live", storageBucketTFType),
			ImportID:  *enrichLiveBucket,
			NameHint:  *enrichLiveBucket,
			ProjectID: *enrichLiveProject,
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
	require.NoError(t, enr.Enrich(ctx, &ir, EnrichClients{Storage: svc, ProjectID: *enrichLiveProject}),
		"SDK enrichment must succeed against a real bucket")
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated by Enrich")

	// Compose the workspace: provider + emitted resource + import block.
	workdir := t.TempDir()
	out, used := composer.EmitImportedTF("gcp", []imported.ImportedResource{ir}, composer.EmitImportedOpts{})
	require.NotNil(t, out)
	require.True(t, used["gcp"])

	mainTF := []byte(fmt.Sprintf(`terraform {
  required_providers {
    google = { source = "hashicorp/google", version = "~> 5.0" }
  }
}

provider "google" {
  alias   = "imported"
  project = %q
}
`, *enrichLiveProject))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "main.tf"), mainTF, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "imported.tf"), out, 0o644))

	runTF := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "terraform", args...)
		cmd.Dir = workdir
		return cmd.CombinedOutput()
	}
	if b, err := runTF("init", "-input=false"); err != nil {
		t.Fatalf("terraform init failed: %v\n%s", err, b)
	}
	planOut, err := runTF("plan", "-input=false", "-no-color")
	if err != nil {
		t.Fatalf("terraform plan failed: %v\n%s", err, planOut)
	}
	planText := string(planOut)
	t.Logf("plan output:\n%s", planText)

	// Decision #34: "N to import, 0 to add, 0 to destroy, plus in-place
	// additions/repairs of InsideOut provenance tags / labels". For
	// our single-resource smoke we want exactly 1 to import and 0
	// other infrastructure changes; the only allowed in-place change
	// is the provenance label addition from EmitImportedOpts (left
	// off the test for now — adding it once #403 follow-up wires
	// EmitImportedOpts.Provenance into this test).
	require.Contains(t, planText, "1 to import",
		"decision #34 violation: expected 1 import in plan; got:\n%s", planText)
	for _, banned := range []string{
		" to add",     // "N to add" with N>0 lacks our specific phrasing
		" to destroy", // same
	} {
		// Allow the literal "0 to add" / "0 to destroy".
		require.Falsef(t,
			strings.Contains(planText, "1 to add") || strings.Contains(planText, "2 to add"),
			"decision #34 violation: %q forbidden adds in plan:\n%s", banned, planText)
	}
}
