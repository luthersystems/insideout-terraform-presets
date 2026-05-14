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

func TestSecretManagerSecretIAMMemberListNonCAI_FansOutAcrossPriors(t *testing.T) {
	t.Parallel()
	secretA := "projects/p/secrets/sec-a"
	secretB := "projects/p/secrets/sec-b"
	fake := &fakeIAMPolicyLister{
		bindingsBySecret: map[string][]gcpIAMBinding{
			secretA: {
				{Role: "roles/secretmanager.secretAccessor", Members: []string{"serviceAccount:foo@p.iam.gserviceaccount.com", "user:alice@example.com"}},
			},
			secretB: {
				{Role: "roles/secretmanager.viewer", Members: []string{"allUsers"}},
			},
		},
	}
	d := newSecretManagerSecretIAMMemberDiscoverer(fake).(*secretManagerSecretIAMMemberDiscoverer)
	prior := []imported.ImportedResource{
		makeSecretResult("p", "sec-a"),
		makeSecretResult("p", "sec-b"),
		{Identity: imported.ResourceIdentity{Type: storageBucketTFType, NameHint: "io-foo"}},
	}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	// Member: 2+1 = 3 rows (fanned per member).
	require.Len(t, got, 3)
	require.Len(t, fake.callsBySecret, 2)

	byImport := map[string]bool{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = true
	}
	assert.True(t, byImport["projects/p/secrets/sec-a roles/secretmanager.secretAccessor user:alice@example.com"])
	assert.True(t, byImport["projects/p/secrets/sec-b roles/secretmanager.viewer allUsers"])
}

func TestSecretManagerSecretIAMMemberListNonCAI_PerParentErrorSoftFails(t *testing.T) {
	t.Parallel()
	secretA := "projects/p/secrets/sec-a"
	secretB := "projects/p/secrets/sec-b"
	fake := &fakeIAMPolicyLister{
		bindingsBySecret: map[string][]gcpIAMBinding{
			secretA: {{Role: "roles/secretmanager.viewer", Members: []string{"user:alice@example.com"}}},
		},
		errBySecret: map[string]error{
			secretB: errors.New("secret not accessible"),
		},
	}
	d := newSecretManagerSecretIAMMemberDiscoverer(fake).(*secretManagerSecretIAMMemberDiscoverer)
	prior := []imported.ImportedResource{
		makeSecretResult("p", "sec-a"),
		makeSecretResult("p", "sec-b"),
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
	assert.Contains(t, warns[0].Message, "sec-b")
	assert.Contains(t, warns[0].Message, "secret not accessible")
}

func TestSecretManagerSecretIAMMemberListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newSecretManagerSecretIAMMemberDiscoverer(nil).(*secretManagerSecretIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", []imported.ImportedResource{makeSecretResult("p", "s")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestSecretManagerSecretIAMMemberListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{}
	d := newSecretManagerSecretIAMMemberDiscoverer(fake).(*secretManagerSecretIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.callsBySecret)
}

func TestSecretManagerSecretIAMMemberImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, secret, role, member, want string
	}{
		{name: "service account", secret: "projects/p/secrets/sec", role: "roles/secretmanager.secretAccessor", member: "serviceAccount:foo@p.iam.gserviceaccount.com",
			want: "projects/p/secrets/sec roles/secretmanager.secretAccessor serviceAccount:foo@p.iam.gserviceaccount.com"},
		{name: "user", secret: "projects/p/secrets/sec", role: "roles/secretmanager.viewer", member: "user:alice@example.com",
			want: "projects/p/secrets/sec roles/secretmanager.viewer user:alice@example.com"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, secretManagerSecretIAMMemberImportID(tc.secret, tc.role, tc.member))
		})
	}
}
