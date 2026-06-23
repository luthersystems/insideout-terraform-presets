package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// =============================================================================
// DiscoverAndFetch orchestration — parity port of reliable's
// getServiceMetrics tests (internal/agentapi/aws_metrics_test.go).
// =============================================================================

// TestDiscoverAndFetch_UnsupportedService pins the typed sentinel for a
// service with no metric catalog entry (reliable#1789). ui-core detects
// this via AsUnsupportedServiceMetricsError and emits a clean empty-state
// envelope instead of an `aws_operation_failed` banner.
func TestDiscoverAndFetch_UnsupportedService(t *testing.T) {
	t.Parallel()
	got, err := DiscoverAndFetch(context.Background(), aws.Config{}, "totally-unknown-service", "")
	require.Error(t, err)
	assert.Nil(t, got)

	svc, ok := AsUnsupportedServiceMetricsError(err)
	assert.True(t, ok, "must be detectable as the unsupported-service sentinel")
	assert.Equal(t, "totally-unknown-service", svc)

	var sentinel *UnsupportedServiceMetricsError
	require.True(t, errors.As(err, &sentinel))
	assert.Equal(t, "no metric definitions for service: totally-unknown-service", sentinel.Error())
}

// TestAsUnsupportedServiceMetricsError_StringFallback covers the
// wire-boundary case reliable guards: the dispatcher wraps with `%v`
// (breaking the chain), so detection falls back to matching the stable
// message suffix. Ported from reliable's coverage of the same helper.
func TestAsUnsupportedServiceMetricsError_StringFallback(t *testing.T) {
	t.Parallel()
	// %v-wrapped (chain broken) — must still be detected via the suffix.
	wrapped := errors.New("aws_operation_failed: no metric definitions for service: logs")
	svc, ok := AsUnsupportedServiceMetricsError(wrapped)
	assert.True(t, ok)
	assert.Equal(t, "logs", svc)

	// Unrelated error — must not match.
	_, ok = AsUnsupportedServiceMetricsError(errors.New("throttled"))
	assert.False(t, ok)

	// nil — must not match.
	_, ok = AsUnsupportedServiceMetricsError(nil)
	assert.False(t, ok)
}

// TestResourceDimensionFromFilter is the reliable#2035 dimension-parse
// table, ported verbatim from reliable's aws_metrics_test.go.
func TestResourceDimensionFromFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		filters   string
		dimension string
		want      string
	}{
		{"s3 bucket name resolved", `{"BucketName":"my-bucket"}`, "BucketName", "my-bucket"},
		{"trims whitespace", `{"BucketName":"  padded  "}`, "BucketName", "padded"},
		{"sibling keys ignored", `{"BucketName":"b","hours":6}`, "BucketName", "b"},
		{"project-only filter does not match", `{"project":"io-sess"}`, "BucketName", ""},
		{"empty filter", "", "BucketName", ""},
		{"empty dimension never matches", `{"BucketName":"b"}`, "", ""},
		{"key present but empty", `{"BucketName":""}`, "BucketName", ""},
		{"key present but non-string", `{"BucketName":123}`, "BucketName", ""},
		{"non-object filter", `["BucketName"]`, "BucketName", ""},
		{"malformed json", `{not json`, "BucketName", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, resourceDimensionFromFilter(tc.filters, tc.dimension))
		})
	}
}

