package snapshot_test

import (
	"encoding/json"
	"errors"
	"testing"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/snapshot"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture returns a small, deterministic slice of ImportedResource
// ordered NOT by Address — the round-trip / byte-stable tests rely on
// MarshalSnapshot doing the sort itself.
//
// Every non-trivial field on Identity is populated on at least one
// row so the round-trip test exercises the full struct shape, not
// just Address. A regression that drops Attrs / NativeIDs /
// ProviderConfig / Region from the envelope is caught loud.
func fixture() []composerimported.ImportedResource {
	return []composerimported.ImportedResource{
		{
			Identity: composerimported.ResourceIdentity{
				Cloud:           "aws",
				Type:            "aws_sqs_queue",
				Address:         "aws_sqs_queue.zeta",
				NameHint:        "zeta-queue",
				ProviderConfig:  "aws",
				ProviderSource:  "hashicorp/aws",
				ProviderVersion: "6.0.0",
				SchemaVersion:   "v1",
				AccountID:       "123456789012",
				Region:          "us-east-1",
				ImportID:        "https://sqs.us-east-1.amazonaws.com/123456789012/zeta-queue",
				NativeIDs: map[string]string{
					"arn":  "arn:aws:sqs:us-east-1:123456789012:zeta-queue",
					"name": "zeta-queue",
					"url":  "https://sqs.us-east-1.amazonaws.com/123456789012/zeta-queue",
				},
				Tags: map[string]string{"Project": "demo", "Env": "dev"},
			},
			Tier:       composerimported.TierImportedFlat,
			Source:     composerimported.SourceImporter,
			Attrs:      []byte(`{"name":"zeta-queue","fifo_queue":true,"delay_seconds":0}`),
			Attributes: map[string]any{"name": "zeta-queue", "fifo_queue": true},
			WeakLocked: true,
		},
		{
			Identity: composerimported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_s3_bucket",
				Address:  "aws_s3_bucket.alpha",
				NameHint: "alpha-bucket",
				ImportID: "alpha-bucket",
				NativeIDs: map[string]string{
					"arn":  "arn:aws:s3:::alpha-bucket",
					"name": "alpha-bucket",
				},
			},
			Tier:   composerimported.TierImportedConformant,
			Source: composerimported.SourceImporter,
			Attrs:  []byte(`{"bucket":"alpha-bucket"}`),
		},
		{
			Identity: composerimported.ResourceIdentity{
				Cloud:     "gcp",
				Type:      "google_storage_bucket",
				Address:   "google_storage_bucket.middle",
				ProjectID: "demo-project",
				Location:  "US",
			},
			// Default zero Tier on this row — round-trip should preserve it.
		},
	}
}

func sortedAddresses(rs []composerimported.ImportedResource) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Identity.Address
	}
	return out
}

// TestMarshalSnapshot_RoundTrip verifies that MarshalSnapshot followed
// by UnmarshalSnapshot returns the original resources sorted by
// Address.
func TestMarshalSnapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	irs := fixture()
	raw, version, err := snapshot.MarshalSnapshot(irs)
	require.NoError(t, err)
	require.Equal(t, snapshot.CurrentVersion, version)

	got, gotVersion, err := snapshot.UnmarshalSnapshot(raw)
	require.NoError(t, err)
	require.Equal(t, snapshot.CurrentVersion, gotVersion)
	require.Len(t, got, len(irs))

	assert.Equal(t, []string{
		"aws_s3_bucket.alpha",
		"aws_sqs_queue.zeta",
		"google_storage_bucket.middle",
	}, sortedAddresses(got))

	// Field-level round-trip pin: every populated Identity field and
	// the per-resource Attrs / Tier / Source / WeakLocked must survive
	// the round-trip. A regression that drops any field from the
	// envelope fails here loud (instead of silently producing a
	// snapshot that's missing data downstream).
	expectedSorted := make([]composerimported.ImportedResource, len(irs))
	copy(expectedSorted, irs)
	sortByAddress(expectedSorted)
	for i := range got {
		assertImportedResourceEqual(t, expectedSorted[i], got[i])
	}
}

