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

func makeCloudFunction2Result(project, location, name string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     cloudFunctions2FunctionTFType,
			NameHint: name,
			ImportID: "projects/" + project + "/locations/" + location + "/functions/" + name,
			Location: location,
		},
	}
}

func TestCloudFunctions2FunctionIAMMemberListNonCAI_FansOutAcrossPriors(t *testing.T) {
	t.Parallel()
	fnA := "projects/p/locations/us-central1/functions/fn-a"
	fnB := "projects/p/locations/us-east1/functions/fn-b"
	fake := &fakeIAMPolicyLister{
		bindingsByFunction: map[string][]gcpIAMBinding{
			fnA: {
				{Role: "roles/cloudfunctions.invoker", Members: []string{"allUsers", "user:alice@example.com"}},
			},
			fnB: {
				{Role: "roles/cloudfunctions.invoker", Members: []string{"serviceAccount:foo@p.iam.gserviceaccount.com"}},
			},
		},
	}
	d := newCloudFunctions2FunctionIAMMemberDiscoverer(fake).(*cloudFunctions2FunctionIAMMemberDiscoverer)
	prior := []imported.ImportedResource{
		makeCloudFunction2Result("p", "us-central1", "fn-a"),
		makeCloudFunction2Result("p", "us-east1", "fn-b"),
		{Identity: imported.ResourceIdentity{Type: storageBucketTFType, NameHint: "io-foo"}},
	}
	got, err := d.ListNonCAI(context.Background(), "p", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Len(t, fake.callsByFunction, 2)

	byImport := map[string]bool{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = true
	}
	assert.True(t, byImport["projects/p/locations/us-central1/functions/fn-a roles/cloudfunctions.invoker allUsers"])
	assert.True(t, byImport["projects/p/locations/us-east1/functions/fn-b roles/cloudfunctions.invoker serviceAccount:foo@p.iam.gserviceaccount.com"])
}

func TestCloudFunctions2FunctionIAMMemberListNonCAI_PerParentErrorSoftFails(t *testing.T) {
	t.Parallel()
	fnA := "projects/p/locations/us-central1/functions/fn-a"
	fnB := "projects/p/locations/us-east1/functions/fn-b"
	fake := &fakeIAMPolicyLister{
		bindingsByFunction: map[string][]gcpIAMBinding{
			fnA: {{Role: "roles/cloudfunctions.invoker", Members: []string{"user:alice@example.com"}}},
		},
		errByFunction: map[string]error{
			fnB: errors.New("function not accessible"),
		},
	}
	d := newCloudFunctions2FunctionIAMMemberDiscoverer(fake).(*cloudFunctions2FunctionIAMMemberDiscoverer)
	prior := []imported.ImportedResource{
		makeCloudFunction2Result("p", "us-central1", "fn-a"),
		makeCloudFunction2Result("p", "us-east1", "fn-b"),
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
	assert.Contains(t, warns[0].Message, "fn-b")
	assert.Contains(t, warns[0].Message, "function not accessible")
}

func TestCloudFunctions2FunctionIAMMemberListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newCloudFunctions2FunctionIAMMemberDiscoverer(nil).(*cloudFunctions2FunctionIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", []imported.ImportedResource{makeCloudFunction2Result("p", "us-central1", "f")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestCloudFunctions2FunctionIAMMemberListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{}
	d := newCloudFunctions2FunctionIAMMemberDiscoverer(fake).(*cloudFunctions2FunctionIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "p", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.callsByFunction)
}

func TestCloudFunctions2FunctionIAMMemberImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, fn, role, member, want string
	}{
		{name: "invoker user", fn: "projects/p/locations/us-central1/functions/fn", role: "roles/cloudfunctions.invoker", member: "user:alice@example.com",
			want: "projects/p/locations/us-central1/functions/fn roles/cloudfunctions.invoker user:alice@example.com"},
		{name: "invoker all-users", fn: "projects/p/locations/europe-west1/functions/fn", role: "roles/cloudfunctions.invoker", member: "allUsers",
			want: "projects/p/locations/europe-west1/functions/fn roles/cloudfunctions.invoker allUsers"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, cloudFunctions2FunctionIAMMemberImportID(tc.fn, tc.role, tc.member))
		})
	}
}
