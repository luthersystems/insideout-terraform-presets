package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// makeBucketResult mirrors the CAI-fanout output that storage_bucket_object
// reads from priorResults. Keep the minimum field set the discoverer
// actually consumes (Type + NameHint) so the test signal stays focused.
func makeBucketResult(name string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     storageBucketTFType,
			NameHint: name,
			ImportID: name,
		},
	}
}

func TestStorageBucketObjectListNonCAI_FansOutAcrossPriors(t *testing.T) {
	t.Parallel()
	fake := &fakeBucketObjectLister{
		objectsByBucket: map[string][]gcpBucketObject{
			"bucket-a": {
				{Bucket: "bucket-a", Name: "foo.txt", Md5: "aa=="},
				{Bucket: "bucket-a", Name: "folder/bar.json", Md5: "bb=="},
			},
			"bucket-b": {
				{Bucket: "bucket-b", Name: "baz.yaml"},
			},
		},
	}
	d := newStorageBucketObjectDiscoverer(fake).(*storageBucketObjectDiscoverer)
	prior := []imported.ImportedResource{
		makeBucketResult("bucket-a"),
		makeBucketResult("bucket-b"),
		// Non-bucket prior: must be skipped (no spurious fanout).
		{Identity: imported.ResourceIdentity{Type: secretManagerSecretTFType, NameHint: "sec"}},
	}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Len(t, fake.calls, 2)

	byImport := map[string]bool{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = true
	}
	assert.True(t, byImport["bucket-a/foo.txt"])
	// Object names containing slashes must survive into the import ID
	// verbatim — the provider splits on the first slash only.
	assert.True(t, byImport["bucket-a/folder/bar.json"])
	assert.True(t, byImport["bucket-b/baz.yaml"])
}

func TestStorageBucketObjectListNonCAI_PerParentErrorSoftFails(t *testing.T) {
	t.Parallel()
	fake := &fakeBucketObjectLister{
		objectsByBucket: map[string][]gcpBucketObject{
			"bucket-a": {{Bucket: "bucket-a", Name: "foo.txt"}},
		},
		errByBucket: map[string]error{
			"bucket-b": errors.New("bucket not accessible"),
		},
	}
	d := newStorageBucketObjectDiscoverer(fake).(*storageBucketObjectDiscoverer)
	prior := []imported.ImportedResource{
		makeBucketResult("bucket-a"),
		makeBucketResult("bucket-b"),
	}
	rec := &recordingEmitter{}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, rec)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "bucket-a/foo.txt", got[0].Identity.ImportID)

	var warns []recordedEvent
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" {
			warns = append(warns, ev)
		}
	}
	require.Len(t, warns, 1)
	assert.Equal(t, nonCAIServiceSlug, warns[0].Service)
	assert.Contains(t, warns[0].Message, "bucket-b",
		"warn message must name the failing bucket")
	assert.Contains(t, warns[0].Message, "bucket not accessible",
		"warn message must include the underlying error")
}

func TestStorageBucketObjectListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newStorageBucketObjectDiscoverer(nil).(*storageBucketObjectDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", []imported.ImportedResource{makeBucketResult("b")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStorageBucketObjectListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeBucketObjectLister{}
	d := newStorageBucketObjectDiscoverer(fake).(*storageBucketObjectDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.calls, "lister must be untouched when no bucket priors exist")
}

// TestStorageBucketObjectListNonCAI_RespectsTruncationCap pins the
// per-bucket cap behavior. The fake lister flags `huge-bucket` as
// truncated; the discoverer must emit a ServiceWarn AND still surface
// the partial slice of rows (truncation is "stop early," not
// "discard everything"). Without this assertion a regression that
// either swallowed the warn or treated truncation as a soft-fail
// would compile green.
func TestStorageBucketObjectListNonCAI_RespectsTruncationCap(t *testing.T) {
	t.Parallel()
	// Three canned objects that simulate the truncated tip of a huge
	// bucket. The test doesn't need to actually generate >1000 rows —
	// the lister-side cap and discoverer-side warn are independently
	// testable thanks to the sentinel.
	partial := []gcpBucketObject{
		{Bucket: "huge-bucket", Name: "a.txt"},
		{Bucket: "huge-bucket", Name: "b.txt"},
		{Bucket: "huge-bucket", Name: "c.txt"},
	}
	fake := &fakeBucketObjectLister{
		objectsByBucket: map[string][]gcpBucketObject{
			"huge-bucket": partial,
			"small-bucket": {
				{Bucket: "small-bucket", Name: "only.txt"},
			},
		},
		truncateBucket: map[string]bool{"huge-bucket": true},
	}
	d := newStorageBucketObjectDiscoverer(fake).(*storageBucketObjectDiscoverer)
	prior := []imported.ImportedResource{
		makeBucketResult("huge-bucket"),
		makeBucketResult("small-bucket"),
	}
	rec := &recordingEmitter{}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, rec)
	require.NoError(t, err)
	// Partial slice for huge-bucket (3) + small-bucket (1) = 4 rows.
	require.Len(t, got, 4, "truncated bucket must still surface its partial rows")

	var warns []recordedEvent
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" {
			warns = append(warns, ev)
		}
	}
	require.Len(t, warns, 1, "exactly one truncation warn expected (small-bucket must not warn)")
	assert.Contains(t, warns[0].Message, "huge-bucket")
	assert.Contains(t, warns[0].Message, "truncated")
}

func TestStorageBucketObjectImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, bucket, object, want string
	}{
		{name: "simple name", bucket: "my-bucket", object: "foo.txt", want: "my-bucket/foo.txt"},
		{name: "folder-shaped key", bucket: "my-bucket", object: "a/b/c.json", want: "my-bucket/a/b/c.json"},
		{name: "leading slash preserved", bucket: "b", object: "/leading", want: "b//leading"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, storageBucketObjectImportID(tc.bucket, tc.object))
		})
	}
}

// TestDefaultMaxBucketObjects pins the per-bucket truncation cap.
// Bumping or lowering the cap without updating this test is a contract
// change — operator-visible behavior shifts (more or fewer object rows
// surface per bucket on huge buckets) and the corresponding
// "capped at 1000/bucket" note in gcp.json's purpose string drifts.
// The test exists purely as a tripwire against silent cap bumps.
func TestDefaultMaxBucketObjects(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1000, defaultMaxBucketObjects)
}
