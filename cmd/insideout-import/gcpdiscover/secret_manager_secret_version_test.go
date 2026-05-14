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

func TestSecretManagerSecretVersionListNonCAI_FansOutAcrossPriors(t *testing.T) {
	t.Parallel()
	secretA := "projects/p/secrets/sec-a"
	secretB := "projects/p/secrets/sec-b"
	fake := &fakeSecretVersionLister{
		versionsBySecret: map[string][]gcpSecretVersion{
			secretA: {
				{Name: secretA + "/versions/1", SecretFull: secretA, Version: "1", State: "ENABLED"},
				{Name: secretA + "/versions/2", SecretFull: secretA, Version: "2", State: "DISABLED"},
			},
			secretB: {
				{Name: secretB + "/versions/1", SecretFull: secretB, Version: "1", State: "ENABLED"},
			},
		},
	}
	d := newSecretManagerSecretVersionDiscoverer(fake).(*secretManagerSecretVersionDiscoverer)
	prior := []imported.ImportedResource{
		makeSecretResult("p", "sec-a"),
		makeSecretResult("p", "sec-b"),
		// Non-secret prior: must be skipped (no spurious fanout).
		{Identity: imported.ResourceIdentity{Type: storageBucketTFType, NameHint: "io-foo"}},
	}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	// Two parent secrets queried — non-secret prior didn't trigger a list.
	require.Len(t, fake.calls, 2)

	byImport := map[string]bool{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = true
	}
	assert.True(t, byImport[secretA+"/versions/1"])
	assert.True(t, byImport[secretA+"/versions/2"])
	assert.True(t, byImport[secretB+"/versions/1"])
}

func TestSecretManagerSecretVersionListNonCAI_PerParentErrorSoftFails(t *testing.T) {
	t.Parallel()
	secretA := "projects/p/secrets/sec-a"
	secretB := "projects/p/secrets/sec-b"
	fake := &fakeSecretVersionLister{
		versionsBySecret: map[string][]gcpSecretVersion{
			secretA: {{Name: secretA + "/versions/1", SecretFull: secretA, Version: "1", State: "ENABLED"}},
		},
		errBySecret: map[string]error{
			secretB: errors.New("secret not accessible"),
		},
	}
	d := newSecretManagerSecretVersionDiscoverer(fake).(*secretManagerSecretVersionDiscoverer)
	prior := []imported.ImportedResource{
		makeSecretResult("p", "sec-a"),
		makeSecretResult("p", "sec-b"),
	}
	rec := &recordingEmitter{}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, rec)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, secretA+"/versions/1", got[0].Identity.ImportID)

	var warns []recordedEvent
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" {
			warns = append(warns, ev)
		}
	}
	require.Len(t, warns, 1)
	assert.Equal(t, nonCAIServiceSlug, warns[0].Service)
	// Two separate Contains checks so a failure pinpoints which
	// fragment is missing (parent path vs underlying error) rather
	// than blanket-reporting "missing either".
	assert.Contains(t, warns[0].Message, "sec-b",
		"warn message must name the failing secret")
	assert.Contains(t, warns[0].Message, "secret not accessible",
		"warn message must include the underlying error")
}

func TestSecretManagerSecretVersionListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newSecretManagerSecretVersionDiscoverer(nil).(*secretManagerSecretVersionDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", []imported.ImportedResource{makeSecretResult("p", "s")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestSecretManagerSecretVersionListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeSecretVersionLister{}
	d := newSecretManagerSecretVersionDiscoverer(fake).(*secretManagerSecretVersionDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.calls, "lister must be untouched when no secret priors exist")
}

func TestSecretManagerSecretVersionImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, secret, version, want string
	}{
		{name: "numeric version", secret: "projects/p/secrets/sec", version: "1",
			want: "projects/p/secrets/sec/versions/1"},
		{name: "latest alias", secret: "projects/p/secrets/other", version: "latest",
			want: "projects/p/secrets/other/versions/latest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, secretManagerSecretVersionImportID(tc.secret, tc.version))
		})
	}
}