// TestDiscoverAndFetch_ResourceScopedSkipsDiscovery is the reliable#2035
// regression guard, ported from reliable's
// TestGetServiceMetrics_ResourceScopedSkipsDiscovery. An IMPORTED S3
// bucket lives in the user's pre-existing account and is NOT tagged
// Project=io-<session>, so project-tag-scoped account-wide discovery
// returns zero resources. When the get-metrics filter already names the
// resolved CloudWatch dimension value ({"BucketName":"<bucket>"}),
// DiscoverAndFetch MUST query that dimension directly and MUST NOT run
// account-wide discovery.
//
// MUST NOT run in parallel: it swaps the package-level seam vars.
func TestDiscoverAndFetch_ResourceScopedSkipsDiscovery(t *testing.T) {
	origDiscovery := runMetricsDiscovery
	origFetch := fetchServiceMetricsForResources
	defer func() {
		runMetricsDiscovery = origDiscovery
		fetchServiceMetricsForResources = origFetch
	}()

	t.Run("resolved dimension queries directly without discovery", func(t *testing.T) {
		discoveryCalled := false
		runMetricsDiscovery = func(_ context.Context, _ aws.Config, _, _ string) ([]string, error) {
			discoveryCalled = true
			// An untagged imported bucket is invisible to project-scoped
			// discovery — return nothing, as production would.
			return nil, nil
		}
		var fetched []string
		fetchServiceMetricsForResources = func(_ context.Context, _ aws.Config, service string, _ *observability.AWSObs, dims []string, _ string) (any, error) {
			fetched = dims
			res := make([]ResourceMetrics, 0, len(dims))
			for _, d := range dims {
				res = append(res, ResourceMetrics{ResourceID: d})
			}
			return MetricsResult{Service: service, Resources: res}, nil
		}

		got, err := DiscoverAndFetch(context.Background(), aws.Config{}, "s3", `{"BucketName":"imported-bucket"}`)
		require.NoError(t, err)
		assert.False(t, discoveryCalled,
			"resource-scoped get-metrics must NOT run account-wide discovery (#2035)")
		assert.Equal(t, []string{"imported-bucket"}, fetched,
			"the resolved BucketName must be queried directly")

		mr, ok := got.(MetricsResult)
		require.True(t, ok)
		require.Len(t, mr.Resources, 1,
			"the named imported bucket must produce a series even though discovery would drop it")
		assert.Equal(t, "imported-bucket", mr.Resources[0].ResourceID)
	})

	t.Run("project-scoped filter still routes through discovery", func(t *testing.T) {
		discoveryCalled := false
		runMetricsDiscovery = func(_ context.Context, _ aws.Config, _, project string) ([]string, error) {
			discoveryCalled = true
			assert.Equal(t, "io-managed", project,
				"managed path must thread the project through to discovery")
			return []string{"managed-bucket"}, nil
		}
		var fetched []string
		fetchServiceMetricsForResources = func(_ context.Context, _ aws.Config, service string, _ *observability.AWSObs, dims []string, _ string) (any, error) {
			fetched = dims
			return MetricsResult{Service: service}, nil
		}

		_, err := DiscoverAndFetch(context.Background(), aws.Config{}, "s3", `{"project":"io-managed"}`)
		require.NoError(t, err)
		assert.True(t, discoveryCalled,
			"the managed/designed path (no resource-dimension key) must keep using discovery")
		assert.Equal(t, []string{"managed-bucket"}, fetched,
			"discovered resources must flow to the fetch tail unchanged")
	})
}

