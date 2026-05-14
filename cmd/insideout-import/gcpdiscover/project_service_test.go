package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
)

func TestProjectServiceListNonCAI_EmitsRowPerEnabledService(t *testing.T) {
	t.Parallel()
	fake := &fakeProjectServiceLister{
		servicesByProject: map[string][]gcpEnabledService{
			"real-proj": {
				{Service: "secretmanager.googleapis.com", State: "ENABLED"},
				{Service: "pubsub.googleapis.com", State: "ENABLED"},
				{Service: "compute.googleapis.com", State: "ENABLED"},
			},
		},
	}
	d := newProjectServiceDiscoverer(fake).(*projectServiceDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Len(t, fake.calls, 1, "lister should be called exactly once per project")

	byImport := map[string]string{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = r.Identity.NameHint
	}
	// Import-ID per provider docs: "<project>/<service>".
	assert.Equal(t, "secretmanager.googleapis.com", byImport["real-proj/secretmanager.googleapis.com"])
	assert.Equal(t, "pubsub.googleapis.com", byImport["real-proj/pubsub.googleapis.com"])
	assert.Equal(t, "compute.googleapis.com", byImport["real-proj/compute.googleapis.com"])
}

func TestProjectServiceListNonCAI_ErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("api not enabled")
	fake := &fakeProjectServiceLister{
		errByProject: map[string]error{"real-proj": want},
	}
	d := newProjectServiceDiscoverer(fake).(*projectServiceDiscoverer)
	_, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	assert.ErrorIs(t, err, want)
}

func TestProjectServiceListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newProjectServiceDiscoverer(nil).(*projectServiceDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestProjectServiceListNonCAI_EmptyResultYieldsNil(t *testing.T) {
	t.Parallel()
	fake := &fakeProjectServiceLister{
		servicesByProject: map[string][]gcpEnabledService{
			"real-proj": {},
		},
	}
	d := newProjectServiceDiscoverer(fake).(*projectServiceDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestProjectServiceImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, project, service, want string
	}{
		{name: "standard", project: "my-proj", service: "secretmanager.googleapis.com",
			want: "my-proj/secretmanager.googleapis.com"},
		{name: "numeric project", project: "123456", service: "compute.googleapis.com",
			want: "123456/compute.googleapis.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, projectServiceImportID(tc.project, tc.service))
		})
	}
}
