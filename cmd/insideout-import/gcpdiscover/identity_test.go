package gcpdiscover

import (
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func TestMakeImportedResource_PopulatesRequiredFields(t *testing.T) {
	t.Parallel()
	book := addressBook{}
	wantTags := map[string]string{"project": "io-foo", "env": "prod"}
	got := makeImportedResource(book, "google_pubsub_topic", "io-foo-events",
		"projects/real-proj/topics/io-foo-events", "real-proj", "",
		map[string]string{"asset_name": "//pubsub.googleapis.com/projects/real-proj/topics/io-foo-events"},
		wantTags)

	// Tags carrier (#291) — pin both presence and shape. Mirrors the
	// AWS-side pin so a regression on either side is caught here.
	if got.Identity.Tags == nil {
		t.Error("Tags must be populated when discoverer fetched a label map")
	}
	if got.Identity.Tags["project"] != "io-foo" || got.Identity.Tags["env"] != "prod" {
		t.Errorf("Tags=%v, want %v", got.Identity.Tags, wantTags)
	}

	if got.Identity.Cloud != "gcp" {
		t.Errorf("Cloud=%q, want gcp", got.Identity.Cloud)
	}
	if got.Identity.Type != "google_pubsub_topic" {
		t.Errorf("Type=%q, want google_pubsub_topic", got.Identity.Type)
	}
	if got.Identity.Address == "" {
		t.Error("Address must be populated by GenerateAddress")
	}
	if got.Identity.ImportID != "projects/real-proj/topics/io-foo-events" {
		t.Errorf("ImportID=%q, unexpected shape", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-events" {
		t.Errorf("NameHint=%q, want io-foo-events", got.Identity.NameHint)
	}
	if got.Identity.ProjectID != "real-proj" {
		t.Errorf("ProjectID=%q, want real-proj", got.Identity.ProjectID)
	}
	if got.Identity.ProviderSource != gcpProviderSource {
		t.Errorf("ProviderSource=%q, want %q", got.Identity.ProviderSource, gcpProviderSource)
	}
	// The composer's emitted HCL references `provider = google.imported`
	// for every imported google_* resource — same shape as the AWS path's
	// `aws.imported`. A mutation that drops or renames this constant
	// breaks every downstream stack.
	if got.Identity.ProviderConfig != "google.imported" {
		t.Errorf("ProviderConfig=%q, want google.imported", got.Identity.ProviderConfig)
	}
	if got.Identity.NativeIDs["name"] != "io-foo-events" {
		t.Errorf("NativeIDs[name]=%q, want io-foo-events", got.Identity.NativeIDs["name"])
	}
	if got.Identity.NativeIDs["asset_name"] == "" {
		t.Error("NativeIDs[asset_name] should be populated by extra map")
	}
	if got.Tier != imported.TierImportedFlat {
		t.Errorf("Tier=%q, want TierImportedFlat", got.Tier)
	}
	if got.Source != imported.SourceImporter {
		t.Errorf("Source=%q, want SourceImporter", got.Source)
	}
}

func TestMakeImportedResource_ResolvesAddressCollisionsWithinBatch(t *testing.T) {
	t.Parallel()
	book := addressBook{}
	a := makeImportedResource(book, "google_pubsub_topic", "events",
		"projects/p1/topics/events", "p1", "", nil, nil)
	b := makeImportedResource(book, "google_pubsub_topic", "events",
		"projects/p1/topics/events-also", "p1", "", nil, nil)

	if a.Identity.Address == b.Identity.Address {
		t.Errorf("expected distinct addresses for distinct identities; got %q twice", a.Identity.Address)
	}
	if !strings.HasPrefix(b.Identity.Address, a.Identity.Address+"_") {
		t.Errorf("collision-resolved address %q must start with the original %q + '_'", b.Identity.Address, a.Identity.Address)
	}
	suffix := b.Identity.Address[len(a.Identity.Address)+1:]
	if len(suffix) != 8 {
		t.Errorf("collision suffix = %q (len=%d), want 8 hex chars", suffix, len(suffix))
	}
}

func TestMakeImportedResource_EmptyLocationLeftEmpty(t *testing.T) {
	t.Parallel()
	got := makeImportedResource(addressBook{}, "google_pubsub_topic", "events",
		"projects/p/topics/events", "p", "", nil, nil)
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty for project-global resource", got.Identity.Location)
	}
}

// TestMakeImportedResource_RoutesGoogleBetaTypesThroughBetaAlias pins
// that API Gateway resources — whose schema lives in google-beta — get
// stamped with the google-beta provider source AND the
// google-beta.imported alias. Without this, Stage 2c emits
// `provider = google.imported` and Stage 2b's plan-generate-config-out
// either fails on a missing resource type or rebinds the resource
// through the wrong provider, breaking the import.
func TestMakeImportedResource_RoutesGoogleBetaTypesThroughBetaAlias(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tfType        string
		wantSource    string
		wantAlias     string
		wantImportID  string
		wantNameHint  string
		wantProjectID string
	}{
		{
			tfType:        "google_api_gateway_api",
			wantSource:    gcpBetaProviderSource,
			wantAlias:     gcpBetaProviderConfigAlias,
			wantImportID:  "projects/p/locations/global/apis/demo",
			wantNameHint:  "demo",
			wantProjectID: "p",
		},
		{
			tfType:        "google_api_gateway_api_config",
			wantSource:    gcpBetaProviderSource,
			wantAlias:     gcpBetaProviderConfigAlias,
			wantImportID:  "projects/p/locations/global/apis/demo/configs/v1",
			wantNameHint:  "v1",
			wantProjectID: "p",
		},
		{
			tfType:        "google_api_gateway_gateway",
			wantSource:    gcpBetaProviderSource,
			wantAlias:     gcpBetaProviderConfigAlias,
			wantImportID:  "projects/p/locations/us-central1/gateways/demo-gw",
			wantNameHint:  "demo-gw",
			wantProjectID: "p",
		},
		{
			// Sanity: a GA google_* type must still route through the
			// google provider source + alias. This protects against a
			// regression that flips every type into the beta path.
			tfType:        "google_pubsub_topic",
			wantSource:    gcpProviderSource,
			wantAlias:     gcpProviderConfigAlias,
			wantImportID:  "projects/p/topics/events",
			wantNameHint:  "events",
			wantProjectID: "p",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tfType, func(t *testing.T) {
			t.Parallel()
			got := makeImportedResource(addressBook{}, tc.tfType,
				tc.wantNameHint, tc.wantImportID, tc.wantProjectID, "",
				nil, nil)
			if got.Identity.ProviderSource != tc.wantSource {
				t.Errorf("ProviderSource=%q, want %q", got.Identity.ProviderSource, tc.wantSource)
			}
			if got.Identity.ProviderConfig != tc.wantAlias {
				t.Errorf("ProviderConfig=%q, want %q", got.Identity.ProviderConfig, tc.wantAlias)
			}
			// Pin the non-provider Identity fields too — a refactor
			// that drops the input plumbing for ImportID/NameHint/
			// ProjectID would otherwise pass this test silently.
			if got.Identity.ImportID != tc.wantImportID {
				t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, tc.wantImportID)
			}
			if got.Identity.NameHint != tc.wantNameHint {
				t.Errorf("NameHint=%q, want %q", got.Identity.NameHint, tc.wantNameHint)
			}
			if got.Identity.ProjectID != tc.wantProjectID {
				t.Errorf("ProjectID=%q, want %q", got.Identity.ProjectID, tc.wantProjectID)
			}
		})
	}
}