// TestDiscoverAndFetch_EC2_AutoDiscoveryPath drives DiscoverAndFetch end
// to end through the auto-discovery branch (no resource-dimension key in
// the filter): discovery resolves an instance ID, then the REAL fetch
// tail (fetchServiceMetricsForResourcesImpl → Fetch) runs against a
// mocked CloudWatch client, producing reliable's MetricsResult wire
// shape. This proves the discover→fetch orchestration for the canonical
// service.
//
// MUST NOT run in parallel: swaps package-level seam vars.
func TestDiscoverAndFetch_EC2_AutoDiscoveryPath(t *testing.T) {
	origDiscovery := runMetricsDiscovery
	origClients := clientsFromConfigForFetch
	defer func() {
		runMetricsDiscovery = origDiscovery
		clientsFromConfigForFetch = origClients
	}()

	now := time.Now().UTC()

	// Discovery returns the running instance the account holds for the
	// project. We assert it received the project parsed from the filter.
	discoveryCalled := false
	runMetricsDiscovery = func(_ context.Context, _ aws.Config, service, project string) ([]string, error) {
		discoveryCalled = true
		assert.Equal(t, "ec2", service)
		assert.Equal(t, "io-projx", project, "project must be parsed from the filter and threaded to discovery")
		return []string{"i-abc123"}, nil
	}

	// The fetch tail builds a *Clients from cfg — swap the constructor so
	// it returns a mocked CloudWatch client instead of a real SDK client.
	cwMock := &fakeCloudWatch{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{
				{Label: aws.String("CPUUtilization"), Timestamps: []time.Time{now}, Values: []float64{42.5}},
			},
		},
	}
	clientsFromConfigForFetch = func(_ aws.Config) (*Clients, error) {
		return clientsWithCW(cwMock), nil
	}

	got, err := DiscoverAndFetch(context.Background(), aws.Config{}, "ec2", `{"project":"io-projx","hours":12,"period":600}`)
	require.NoError(t, err)
	assert.True(t, discoveryCalled, "auto-discovery branch must run when no resource-dimension key is present")

	mr, ok := got.(MetricsResult)
	require.True(t, ok, "CloudWatch path must return a MetricsResult value (reliable parity)")
	assert.Equal(t, "ec2", mr.Service)
	assert.Equal(t, 600, mr.Period)
	assert.Contains(t, mr.TimeRange, "12")
	require.Len(t, mr.Resources, 1)
	assert.Equal(t, "i-abc123", mr.Resources[0].ResourceID, "discovered instance must be fetched")
	require.NotEmpty(t, mr.Resources[0].Metrics)
	assert.Equal(t, "CPUUtilization", mr.Resources[0].Metrics[0].Name)
	require.Len(t, mr.Resources[0].Metrics[0].Datapoints, 1)
	assert.InDelta(t, 42.5, mr.Resources[0].Metrics[0].Datapoints[0].Average, 0.001)

	// The instance ID must reach CloudWatch under the EC2 dimension.
	require.NotNil(t, cwMock.lastInput)
	dims := cwMock.lastInput.MetricDataQueries[0].MetricStat.Metric.Dimensions
	require.NotEmpty(t, dims)
	assert.Equal(t, "InstanceId", aws.ToString(dims[0].Name))
	assert.Equal(t, "i-abc123", aws.ToString(dims[0].Value))
}

// TestDiscoverAndFetch_ResourceScoped_EC2_RealFetchTail exercises the
// #2035 fast path through the REAL fetch tail with a mocked CloudWatch
// client, asserting discovery is never consulted. Complements the
// seam-swapped fast-path test above by validating the actual fetch
// produces the wire shape.
//
// MUST NOT run in parallel: swaps package-level seam vars.
func TestDiscoverAndFetch_ResourceScoped_EC2_RealFetchTail(t *testing.T) {
	origDiscovery := runMetricsDiscovery
	origClients := clientsFromConfigForFetch
	defer func() {
		runMetricsDiscovery = origDiscovery
		clientsFromConfigForFetch = origClients
	}()

	discoveryCalled := false
	runMetricsDiscovery = func(_ context.Context, _ aws.Config, _, _ string) ([]string, error) {
		discoveryCalled = true
		return nil, nil
	}
	cwMock := &fakeCloudWatch{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{
				{Label: aws.String("CPUUtilization"), Timestamps: []time.Time{time.Now().UTC()}, Values: []float64{7}},
			},
		},
	}
	clientsFromConfigForFetch = func(_ aws.Config) (*Clients, error) { return clientsWithCW(cwMock), nil }

	got, err := DiscoverAndFetch(context.Background(), aws.Config{}, "ec2", `{"InstanceId":"i-imported"}`)
	require.NoError(t, err)
	assert.False(t, discoveryCalled, "resource-scoped fast path must skip discovery (#2035)")

	mr, ok := got.(MetricsResult)
	require.True(t, ok)
	require.Len(t, mr.Resources, 1)
	assert.Equal(t, "i-imported", mr.Resources[0].ResourceID)
	require.NotNil(t, cwMock.lastInput)
	assert.Equal(t, "i-imported", aws.ToString(cwMock.lastInput.MetricDataQueries[0].MetricStat.Metric.Dimensions[0].Value))
}

