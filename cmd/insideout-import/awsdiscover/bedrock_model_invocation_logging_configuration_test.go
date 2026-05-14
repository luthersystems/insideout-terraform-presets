package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

type fakeBedrockMILCClient struct {
	mu       sync.Mutex
	calls    []string // region order across multiple-instance fakes via the per-region constructor
	out      *bedrock.GetModelInvocationLoggingConfigurationOutput
	err      error
	regionID string
}

func (f *fakeBedrockMILCClient) GetModelInvocationLoggingConfiguration(_ context.Context, _ *bedrock.GetModelInvocationLoggingConfigurationInput, _ ...func(*bedrock.Options)) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, f.regionID)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

// configuredLoggingConfig returns a non-nil LoggingConfig matching the
// shape the live API returns when the singleton is set: typically both
// CloudWatch and S3 destinations are populated.
func configuredLoggingConfig(logGroup, roleArn, bucket, prefix string) *bedrocktypes.LoggingConfig {
	out := &bedrocktypes.LoggingConfig{}
	if logGroup != "" || roleArn != "" {
		out.CloudWatchConfig = &bedrocktypes.CloudWatchConfig{
			LogGroupName: aws.String(logGroup),
			RoleArn:      aws.String(roleArn),
		}
	}
	if bucket != "" || prefix != "" {
		out.S3Config = &bedrocktypes.S3Config{
			BucketName: aws.String(bucket),
			KeyPrefix:  aws.String(prefix),
		}
	}
	return out
}

// TestBedrockMILCDiscover_ConfiguredEmitsOnePerRegion pins the happy path:
// each region whose GetModelInvocationLoggingConfiguration response carries
// a non-nil LoggingConfig emits exactly one ImportedResource keyed by region,
// and the NativeIDs surface the wired CloudWatch + S3 destinations.
func TestBedrockMILCDiscover_ConfiguredEmitsOnePerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeBedrockMILCClient{
		"us-east-1": {
			out: &bedrock.GetModelInvocationLoggingConfigurationOutput{
				LoggingConfig: configuredLoggingConfig("/aws/bedrock/invocations", "arn:aws:iam::123:role/bedrock-logs", "my-logs-bucket", "bedrock/"),
			},
			regionID: "us-east-1",
		},
		"eu-west-1": {
			out: &bedrock.GetModelInvocationLoggingConfigurationOutput{
				LoggingConfig: configuredLoggingConfig("/aws/bedrock/invocations-eu", "", "", ""),
			},
			regionID: "eu-west-1",
		},
	}
	d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(region string) bedrockModelInvocationLoggingConfigurationClient { return fakes[region] },
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per configured region)", len(got))
	}
	byRegion := map[string]int{}
	for _, ir := range got {
		byRegion[ir.Identity.Region]++
		if ir.Identity.ImportID != ir.Identity.Region {
			t.Errorf("ImportID=%q, want %q (TF import id is the region string)", ir.Identity.ImportID, ir.Identity.Region)
		}
		if ir.Identity.Type != bedrockModelInvocationLoggingConfigurationTFType {
			t.Errorf("Type=%q", ir.Identity.Type)
		}
		if ir.Identity.Tags == nil {
			t.Error("Tags is nil; must be non-nil empty per #255 JSON-shape contract")
		}
		if ir.Identity.NativeIDs["region"] != ir.Identity.Region {
			t.Errorf("NativeIDs[region]=%q, want %q", ir.Identity.NativeIDs["region"], ir.Identity.Region)
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if byRegion[region] != 1 {
			t.Errorf("region=%s: got %d imps, want 1", region, byRegion[region])
		}
	}

	// us-east-1 had S3+CW; verify both NativeIDs populated.
	for _, ir := range got {
		if ir.Identity.Region == "us-east-1" {
			if ir.Identity.NativeIDs["cloud_watch_log_group_name"] != "/aws/bedrock/invocations" {
				t.Errorf("cloud_watch_log_group_name=%q", ir.Identity.NativeIDs["cloud_watch_log_group_name"])
			}
			if ir.Identity.NativeIDs["s3_bucket_name"] != "my-logs-bucket" {
				t.Errorf("s3_bucket_name=%q", ir.Identity.NativeIDs["s3_bucket_name"])
			}
		}
	}
}

