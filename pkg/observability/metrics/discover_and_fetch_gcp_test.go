package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// =============================================================================
// DiscoverAndFetchGCP — parity port of reliable's getGCPServiceMetrics
// tests (internal/agentapi/gcp_metrics_test.go).
// =============================================================================

// withGCPClients swaps the GCP monitoring-client constructor so
// DiscoverAndFetchGCP's metric path runs against a mocked Cloud
// Monitoring client (fakeMonitoring lives in gcp_test.go).
func withGCPClients(t *testing.T, mon MonitoringAPI) {
	t.Helper()
	orig := newGCPClientsForFetch
	t.Cleanup(func() { newGCPClientsForFetch = orig })
	newGCPClientsForFetch = func(_ context.Context, projectID string, _ ...option.ClientOption) (*GCPClients, error) {
		return &GCPClients{ProjectID: projectID, Monitoring: mon}, nil
	}
}

// TestDiscoverAndFetchGCP_Compute drives the Cloud Monitoring path end to
// end with a mocked monitoring client and asserts reliable's MetricsResult
// wire shape. Cloud Monitoring returns every resource publishing the
// metric, so there is no per-service discovery — DiscoverAndFetchGCP
// passes a nil resource list to FetchGCP (parity with reliable).
func TestDiscoverAndFetchGCP_Compute(t *testing.T) {
	now := time.Now().UTC()
	ts1 := timestamppb.New(now.Add(-10 * time.Minute))
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-abc123", "zone": "us-central1-a"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: ts1}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.45}}},
					},
				},
			},
		},
	}
	withGCPClients(t, mon)

	got, err := DiscoverAndFetchGCP(context.Background(), "test-project", "compute", `{"hours":12,"period":600}`)
	require.NoError(t, err)

	mr, ok := got.(MetricsResult)
	require.True(t, ok, "Cloud Monitoring path must return a MetricsResult value (reliable parity)")
	assert.Equal(t, "compute", mr.Service)
	assert.Equal(t, 600, mr.Period)
	assert.Contains(t, mr.TimeRange, "12")
	require.Len(t, mr.Resources, 1)
	assert.Equal(t, "i-abc123", mr.Resources[0].ResourceID)

	// One ListTimeSeries call per metric in the compute spec.
	obs := gcpSpec(t, composer.KeyGCPCompute)
	assert.Len(t, mon.calls, len(obs.Metrics))
}

// TestDiscoverAndFetchGCP_BastionAlias pins the bastion→compute alias:
// the bastion service resolves to the compute spec, yet the result's
// Service field carries the caller's original "bastion" (reliable
// passes service, not resolvedService, to FetchGCP).
func TestDiscoverAndFetchGCP_BastionAlias(t *testing.T) {
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "bastion-vm"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.1}}},
					},
				},
			},
		},
	}
	withGCPClients(t, mon)

	got, err := DiscoverAndFetchGCP(context.Background(), "test-project", "bastion", "")
	require.NoError(t, err)
	mr, ok := got.(MetricsResult)
	require.True(t, ok)
	assert.Equal(t, "bastion", mr.Service, "result Service must echo the caller's service, not the resolved alias")
	// The compute metric set was queried (alias resolved to compute spec).
	obs := gcpSpec(t, composer.KeyGCPCompute)
	assert.Len(t, mon.calls, len(obs.Metrics))
	require.Len(t, mr.Resources, 1)
	assert.Equal(t, "bastion-vm", mr.Resources[0].ResourceID)
}

// TestDiscoverAndFetchGCP_UnknownService pins the error for a GCP service
// with no metric catalog entry.
func TestDiscoverAndFetchGCP_UnknownService(t *testing.T) {
	t.Parallel()
	got, err := DiscoverAndFetchGCP(context.Background(), "test-project", "totally-unknown", "")
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "no metric definitions for GCP service: totally-unknown")
}

// --- GCP Secret Manager health ---

// sliceSecretIterator / sliceVersionIterator are minimal in-memory
// iterators satisfying the narrow iterator interfaces.
type sliceSecretIterator struct {
	items []*secretmanagerpb.Secret
	i     int
}

func (s *sliceSecretIterator) Next() (*secretmanagerpb.Secret, error) {
	if s.i >= len(s.items) {
		return nil, iterator.Done
	}
	v := s.items[s.i]
	s.i++
	return v, nil
}

