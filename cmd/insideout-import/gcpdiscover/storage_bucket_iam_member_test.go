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

func makeStorageBucketResult(name string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     storageBucketTFType,
			NameHint: name,
			ImportID: name,
		},
	}
}

func TestStorageBucketIAMMemberListNonCAI_FansOutAcrossPriors(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{
		bindingsByBucket: map[string][]gcpIAMBinding{
			"bucket-a": {
				{Role: "roles/storage.objectViewer", Members: []string{"user:alice@example.com", "user:bob@example.com"}},
				{Role: "roles/storage.objectCreator", Members: []string{"serviceAccount:writer@p.iam.gserviceaccount.com"}},
			},
			"bucket-b": {
				{Role: "roles/storage.legacyBucketReader", Members: []string{"allUsers"}},
			},
		},
	}
	d := newStorageBucketIAMMemberDiscoverer(fake).(*storageBucketIAMMemberDiscoverer)
	prior := []imported.ImportedResource{
		makeStorageBucketResult("bucket-a"),
		makeStorageBucketResult("bucket-b"),
		// Non-bucket resource: must be ignored.
		{Identity: imported.ResourceIdentity{Type: secretManagerSecretTFType, NameHint: "my-secret"}},
	}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 4, "2+1 from bucket-a, 1 from bucket-b")
	require.Len(t, fake.callsByBucket, 2, "exactly one IAM call per bucket prior")

	byImport := map[string]string{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = r.Identity.NameHint
	}
	assert.Contains(t, byImport, "b/bucket-a roles/storage.objectViewer user:alice@example.com")
	assert.Contains(t, byImport, "b/bucket-b roles/storage.legacyBucketReader allUsers")
}

func TestStorageBucketIAMMemberListNonCAI_PerParentErrorSoftFails(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{
		bindingsByBucket: map[string][]gcpIAMBinding{
			"bucket-a": {{Role: "roles/storage.objectViewer", Members: []string{"user:alice@example.com"}}},
		},
		errByBucket: map[string]error{
			"bucket-b": errors.New("bucket not accessible"),
		},
	}
	d := newStorageBucketIAMMemberDiscoverer(fake).(*storageBucketIAMMemberDiscoverer)
	prior := []imported.ImportedResource{
		makeStorageBucketResult("bucket-a"),
		makeStorageBucketResult("bucket-b"),
	}
	rec := &recordingEmitter{}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior, rec)
	require.NoError(t, err)
	require.Len(t, got, 1)
	var warns []recordedEvent
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" {
			warns = append(warns, ev)
		}
	}
	require.Len(t, warns, 1)
	assert.Equal(t, nonCAIServiceSlug, warns[0].Service)
	assert.Contains(t, warns[0].Message, "bucket-b")
	assert.Contains(t, warns[0].Message, "bucket not accessible")
}

func TestStorageBucketIAMMemberListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newStorageBucketIAMMemberDiscoverer(nil).(*storageBucketIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", []imported.ImportedResource{makeStorageBucketResult("b")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStorageBucketIAMMemberListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{}
	d := newStorageBucketIAMMemberDiscoverer(fake).(*storageBucketIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.callsByBucket, "lister must be untouched when no parents to fan out")
}

func TestStorageBucketIAMMemberImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, bucket, role, member, want string
	}{
		{name: "user", bucket: "my-bucket", role: "roles/storage.objectViewer", member: "user:alice@example.com", want: "b/my-bucket roles/storage.objectViewer user:alice@example.com"},
		{name: "service account", bucket: "my-bucket", role: "roles/storage.objectCreator", member: "serviceAccount:foo@p.iam.gserviceaccount.com", want: "b/my-bucket roles/storage.objectCreator serviceAccount:foo@p.iam.gserviceaccount.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, storageBucketIAMMemberImportID(tc.bucket, tc.role, tc.member))
		})
	}
}
