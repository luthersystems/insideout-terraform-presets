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

func makeSecretResult(project, name string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     secretManagerSecretTFType,
			NameHint: name,
			ImportID: "projects/" + project + "/secrets/" + name,
		},
	}
}

func TestSecretManagerSecretIAMBindingListNonCAI_FansOutAcrossPriors(t *testing.T) {
	t.Parallel()
	secretA := "projects/p/secrets/sec-a"
	secretB := "projects/p/secrets/sec-b"
	fake := &fakeIAMPolicyLister{
		bindingsBySecret: map[string][]gcpIAMBinding{
			secretA: {
				{Role: "roles/secretmanager.secretAccessor", Members: []string{"serviceAccount:foo@p.iam.gserviceaccount.com", "user:alice@example.com"}},
				{Role: "roles/secretmanager.viewer", Members: []string{"user:bob@example.com"}},
			},
			secretB: {
				{Role: "roles/secretmanager.secretAccessor", Members: []string{"serviceAccount:bar@p.iam.gserviceaccount.com"}},
			},
		},
	}
	d := newSecretManagerSecretIAMBindingDiscoverer(fake).(*secretManagerSecretIAMBindingDiscoverer)
	prior := []imported.ImportedResource{
		makeSecretResult("p", "sec-a"),
		makeSecretResult("p", "sec-b"),
		{Identity: imported.ResourceIdentity{Type: storageBucketTFType, NameHint: "io-foo"}},
	}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	// Binding: 2 roles on secret-a, 1 role on secret-b → 3 rows.
	require.Len(t, got, 3)
	require.Len(t, fake.callsBySecret, 2)

	byImport := map[string]bool{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = true
	}
	assert.True(t, byImport["projects/p/secrets/sec-a roles/secretmanager.secretAccessor"])
	assert.True(t, byImport["projects/p/secrets/sec-b roles/secretmanager.secretAccessor"])
}

func TestSecretManagerSecretIAMBindingListNonCAI_PerParentErrorSoftFails(t *testing.T) {
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
	d := newSecretManagerSecretIAMBindingDiscoverer(fake).(*secretManagerSecretIAMBindingDiscoverer)
	prior := []imported.ImportedResource{
		makeSecretResult("p", "sec-a"),
		makeSecretResult("p", "sec-b"),
	}
	rec := &recordingEmitter{}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, rec)
	require.NoError(t, err)
	require.Len(t, got, 1)
	// Pin which parent's binding row survived.
	assert.Equal(t, secretA+" roles/secretmanager.viewer", got[0].Identity.ImportID)
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

func TestSecretManagerSecretIAMBindingListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newSecretManagerSecretIAMBindingDiscoverer(nil).(*secretManagerSecretIAMBindingDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", []imported.ImportedResource{makeSecretResult("p", "s")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestSecretManagerSecretIAMBindingListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{}
	d := newSecretManagerSecretIAMBindingDiscoverer(fake).(*secretManagerSecretIAMBindingDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.callsBySecret)
}

func TestSecretManagerSecretIAMBindingImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, secret, role, want string
	}{
		{name: "accessor", secret: "projects/p/secrets/sec", role: "roles/secretmanager.secretAccessor",
			want: "projects/p/secrets/sec roles/secretmanager.secretAccessor"},
		{name: "viewer", secret: "projects/p/secrets/other", role: "roles/secretmanager.viewer",
			want: "projects/p/secrets/other roles/secretmanager.viewer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, secretManagerSecretIAMBindingImportID(tc.secret, tc.role))
		})
	}
}
