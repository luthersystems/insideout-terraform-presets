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

func makeCloudRunServiceResult(project, location, name string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     cloudRunV2ServiceTFType,
			NameHint: name,
			ImportID: "projects/" + project + "/locations/" + location + "/services/" + name,
			Location: location,
		},
	}
}

func TestCloudRunV2ServiceIAMMemberListNonCAI_FansOutAcrossPriors(t *testing.T) {
	t.Parallel()
	svcA := "projects/p/locations/us-central1/services/svc-a"
	svcB := "projects/p/locations/us-east1/services/svc-b"
	fake := &fakeIAMPolicyLister{
		bindingsByService: map[string][]gcpIAMBinding{
			svcA: {
				{Role: "roles/run.invoker", Members: []string{"allUsers", "user:alice@example.com"}},
			},
			svcB: {
				{Role: "roles/run.invoker", Members: []string{"serviceAccount:foo@p.iam.gserviceaccount.com"}},
			},
		},
	}
	d := newCloudRunV2ServiceIAMMemberDiscoverer(fake).(*cloudRunV2ServiceIAMMemberDiscoverer)
	prior := []imported.ImportedResource{
		makeCloudRunServiceResult("p", "us-central1", "svc-a"),
		makeCloudRunServiceResult("p", "us-east1", "svc-b"),
		{Identity: imported.ResourceIdentity{Type: storageBucketTFType, NameHint: "io-foo"}},
	}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Len(t, fake.callsByService, 2)

	byImport := map[string]bool{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = true
	}
	assert.True(t, byImport["projects/p/locations/us-central1/services/svc-a roles/run.invoker allUsers"])
	assert.True(t, byImport["projects/p/locations/us-east1/services/svc-b roles/run.invoker serviceAccount:foo@p.iam.gserviceaccount.com"])
}

func TestCloudRunV2ServiceIAMMemberListNonCAI_PerParentErrorSoftFails(t *testing.T) {
	t.Parallel()
	svcA := "projects/p/locations/us-central1/services/svc-a"
	svcB := "projects/p/locations/us-east1/services/svc-b"
	fake := &fakeIAMPolicyLister{
		bindingsByService: map[string][]gcpIAMBinding{
			svcA: {{Role: "roles/run.invoker", Members: []string{"user:alice@example.com"}}},
		},
		errByService: map[string]error{
			svcB: errors.New("service not accessible"),
		},
	}
	d := newCloudRunV2ServiceIAMMemberDiscoverer(fake).(*cloudRunV2ServiceIAMMemberDiscoverer)
	prior := []imported.ImportedResource{
		makeCloudRunServiceResult("p", "us-central1", "svc-a"),
		makeCloudRunServiceResult("p", "us-east1", "svc-b"),
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
	assert.Contains(t, warns[0].Message, "svc-b")
	assert.Contains(t, warns[0].Message, "service not accessible")
}

func TestCloudRunV2ServiceIAMMemberListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newCloudRunV2ServiceIAMMemberDiscoverer(nil).(*cloudRunV2ServiceIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", []imported.ImportedResource{makeCloudRunServiceResult("p", "us-central1", "s")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestCloudRunV2ServiceIAMMemberListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{}
	d := newCloudRunV2ServiceIAMMemberDiscoverer(fake).(*cloudRunV2ServiceIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.callsByService)
}

func TestCloudRunV2ServiceIAMMemberImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, service, role, member, want string
	}{
		{name: "invoker user", service: "projects/p/locations/us-central1/services/svc", role: "roles/run.invoker", member: "user:alice@example.com",
			want: "projects/p/locations/us-central1/services/svc roles/run.invoker user:alice@example.com"},
		{name: "invoker all-users", service: "projects/p/locations/europe-west1/services/svc", role: "roles/run.invoker", member: "allUsers",
			want: "projects/p/locations/europe-west1/services/svc roles/run.invoker allUsers"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, cloudRunV2ServiceIAMMemberImportID(tc.service, tc.role, tc.member))
		})
	}
}
