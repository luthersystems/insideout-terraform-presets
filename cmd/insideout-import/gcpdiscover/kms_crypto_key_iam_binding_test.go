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

func makeCryptoKeyResult(project, location, ring, key string) imported.ImportedResource {
	importID := "projects/" + project + "/locations/" + location + "/keyRings/" + ring + "/cryptoKeys/" + key
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     kmsCryptoKeyTFType,
			NameHint: key,
			ImportID: importID,
			Location: location,
		},
	}
}

func TestKMSCryptoKeyIAMBindingListNonCAI_FansOutAcrossPriors(t *testing.T) {
	t.Parallel()
	keyA := "projects/p/locations/us-central1/keyRings/r/cryptoKeys/k-a"
	keyB := "projects/p/locations/us-east1/keyRings/r/cryptoKeys/k-b"
	fake := &fakeIAMPolicyLister{
		bindingsByKey: map[string][]gcpIAMBinding{
			keyA: {
				{Role: "roles/cloudkms.cryptoKeyEncrypterDecrypter", Members: []string{"serviceAccount:enc@p.iam.gserviceaccount.com", "serviceAccount:dec@p.iam.gserviceaccount.com"}},
				{Role: "roles/cloudkms.viewer", Members: []string{"user:alice@example.com"}},
			},
			keyB: {
				{Role: "roles/cloudkms.cryptoKeyEncrypter", Members: []string{"serviceAccount:other@p.iam.gserviceaccount.com"}},
			},
		},
	}
	d := newKMSCryptoKeyIAMBindingDiscoverer(fake).(*kmsCryptoKeyIAMBindingDiscoverer)
	prior := []imported.ImportedResource{
		makeCryptoKeyResult("p", "us-central1", "r", "k-a"),
		makeCryptoKeyResult("p", "us-east1", "r", "k-b"),
		{Identity: imported.ResourceIdentity{Type: storageBucketTFType, NameHint: "io-foo"}},
	}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	// Binding: one row per (key × role). 2+1 = 3 rows.
	require.Len(t, got, 3)
	require.Len(t, fake.callsByKey, 2)

	byImport := map[string]bool{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = true
	}
	assert.True(t, byImport["p/us-central1/r/k-a roles/cloudkms.cryptoKeyEncrypterDecrypter"])
	assert.True(t, byImport["p/us-east1/r/k-b roles/cloudkms.cryptoKeyEncrypter"])
}

func TestKMSCryptoKeyIAMBindingListNonCAI_PerParentErrorSoftFails(t *testing.T) {
	t.Parallel()
	keyA := "projects/p/locations/us-central1/keyRings/r/cryptoKeys/k-a"
	keyB := "projects/p/locations/us-east1/keyRings/r/cryptoKeys/k-b"
	fake := &fakeIAMPolicyLister{
		bindingsByKey: map[string][]gcpIAMBinding{
			keyA: {{Role: "roles/cloudkms.viewer", Members: []string{"user:alice@example.com"}}},
		},
		errByKey: map[string]error{
			keyB: errors.New("key not accessible"),
		},
	}
	d := newKMSCryptoKeyIAMBindingDiscoverer(fake).(*kmsCryptoKeyIAMBindingDiscoverer)
	prior := []imported.ImportedResource{
		makeCryptoKeyResult("p", "us-central1", "r", "k-a"),
		makeCryptoKeyResult("p", "us-east1", "r", "k-b"),
	}
	rec := &recordingEmitter{}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, rec)
	require.NoError(t, err)
	require.Len(t, got, 1)
	var warns []recordedEvent
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" {
			warns = append(warns, ev)
		}
	}
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0].Message, "k-b")
	assert.Contains(t, warns[0].Message, "key not accessible")
}

func TestKMSCryptoKeyIAMBindingListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newKMSCryptoKeyIAMBindingDiscoverer(nil).(*kmsCryptoKeyIAMBindingDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", []imported.ImportedResource{makeCryptoKeyResult("p", "us-central1", "r", "k")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestKMSCryptoKeyIAMBindingListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{}
	d := newKMSCryptoKeyIAMBindingDiscoverer(fake).(*kmsCryptoKeyIAMBindingDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.callsByKey)
}

func TestKMSCryptoKeyIAMBindingImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, project, loc, ring, key, role, want string
	}{
		{name: "viewer", project: "p", loc: "us-central1", ring: "r", key: "k", role: "roles/cloudkms.viewer",
			want: "p/us-central1/r/k roles/cloudkms.viewer"},
		{name: "encrypter", project: "p", loc: "global", ring: "r", key: "k", role: "roles/cloudkms.cryptoKeyEncrypter",
			want: "p/global/r/k roles/cloudkms.cryptoKeyEncrypter"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, kmsCryptoKeyIAMBindingImportID(tc.project, tc.loc, tc.ring, tc.key, tc.role))
		})
	}
}