// sortByAddress is the sort that MarshalSnapshot performs internally;
// the test mirrors it so per-field comparisons line up.
func sortByAddress(rs []composerimported.ImportedResource) {
	sortStable(rs, func(a, b composerimported.ImportedResource) bool {
		return a.Identity.Address < b.Identity.Address
	})
}

// sortStable is a tiny generic stable sort so the test doesn't take a
// dep on `sort` directly here.
func sortStable[T any](s []T, less func(a, b T) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// assertImportedResourceEqual pins every Identity field plus
// Attrs / Tier / Source / WeakLocked across the round-trip. Uses
// field-by-field assertions so a failure points at the specific
// dropped field, not a wall of struct diff output.
func assertImportedResourceEqual(t *testing.T, want, got composerimported.ImportedResource) {
	t.Helper()
	assert.Equal(t, want.Identity.Cloud, got.Identity.Cloud, "Cloud")
	assert.Equal(t, want.Identity.Type, got.Identity.Type, "Type")
	assert.Equal(t, want.Identity.Address, got.Identity.Address, "Address")
	assert.Equal(t, want.Identity.NameHint, got.Identity.NameHint, "NameHint")
	assert.Equal(t, want.Identity.ProviderConfig, got.Identity.ProviderConfig, "ProviderConfig")
	assert.Equal(t, want.Identity.ProviderSource, got.Identity.ProviderSource, "ProviderSource")
	assert.Equal(t, want.Identity.ProviderVersion, got.Identity.ProviderVersion, "ProviderVersion")
	assert.Equal(t, want.Identity.SchemaVersion, got.Identity.SchemaVersion, "SchemaVersion")
	assert.Equal(t, want.Identity.AccountID, got.Identity.AccountID, "AccountID")
	assert.Equal(t, want.Identity.ProjectID, got.Identity.ProjectID, "ProjectID")
	assert.Equal(t, want.Identity.Region, got.Identity.Region, "Region")
	assert.Equal(t, want.Identity.Location, got.Identity.Location, "Location")
	assert.Equal(t, want.Identity.ImportID, got.Identity.ImportID, "ImportID")
	assert.Equal(t, want.Identity.NativeIDs, got.Identity.NativeIDs, "NativeIDs")
	assert.Equal(t, want.Identity.Tags, got.Identity.Tags, "Tags")
	assert.Equal(t, want.Tier, got.Tier, "Tier")
	assert.Equal(t, want.Source, got.Source, "Source")
	assert.Equal(t, want.WeakLocked, got.WeakLocked, "WeakLocked")
	// Attrs is json.RawMessage — assert byte-equality on the JSON
	// payload to catch silent semantic changes (whitespace re-emits
	// would also fail; that's intentional — the envelope is supposed
	// to round-trip Attrs verbatim).
	assert.Equal(t, string(want.Attrs), string(got.Attrs), "Attrs")
}

// TestMarshalSnapshot_ByteStable verifies that marshaling the same
// slice twice produces byte-identical output. The downstream stack
// versions table compares envelopes byte-wise to detect drift; any
// non-determinism here would manufacture spurious "imported changed"
// rows.
func TestMarshalSnapshot_ByteStable(t *testing.T) {
	t.Parallel()

	irs := fixture()
	first, _, err := snapshot.MarshalSnapshot(irs)
	require.NoError(t, err)

	second, _, err := snapshot.MarshalSnapshot(irs)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second),
		"snapshot output must be byte-identical across calls")

	// Also verify that an unrelated permutation of the input yields
	// the same output — MarshalSnapshot sorts internally.
	permuted := []composerimported.ImportedResource{
		irs[1], irs[2], irs[0],
	}
	third, _, err := snapshot.MarshalSnapshot(permuted)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(third),
		"snapshot output must be invariant under input permutation")
}

