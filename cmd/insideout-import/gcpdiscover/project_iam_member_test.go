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

// TestProjectIAMMemberListNonCAI_QueriesProject pins the singleton-
// fanout path: project_iam_member calls the lister exactly once per
// run, ignoring priorResults entirely (the project is a singleton in
// every discover scope). One ImportedResource row per (role × member).
func TestProjectIAMMemberListNonCAI_QueriesProject(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{
		bindingsProject: map[string][]gcpIAMBinding{
			"real-proj": {
				{Role: "roles/storage.objectViewer", Members: []string{"user:alice@example.com", "serviceAccount:foo@real-proj.iam.gserviceaccount.com"}},
				{Role: "roles/run.invoker", Members: []string{"allUsers"}},
			},
		},
	}
	d := newProjectIAMMemberDiscoverer(fake).(*projectIAMMemberDiscoverer)
	// priorResults is intentionally non-empty to confirm the discoverer
	// does NOT iterate it for project-level bindings.
	prior := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: storageBucketTFType, NameHint: "io-foo-bucket"}},
	}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 3, "want 3 rows (2 members on objectViewer + 1 on invoker)")
	require.Len(t, fake.callsProject, 1, "project IAM policy must be queried exactly once (singleton fanout)")

	// Pin the import-ID format for one row.
	byImport := map[string]string{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = r.Identity.NameHint
	}
	assert.Contains(t, byImport, "real-proj roles/storage.objectViewer user:alice@example.com")
	assert.Contains(t, byImport, "real-proj roles/run.invoker allUsers")
}

func TestProjectIAMMemberListNonCAI_PerParentErrorSoftFails(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{
		errProject: map[string]error{
			"real-proj": errors.New("project not accessible"),
		},
	}
	d := newProjectIAMMemberDiscoverer(fake).(*projectIAMMemberDiscoverer)
	rec := &recordingEmitter{}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, rec)
	require.NoError(t, err, "expected soft-fail")
	require.Empty(t, got)

	var warns []recordedEvent
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" {
			warns = append(warns, ev)
		}
	}
	require.Len(t, warns, 1, "want one ServiceWarn for the soft-failed project")
	assert.Equal(t, nonCAIServiceSlug, warns[0].Service)
	assert.Contains(t, warns[0].Message, "real-proj")
	assert.Contains(t, warns[0].Message, "project not accessible")
}

func TestProjectIAMMemberListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newProjectIAMMemberDiscoverer(nil).(*projectIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

// TestProjectIAMMemberListNonCAI_NoPriorParentsYieldsNoFanout is a
// degenerate-case mirror of the sibling tests on parent-scoped
// discoverers — for project IAM the lister IS still called because
// the project itself is the singleton parent. The test pins that
// behavior so a future refactor that confused project_iam_member with
// parent-scoped fanout fails loudly.
func TestProjectIAMMemberListNonCAI_NoPriorParentsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyLister{
		bindingsProject: map[string][]gcpIAMBinding{
			"real-proj": {{Role: "roles/viewer", Members: []string{"user:alice@example.com"}}},
		},
	}
	d := newProjectIAMMemberDiscoverer(fake).(*projectIAMMemberDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	// Project IAM IS queried even with no priors (singleton parent).
	require.Len(t, got, 1)
	require.Len(t, fake.callsProject, 1)
}

func TestProjectIAMMemberImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, project, role, member, want string
	}{
		{name: "service account", project: "p", role: "roles/storage.objectViewer", member: "serviceAccount:foo@p.iam.gserviceaccount.com", want: "p roles/storage.objectViewer serviceAccount:foo@p.iam.gserviceaccount.com"},
		{name: "user", project: "p", role: "roles/run.invoker", member: "user:alice@example.com", want: "p roles/run.invoker user:alice@example.com"},
		{name: "all users", project: "p", role: "roles/run.invoker", member: "allUsers", want: "p roles/run.invoker allUsers"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, projectIAMMemberImportID(tc.project, tc.role, tc.member))
		})
	}
}