type sliceVersionIterator struct {
	n int
	i int
}

func (s *sliceVersionIterator) Next() (*secretmanagerpb.SecretVersion, error) {
	if s.i >= s.n {
		return nil, iterator.Done
	}
	s.i++
	return &secretmanagerpb.SecretVersion{}, nil
}

type fakeGCPSecretClient struct {
	secrets        []*secretmanagerpb.Secret
	versionsByName map[string]int
	closed         bool
}

func (f *fakeGCPSecretClient) ListSecrets(_ context.Context, _ *secretmanagerpb.ListSecretsRequest) gcpSecretIterator {
	return &sliceSecretIterator{items: f.secrets}
}

func (f *fakeGCPSecretClient) ListSecretVersions(_ context.Context, req *secretmanagerpb.ListSecretVersionsRequest) gcpSecretVersionIterator {
	return &sliceVersionIterator{n: f.versionsByName[req.Parent]}
}

func (f *fakeGCPSecretClient) Close() error { f.closed = true; return nil }

func withGCPSecretClient(t *testing.T, f gcpSecretManagerAPI) {
	t.Helper()
	orig := newGCPSecretClient
	t.Cleanup(func() { newGCPSecretClient = orig })
	newGCPSecretClient = func(_ context.Context, _ ...option.ClientOption) (gcpSecretManagerAPI, error) {
		return f, nil
	}
}

// TestDiscoverAndFetchGCP_SecretManagerHealth asserts the secretmanager
// branch returns a *GCPSecretHealthResult with reliable's field shape
// (replication-type dispatch, RFC3339 create time, version count).
func TestDiscoverAndFetchGCP_SecretManagerHealth(t *testing.T) {
	created := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	f := &fakeGCPSecretClient{
		secrets: []*secretmanagerpb.Secret{
			{
				Name:        "projects/test-project/secrets/auto-secret",
				CreateTime:  timestamppb.New(created),
				Replication: &secretmanagerpb.Replication{Replication: &secretmanagerpb.Replication_Automatic_{Automatic: &secretmanagerpb.Replication_Automatic{}}},
			},
			{
				Name:        "projects/test-project/secrets/usermanaged-secret",
				Replication: &secretmanagerpb.Replication{Replication: &secretmanagerpb.Replication_UserManaged_{UserManaged: &secretmanagerpb.Replication_UserManaged{}}},
			},
		},
		versionsByName: map[string]int{
			"projects/test-project/secrets/auto-secret":        3,
			"projects/test-project/secrets/usermanaged-secret": 1,
		},
	}
	withGCPSecretClient(t, f)

	got, err := DiscoverAndFetchGCP(context.Background(), "test-project", "secretmanager", "")
	require.NoError(t, err)

	res, ok := got.(*GCPSecretHealthResult)
	require.True(t, ok, "secretmanager path must return *GCPSecretHealthResult (reliable parity)")
	assert.Equal(t, "secretmanager", res.Service)
	assert.NotEmpty(t, res.Note)
	require.Len(t, res.Secrets, 2)

	byName := map[string]GCPSecretHealthInfo{}
	for _, s := range res.Secrets {
		byName[s.Name] = s
	}
	auto := byName["projects/test-project/secrets/auto-secret"]
	assert.Equal(t, "automatic", auto.ReplicationType)
	assert.Equal(t, 3, auto.VersionCount)
	assert.Equal(t, created.Format(time.RFC3339), auto.CreateTime)

	um := byName["projects/test-project/secrets/usermanaged-secret"]
	assert.Equal(t, "user-managed", um.ReplicationType)
	assert.Equal(t, 1, um.VersionCount)

	assert.True(t, f.closed, "the secret manager client must be closed")
}

func TestDiscoverAndFetchGCP_SecretManager_ClientError(t *testing.T) {
	t.Parallel()
	orig := newGCPSecretClient
	t.Cleanup(func() { newGCPSecretClient = orig })
	newGCPSecretClient = func(_ context.Context, _ ...option.ClientOption) (gcpSecretManagerAPI, error) {
		return nil, errors.New("PermissionDenied")
	}
	_, err := DiscoverAndFetchGCP(context.Background(), "test-project", "secretmanager", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PermissionDenied")
}