// =============================================================================
// KMS / Secrets Manager operational-health readers — parity port.
// =============================================================================

// --- KMS fake ---

type fakeKMS struct {
	listKeys     *kms.ListKeysOutput
	listKeysErr  error
	listAliases  *kms.ListAliasesOutput
	describeKeys map[string]*kms.DescribeKeyOutput
	tags         map[string]*kms.ListResourceTagsOutput
	rotation     map[string]*kms.GetKeyRotationStatusOutput
}

func (f *fakeKMS) ListKeys(_ context.Context, _ *kms.ListKeysInput, _ ...func(*kms.Options)) (*kms.ListKeysOutput, error) {
	if f.listKeysErr != nil {
		return nil, f.listKeysErr
	}
	if f.listKeys == nil {
		return &kms.ListKeysOutput{}, nil
	}
	return f.listKeys, nil
}

func (f *fakeKMS) ListAliases(_ context.Context, _ *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	if f.listAliases == nil {
		return &kms.ListAliasesOutput{}, nil
	}
	return f.listAliases, nil
}

func (f *fakeKMS) DescribeKey(_ context.Context, in *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	out, ok := f.describeKeys[aws.ToString(in.KeyId)]
	if !ok {
		return &kms.DescribeKeyOutput{}, nil
	}
	return out, nil
}

func (f *fakeKMS) ListResourceTags(_ context.Context, in *kms.ListResourceTagsInput, _ ...func(*kms.Options)) (*kms.ListResourceTagsOutput, error) {
	out, ok := f.tags[aws.ToString(in.KeyId)]
	if !ok {
		return &kms.ListResourceTagsOutput{}, nil
	}
	return out, nil
}

func (f *fakeKMS) GetKeyRotationStatus(_ context.Context, in *kms.GetKeyRotationStatusInput, _ ...func(*kms.Options)) (*kms.GetKeyRotationStatusOutput, error) {
	out, ok := f.rotation[aws.ToString(in.KeyId)]
	if !ok {
		return &kms.GetKeyRotationStatusOutput{}, nil
	}
	return out, nil
}

func withKMS(t *testing.T, f kmsHealthAPI) {
	t.Helper()
	orig := newKMSClient
	t.Cleanup(func() { newKMSClient = orig })
	newKMSClient = func(_ aws.Config) kmsHealthAPI { return f }
}

// TestDiscoverAndFetch_KMSHealth_DemoSession asserts the kms branch of
// DiscoverAndFetch returns a *KeyHealthResult and that with project==""
// every key (customer- AND aws-managed) is surfaced. Ported from
// reliable's getKMSKeyHealth behavior.
func TestDiscoverAndFetch_KMSHealth_DemoSession(t *testing.T) {
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	f := &fakeKMS{
		listKeys: &kms.ListKeysOutput{Keys: []kmstypes.KeyListEntry{
			{KeyId: aws.String("key-cust")},
			{KeyId: aws.String("key-aws")},
		}},
		listAliases: &kms.ListAliasesOutput{Aliases: []kmstypes.AliasListEntry{
			{AliasName: aws.String("alias/my-key"), TargetKeyId: aws.String("key-cust")},
		}},
		describeKeys: map[string]*kms.DescribeKeyOutput{
			"key-cust": {KeyMetadata: &kmstypes.KeyMetadata{
				KeyId: aws.String("key-cust"), KeyState: kmstypes.KeyStateEnabled,
				KeyManager: kmstypes.KeyManagerTypeCustomer, Description: aws.String("app key"),
				CreationDate: &created,
			}},
			"key-aws": {KeyMetadata: &kmstypes.KeyMetadata{
				KeyId: aws.String("key-aws"), KeyState: kmstypes.KeyStateEnabled,
				KeyManager: kmstypes.KeyManagerTypeAws,
			}},
		},
		rotation: map[string]*kms.GetKeyRotationStatusOutput{
			"key-cust": {KeyRotationEnabled: true},
		},
	}
	withKMS(t, f)

	got, err := DiscoverAndFetch(context.Background(), aws.Config{}, "kms", "")
	require.NoError(t, err)

	res, ok := got.(*KeyHealthResult)
	require.True(t, ok, "kms path must return *KeyHealthResult (reliable parity)")
	assert.Equal(t, "kms", res.Service)
	assert.NotEmpty(t, res.Note)
	require.Len(t, res.Keys, 2, "demo session (project=='') surfaces aws-managed keys too")

	byID := map[string]KeyHealthInfo{}
	for _, k := range res.Keys {
		byID[k.KeyID] = k
	}
	cust := byID["key-cust"]
	assert.Equal(t, "alias/my-key", cust.Alias)
	assert.Equal(t, "Enabled", cust.KeyState)
	assert.Equal(t, "CUSTOMER", cust.KeyManager)
	assert.True(t, cust.RotationEnabled, "customer key rotation status must be read")
	assert.Equal(t, "app key", cust.Description)
	assert.Equal(t, created.Format(time.RFC3339), cust.CreationDate)

	awsKey := byID["key-aws"]
	assert.Equal(t, "AWS", awsKey.KeyManager)
	assert.False(t, awsKey.RotationEnabled, "aws-managed key rotation is never queried")
}

