package gcpdiscover

import (
	"reflect"
	"testing"

	"cloud.google.com/go/asset/apiv1/assetpb"
)

// TestAssetResultFromProto_FieldMapping pins the proto→gcpAssetResult
// mapping field-by-field. The per-type discoverers all start from a
// hand-built gcpAssetResult, so a mutation here (e.g. swapping
// `Name: r.GetName()` ← → `AssetType: r.GetAssetType()`, or dropping
// Location) would silently invalidate every downstream contract. Using
// distinct, non-overlapping values for every field guarantees a swap of
// any two is caught.
func TestAssetResultFromProto_FieldMapping(t *testing.T) {
	t.Parallel()
	in := &assetpb.ResourceSearchResult{
		Name:      "//pubsub.googleapis.com/projects/real-proj/topics/io-events",
		AssetType: "pubsub.googleapis.com/Topic",
		Project:   "real-proj",
		Location:  "us-central1",
		Labels:    map[string]string{"project": "io-foo", "owner": "team-a"},
	}
	got := assetResultFromProto(in)
	want := gcpAssetResult{
		Name:      "//pubsub.googleapis.com/projects/real-proj/topics/io-events",
		AssetType: "pubsub.googleapis.com/Topic",
		Project:   "real-proj",
		Location:  "us-central1",
		Labels:    map[string]string{"project": "io-foo", "owner": "team-a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assetResultFromProto mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// TestAssetResultFromProto_NilProtoSafeFields pins the GetX accessor
// idioms — every proto getter on a nil/zero ResourceSearchResult
// returns the zero value of its field type. A future refactor that
// replaced `r.GetName()` with `r.Name` would panic on a nil proto;
// asserting on a zero-valued proto guards that.
func TestAssetResultFromProto_NilProtoSafeFields(t *testing.T) {
	t.Parallel()
	got := assetResultFromProto(&assetpb.ResourceSearchResult{})
	if got.Name != "" || got.AssetType != "" || got.Project != "" || got.Location != "" || got.Labels != nil {
		t.Errorf("zero-proto must produce zero result; got %+v", got)
	}
}