func TestMakeImportedResource_PropagatesLocationForRegionalResource(t *testing.T) {
	t.Parallel()
	got := makeImportedResource(addressBook{}, "google_storage_bucket", "io-bucket",
		"io-bucket", "real-proj", "us-central1", nil, nil)
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
}

func TestMergeNativeIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		hint string
		in   map[string]string
		want map[string]string
	}{
		{name: "name only", hint: "topic", in: nil, want: map[string]string{"name": "topic"}},
		{name: "name plus asset_name", hint: "topic", in: map[string]string{"asset_name": "//x/y"}, want: map[string]string{"name": "topic", "asset_name": "//x/y"}},
		{name: "drops empty values", hint: "topic", in: map[string]string{"self_link": "", "asset_name": "//x"}, want: map[string]string{"name": "topic", "asset_name": "//x"}},
		{name: "empty everything returns nil", hint: "", in: nil, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mergeNativeIDs(tc.hint, tc.in)
			if len(got) != len(tc.want) {
				t.Errorf("len=%d, want %d (%v)", len(got), len(tc.want), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("got[%q]=%q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestShortName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"//pubsub.googleapis.com/projects/p/topics/alpha", "alpha"},
		{"//storage.googleapis.com/io-bucket", "io-bucket"},
		{"projects/p/global/networks/vpc-main", "vpc-main"},
		{"bare-name", "bare-name"},
		{"trailing-slash/", "trailing-slash/"}, // pathological — no trailing-segment stripping
	}
	for _, tc := range cases {
		if got := shortName(tc.in); got != tc.want {
			t.Errorf("shortName(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}