// TestDiscoverAndFetch_KMSHealth_ProjectScopedFailClosed pins reliable's
// #1112 fail-closed gate: with a non-empty project, aws-managed keys are
// dropped and customer keys without the Project tag are dropped.
func TestDiscoverAndFetch_KMSHealth_ProjectScopedFailClosed(t *testing.T) {
	f := &fakeKMS{
		listKeys: &kms.ListKeysOutput{Keys: []kmstypes.KeyListEntry{
			{KeyId: aws.String("key-tagged")},
			{KeyId: aws.String("key-untagged")},
			{KeyId: aws.String("key-aws")},
		}},
		describeKeys: map[string]*kms.DescribeKeyOutput{
			"key-tagged":   {KeyMetadata: &kmstypes.KeyMetadata{KeyId: aws.String("key-tagged"), KeyManager: kmstypes.KeyManagerTypeCustomer, KeyState: kmstypes.KeyStateEnabled}},
			"key-untagged": {KeyMetadata: &kmstypes.KeyMetadata{KeyId: aws.String("key-untagged"), KeyManager: kmstypes.KeyManagerTypeCustomer, KeyState: kmstypes.KeyStateEnabled}},
			"key-aws":      {KeyMetadata: &kmstypes.KeyMetadata{KeyId: aws.String("key-aws"), KeyManager: kmstypes.KeyManagerTypeAws, KeyState: kmstypes.KeyStateEnabled}},
		},
		tags: map[string]*kms.ListResourceTagsOutput{
			"key-tagged": {Tags: []kmstypes.Tag{{TagKey: aws.String("Project"), TagValue: aws.String("io-projx")}}},
			// key-untagged: no Project tag.
			"key-untagged": {Tags: []kmstypes.Tag{{TagKey: aws.String("Name"), TagValue: aws.String("other")}}},
		},
	}
	withKMS(t, f)

	got, err := getKMSKeyHealth(context.Background(), aws.Config{}, "io-projx")
	require.NoError(t, err)
	require.Len(t, got.Keys, 1, "only the Project-tagged customer key survives the fail-closed gate")
	assert.Equal(t, "key-tagged", got.Keys[0].KeyID)
}