// TestMarshalSnapshot_NilInput verifies that nil and empty-slice
// inputs both round-trip to a valid empty envelope. The downstream
// reader treats an empty resources list as "no imported resources" —
// a hard error here would block initial bootstrapping of a stack with
// no imports yet.
func TestMarshalSnapshot_NilInput(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"nil", "empty"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var in []composerimported.ImportedResource
			if name == "empty" {
				in = []composerimported.ImportedResource{}
			}
			raw, version, err := snapshot.MarshalSnapshot(in)
			require.NoError(t, err)
			require.Equal(t, snapshot.CurrentVersion, version)

			got, gotVersion, err := snapshot.UnmarshalSnapshot(raw)
			require.NoError(t, err)
			require.Equal(t, snapshot.CurrentVersion, gotVersion)
			assert.Empty(t, got)
		})
	}
}

// TestUnmarshalSnapshot_V0Legacy verifies that a hand-crafted JSON
// array (the v0 storage shape from #144 cut 1) decodes as a v0
// envelope. The returned version is 0; the resources are the raw
// array contents in original order (no implicit sort).
func TestUnmarshalSnapshot_V0Legacy(t *testing.T) {
	t.Parallel()

	legacy := []composerimported.ImportedResource{
		{Identity: composerimported.ResourceIdentity{Address: "aws_sqs_queue.zeta"}},
		{Identity: composerimported.ResourceIdentity{Address: "aws_s3_bucket.alpha"}},
	}
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)

	got, version, err := snapshot.UnmarshalSnapshot(raw)
	require.NoError(t, err)
	assert.Equal(t, 0, version)
	require.Len(t, got, 2)
	// Order is preserved — v0 decode is a passthrough.
	assert.Equal(t, "aws_sqs_queue.zeta", got[0].Identity.Address)
	assert.Equal(t, "aws_s3_bucket.alpha", got[1].Identity.Address)
}

// TestUnmarshalSnapshot_UnsupportedVersion verifies that an envelope
// claiming a version higher than CurrentVersion returns the sentinel
// error. The version field is also returned so the caller can log it
// without re-decoding the envelope.
func TestUnmarshalSnapshot_UnsupportedVersion(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"version":99,"resources":[]}`)
	got, version, err := snapshot.UnmarshalSnapshot(raw)
	require.Error(t, err)
	assert.True(t, errors.Is(err, snapshot.ErrUnsupportedVersion),
		"expected ErrUnsupportedVersion, got %v", err)
	assert.Equal(t, 99, version)
	assert.Nil(t, got)
}

// TestUnmarshalSnapshot_LeadingWhitespace verifies that the
// array-vs-object sniff tolerates leading whitespace — encoding/json
// accepts it, so UnmarshalSnapshot must too.
func TestUnmarshalSnapshot_LeadingWhitespace(t *testing.T) {
	t.Parallel()

	raw := []byte("  \n\t{\"version\":1,\"resources\":[]}")
	got, version, err := snapshot.UnmarshalSnapshot(raw)
	require.NoError(t, err)
	assert.Equal(t, 1, version)
	assert.Empty(t, got)
}

// TestUnmarshalSnapshot_Empty verifies that nil and zero-length inputs
// return cleanly without error — useful for callers that may pass an
// uninitialized stack_versions.imported column.
func TestUnmarshalSnapshot_Empty(t *testing.T) {
	t.Parallel()

	got, version, err := snapshot.UnmarshalSnapshot(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, version)
	assert.Nil(t, got)

	got, version, err = snapshot.UnmarshalSnapshot([]byte{})
	require.NoError(t, err)
	assert.Equal(t, 0, version)
	assert.Nil(t, got)
}

// TestUnmarshalSnapshot_BadLeadingByte verifies that a payload that
// is neither array nor object yields a parse error rather than panic.
func TestUnmarshalSnapshot_BadLeadingByte(t *testing.T) {
	t.Parallel()

	_, _, err := snapshot.UnmarshalSnapshot([]byte("garbage"))
	require.Error(t, err)
}
