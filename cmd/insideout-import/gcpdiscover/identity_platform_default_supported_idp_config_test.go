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

// makeIdentityPlatformConfigResult builds a minimal ImportedResource
// for the Identity Platform Config singleton — what the default-IDP
// discoverer reads from priorResults to gate its API call.
func makeIdentityPlatformConfigResult(project string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     identityPlatformConfigTFType,
			NameHint: "config",
			ImportID: project,
		},
	}
}

func TestIdentityPlatformDefaultSupportedIdpConfigListNonCAI_EmitsRowPerConfig(t *testing.T) {
	t.Parallel()
	fake := &fakeDefaultSupportedIdpConfigLister{
		configsByProject: map[string][]gcpDefaultSupportedIdpConfig{
			"real-proj": {
				{Name: "projects/real-proj/defaultSupportedIdpConfigs/google.com", IdpID: "google.com", Enabled: true},
				{Name: "projects/real-proj/defaultSupportedIdpConfigs/facebook.com", IdpID: "facebook.com", Enabled: false},
			},
		},
	}
	d := newIdentityPlatformDefaultSupportedIdpConfigDiscoverer(fake).(*identityPlatformDefaultSupportedIdpConfigDiscoverer)
	prior := []imported.ImportedResource{makeIdentityPlatformConfigResult("real-proj")}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 2)

	byImport := map[string]string{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = r.Identity.NameHint
	}
	assert.Equal(t, "google.com", byImport["projects/real-proj/defaultSupportedIdpConfigs/google.com"])
	assert.Equal(t, "facebook.com", byImport["projects/real-proj/defaultSupportedIdpConfigs/facebook.com"])
}

func TestIdentityPlatformDefaultSupportedIdpConfigListNonCAI_NoParentYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeDefaultSupportedIdpConfigLister{
		configsByProject: map[string][]gcpDefaultSupportedIdpConfig{
			"real-proj": {{Name: "projects/real-proj/defaultSupportedIdpConfigs/google.com", IdpID: "google.com"}},
		},
	}
	d := newIdentityPlatformDefaultSupportedIdpConfigDiscoverer(fake).(*identityPlatformDefaultSupportedIdpConfigDiscoverer)
	// Identity Platform not activated → no priors row → no API call,
	// no rows. Critical: without the gate the API would return a
	// hard error not an empty list, so every project that hasn't
	// activated Identity Platform would fail discover.
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.calls, "lister must be untouched without an Identity Platform Config parent prior")
}

func TestIdentityPlatformDefaultSupportedIdpConfigListNonCAI_RecoversIdpIDFromNameWhenAbsent(t *testing.T) {
	t.Parallel()
	// Belt-and-braces: a fake that sets only Name (not IdpID) should
	// still produce a row with the recovered IdpID.
	fake := &fakeDefaultSupportedIdpConfigLister{
		configsByProject: map[string][]gcpDefaultSupportedIdpConfig{
			"real-proj": {
				{Name: "projects/real-proj/defaultSupportedIdpConfigs/apple.com", Enabled: true},
			},
		},
	}
	d := newIdentityPlatformDefaultSupportedIdpConfigDiscoverer(fake).(*identityPlatformDefaultSupportedIdpConfigDiscoverer)
	prior := []imported.ImportedResource{makeIdentityPlatformConfigResult("real-proj")}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "apple.com", got[0].Identity.NameHint)
	assert.Equal(t, "projects/real-proj/defaultSupportedIdpConfigs/apple.com", got[0].Identity.ImportID)
}

func TestIdentityPlatformDefaultSupportedIdpConfigListNonCAI_ErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("permission denied")
	fake := &fakeDefaultSupportedIdpConfigLister{
		errByProject: map[string]error{"real-proj": want},
	}
	d := newIdentityPlatformDefaultSupportedIdpConfigDiscoverer(fake).(*identityPlatformDefaultSupportedIdpConfigDiscoverer)
	prior := []imported.ImportedResource{makeIdentityPlatformConfigResult("real-proj")}
	_, err := d.ListNonCAI(context.Background(), "real-proj", "", prior, progress.NopEmitter{})
	assert.ErrorIs(t, err, want)
}

func TestIdentityPlatformDefaultSupportedIdpConfigListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newIdentityPlatformDefaultSupportedIdpConfigDiscoverer(nil).(*identityPlatformDefaultSupportedIdpConfigDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "",
		[]imported.ImportedResource{makeIdentityPlatformConfigResult("real-proj")},
		progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestIdentityPlatformDefaultSupportedIdpConfigImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, project, idp, want string
	}{
		{name: "google", project: "p", idp: "google.com",
			want: "projects/p/defaultSupportedIdpConfigs/google.com"},
		{name: "apple", project: "real-proj", idp: "apple.com",
			want: "projects/real-proj/defaultSupportedIdpConfigs/apple.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, identityPlatformDefaultSupportedIdpConfigImportID(tc.project, tc.idp))
		})
	}
}