func TestDiscoverAndFetch_KMSHealth_ListKeysError(t *testing.T) {
	withKMS(t, &fakeKMS{listKeysErr: errors.New("AccessDenied")})
	_, err := DiscoverAndFetch(context.Background(), aws.Config{}, "kms", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

// --- Secrets Manager fake ---

type fakeSM struct {
	listSecrets    *secretsmanager.ListSecretsOutput
	listSecretsErr error
	versions       map[string]*secretsmanager.ListSecretVersionIdsOutput
}

func (f *fakeSM) ListSecrets(_ context.Context, in *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	if f.listSecretsErr != nil {
		return nil, f.listSecretsErr
	}
	if f.listSecrets == nil {
		return &secretsmanager.ListSecretsOutput{}, nil
	}
	return f.listSecrets, nil
}

func (f *fakeSM) ListSecretVersionIds(_ context.Context, in *secretsmanager.ListSecretVersionIdsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretVersionIdsOutput, error) {
	out, ok := f.versions[aws.ToString(in.SecretId)]
	if !ok {
		return &secretsmanager.ListSecretVersionIdsOutput{}, nil
	}
	return out, nil
}

func withSM(t *testing.T, f smHealthAPI) {
	t.Helper()
	orig := newSecretsManagerClient
	t.Cleanup(func() { newSecretsManagerClient = orig })
	newSecretsManagerClient = func(_ aws.Config) smHealthAPI { return f }
}

// TestDiscoverAndFetch_SecretHealth asserts the secretsmanager branch of
// DiscoverAndFetch returns a *SecretHealthResult with the reliable field
// shape, including the version-count second round trip.
func TestDiscoverAndFetch_SecretHealth(t *testing.T) {
	rotated := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	f := &fakeSM{
		listSecrets: &secretsmanager.ListSecretsOutput{SecretList: []smtypes.SecretListEntry{
			{
				Name:            aws.String("io-projx/db-password"),
				ARN:             aws.String("arn:aws:secretsmanager:eu-west-2:111:secret:io-projx/db-password-AbCdEf"),
				RotationEnabled: aws.Bool(true),
				LastRotatedDate: &rotated,
			},
		}},
		versions: map[string]*secretsmanager.ListSecretVersionIdsOutput{
			"arn:aws:secretsmanager:eu-west-2:111:secret:io-projx/db-password-AbCdEf": {
				Versions: []smtypes.SecretVersionsListEntry{{}, {}, {}},
			},
		},
	}
	withSM(t, f)

	got, err := DiscoverAndFetch(context.Background(), aws.Config{}, "secretsmanager", `{"project":"io-projx"}`)
	require.NoError(t, err)

	res, ok := got.(*SecretHealthResult)
	require.True(t, ok, "secretsmanager path must return *SecretHealthResult (reliable parity)")
	assert.Equal(t, "secretsmanager", res.Service)
	assert.NotEmpty(t, res.Note)
	require.Len(t, res.Secrets, 1)

	s := res.Secrets[0]
	assert.Equal(t, "io-projx/db-password", s.Name)
	assert.True(t, s.RotationEnabled)
	assert.Equal(t, rotated.Format(time.RFC3339), s.LastRotatedDate)
	assert.Equal(t, 3, s.VersionCount, "version count comes from the second ListSecretVersionIds call")
}

// TestBuildSecretsManagerListInput pins the project-tag-scoped list
// filter, ported from reliable.
func TestBuildSecretsManagerListInput(t *testing.T) {
	t.Parallel()
	t.Run("empty project has no filters", func(t *testing.T) {
		t.Parallel()
		input := buildSecretsManagerListInput("")
		require.NotNil(t, input)
		assert.Empty(t, input.Filters)
	})
	t.Run("project scopes by tag value", func(t *testing.T) {
		t.Parallel()
		input := buildSecretsManagerListInput("io-projx")
		require.Len(t, input.Filters, 1)
		assert.Equal(t, smtypes.FilterNameStringTypeTagValue, input.Filters[0].Key)
		assert.Equal(t, []string{"io-projx"}, input.Filters[0].Values)
	})
}

func TestDiscoverAndFetch_SecretHealth_ListError(t *testing.T) {
	withSM(t, &fakeSM{listSecretsErr: errors.New("AccessDenied")})
	_, err := DiscoverAndFetch(context.Background(), aws.Config{}, "secretsmanager", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}