// TestBedrockMILCDiscover_NilLoggingConfigEmitsNothing pins the
// "unconfigured" empty-state contract: a 200 OK with LoggingConfig == nil
// must NOT emit a resource — the singleton genuinely doesn't exist in
// that region. Mirrors the SDK behavior observed against live accounts
// where no model-invocation logging has been turned on.
func TestBedrockMILCDiscover_NilLoggingConfigEmitsNothing(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockMILCClient{
		out:      &bedrock.GetModelInvocationLoggingConfigurationOutput{LoggingConfig: nil},
		regionID: "us-east-1",
	}
	d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(_ string) bedrockModelInvocationLoggingConfigurationClient { return fake },
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("len=%d, want 0 (LoggingConfig=nil means unconfigured)", len(got))
	}
}

// TestBedrockMILCDiscover_ResourceNotFoundEmitsNothing pins the typed
// not-found branch: bedrock SDK may return ResourceNotFoundException
// instead of a nil payload for the empty state in some
// region/version combos. Both code paths must collapse to "no resource."
func TestBedrockMILCDiscover_ResourceNotFoundEmitsNothing(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockMILCClient{
		err:      &bedrocktypes.ResourceNotFoundException{Message: aws.String("no config")},
		regionID: "us-east-1",
	}
	d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(_ string) bedrockModelInvocationLoggingConfigurationClient { return fake },
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("len=%d, want 0 (RNF is the unconfigured shape)", len(got))
	}
}

// TestBedrockMILCDiscover_OtherErrorWarnsAndContinues pins the fail-open
// posture: a non-RNF error against one region must not abort the whole
// multi-region scan. The error surfaces as ServiceWarn so the operator
// sees what went wrong without losing the rest of the discover output.
func TestBedrockMILCDiscover_OtherErrorWarnsAndContinues(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeBedrockMILCClient{
		"us-east-1": {err: errors.New("AccessDenied"), regionID: "us-east-1"},
		"eu-west-1": {
			out: &bedrock.GetModelInvocationLoggingConfigurationOutput{
				LoggingConfig: configuredLoggingConfig("/aws/bedrock/eu", "", "", ""),
			},
			regionID: "eu-west-1",
		},
	}
	d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(region string) bedrockModelInvocationLoggingConfigurationClient { return fakes[region] },
	}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Identity.Region != "eu-west-1" {
		t.Fatalf("got %d imps, want 1 from eu-west-1", len(got))
	}
	var warns []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "service_warn" {
			warns = append(warns, e)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("warns=%d, want 1", len(warns))
	}
	if warns[0].Region != "us-east-1" {
		t.Errorf("warn.region=%q, want us-east-1", warns[0].Region)
	}
	if warns[0].Service != bedrockModelInvocationLoggingConfigurationSlug {
		t.Errorf("warn.service=%q, want %s", warns[0].Service, bedrockModelInvocationLoggingConfigurationSlug)
	}
}

// TestBedrockMILCDiscover_EmitsServiceStartFinish_PerRegion pins the
// per-region progress contract: every region issued ServiceStart and
// ServiceFinish exactly once, and ServiceFinish.count reflects emit count.
func TestBedrockMILCDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeBedrockMILCClient{
		"us-east-1": {
			out: &bedrock.GetModelInvocationLoggingConfigurationOutput{
				LoggingConfig: configuredLoggingConfig("/aws/bedrock/east", "", "", ""),
			},
			regionID: "us-east-1",
		},
		"eu-west-1": {
			out:      &bedrock.GetModelInvocationLoggingConfigurationOutput{LoggingConfig: nil},
			regionID: "eu-west-1",
		},
	}
	d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(region string) bedrockModelInvocationLoggingConfigurationClient { return fakes[region] },
	}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	finishes := map[string]int{}
	finishCount := map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != bedrockModelInvocationLoggingConfigurationSlug {
				t.Errorf("service_start.service=%q, want %s", e.Service, bedrockModelInvocationLoggingConfigurationSlug)
			}
			starts[e.Region]++
		case "service_finish":
			finishes[e.Region]++
			finishCount[e.Region] = e.Count
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if starts[region] != 1 {
			t.Errorf("region=%s: service_start count=%d, want 1", region, starts[region])
		}
		if finishes[region] != 1 {
			t.Errorf("region=%s: service_finish count=%d, want 1", region, finishes[region])
		}
	}
	if finishCount["us-east-1"] != 1 {
		t.Errorf("us-east-1 service_finish.count=%d, want 1", finishCount["us-east-1"])
	}
	if finishCount["eu-west-1"] != 0 {
		t.Errorf("eu-west-1 service_finish.count=%d, want 0 (unconfigured)", finishCount["eu-west-1"])
	}
}

// TestBedrockMILCDiscover_TagsJSONShape pins the #255 contract:
// Identity.Tags must marshal to a JSON object literal (`{}`), not
// JSON `null`. This resource is untaggable in AWS provider 6.x so the
// map is always empty — but it must be non-nil.
func TestBedrockMILCDiscover_TagsJSONShape(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockMILCClient{
		out: &bedrock.GetModelInvocationLoggingConfigurationOutput{
			LoggingConfig: configuredLoggingConfig("/aws/bedrock/east", "", "", ""),
		},
		regionID: "us-east-1",
	}
	d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(_ string) bedrockModelInvocationLoggingConfigurationClient { return fake },
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.Tags == nil {
		t.Fatal("Tags is nil; #255 requires non-nil empty map")
	}
	b, err := json.Marshal(got[0].Identity.Tags)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}" {
		t.Errorf("json.Marshal(Tags)=%q, want %q", string(b), "{}")
	}
}

func TestBedrockMILCDiscoverByID_AcceptsRegion(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockMILCClient{
		out: &bedrock.GetModelInvocationLoggingConfigurationOutput{
			LoggingConfig: configuredLoggingConfig("/aws/bedrock/east", "arn:aws:iam::123:role/r", "b", "p/"),
		},
	}
	d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(_ string) bedrockModelInvocationLoggingConfigurationClient { return fake },
	}
	got, err := d.DiscoverByID(context.Background(), "us-east-1", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "us-east-1" {
		t.Errorf("ImportID=%q, want us-east-1", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["region"] != "us-east-1" {
		t.Errorf("NativeIDs[region]=%q", got.Identity.NativeIDs["region"])
	}
	if got.Identity.NativeIDs["s3_bucket_name"] != "b" {
		t.Errorf("NativeIDs[s3_bucket_name]=%q", got.Identity.NativeIDs["s3_bucket_name"])
	}
}

func TestBedrockMILCDiscoverByID_NotFoundShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fake *fakeBedrockMILCClient
	}{
		{"rnf_error", &fakeBedrockMILCClient{err: &bedrocktypes.ResourceNotFoundException{}}},
		{"nil_payload", &fakeBedrockMILCClient{out: &bedrock.GetModelInvocationLoggingConfigurationOutput{LoggingConfig: nil}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
				new: func(_ string) bedrockModelInvocationLoggingConfigurationClient { return tc.fake },
			}
			_, err := d.DiscoverByID(context.Background(), "us-east-1", "us-east-1", "123")
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("err=%v, want ErrNotFound", err)
			}
		})
	}
}

func TestBedrockMILCDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(_ string) bedrockModelInvocationLoggingConfigurationClient { return &fakeBedrockMILCClient{} },
	}
	cases := []string{
		"",
		"us-east-1/extra",
		"us east 1",
		"arn:aws:bedrock:us-east-1:123:foo",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
