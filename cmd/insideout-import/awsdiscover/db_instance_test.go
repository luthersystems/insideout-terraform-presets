package awsdiscover

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// errDBInstanceSeed is the package-level sentinel for db_instance error
// propagation — see vpc_test.go / dynamodb_test.go for the contract.
var errDBInstanceSeed = errors.New("AccessDenied")

type fakeDBInstanceClient struct {
	pages []rds.DescribeDBInstancesOutput
	err   error

	mu    sync.Mutex
	calls []rds.DescribeDBInstancesInput
}

func (f *fakeDBInstanceClient) DescribeDBInstances(_ context.Context, in *rds.DescribeDBInstancesInput, _ ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, *in)
	idx := len(f.calls) - 1
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if idx >= len(f.pages) {
		return &rds.DescribeDBInstancesOutput{}, nil
	}
	out := f.pages[idx]
	return &out, nil
}

func rdsTag(k, v string) rdstypes.Tag {
	return rdstypes.Tag{Key: aws.String(k), Value: aws.String(v)}
}

func dbInstanceWithStatus(id, arn, engine, status, endpoint, subnetGroup string, tags ...rdstypes.Tag) rdstypes.DBInstance {
	db := rdstypes.DBInstance{
		DBInstanceIdentifier: aws.String(id),
		DBInstanceArn:        aws.String(arn),
		Engine:               aws.String(engine),
		DBInstanceStatus:     aws.String(status),
		TagList:              tags,
	}
	if endpoint != "" {
		db.Endpoint = &rdstypes.Endpoint{Address: aws.String(endpoint)}
	}
	if subnetGroup != "" {
		db.DBSubnetGroup = &rdstypes.DBSubnetGroup{DBSubnetGroupName: aws.String(subnetGroup)}
	}
	return db
}

func TestDBInstanceDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{
			pages: []rds.DescribeDBInstancesOutput{
				{DBInstances: []rdstypes.DBInstance{
					dbInstanceWithStatus("io-foo-rds0", "arn:aws:rds:us-east-1:123:db:io-foo-rds0", "postgres", "available", "io-foo-rds0.abc.us-east-1.rds.amazonaws.com", "io-foo-rds0-subnets", rdsTag("Project", "io-foo")),
					dbInstanceWithStatus("io-foo-rds1", "arn:aws:rds:us-east-1:123:db:io-foo-rds1", "postgres", "available", "", "", rdsTag("Project", "io-foo")),
				}},
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for _, ir := range got {
		if ir.Identity.Type != dbInstanceTFType {
			t.Errorf("Type=%q, want %q", ir.Identity.Type, dbInstanceTFType)
		}
		if ir.Identity.NativeIDs["arn"] == "" {
			t.Error("NativeIDs[arn] empty")
		}
		if ir.Identity.NativeIDs["engine"] != "postgres" {
			t.Errorf("NativeIDs[engine]=%q, want postgres", ir.Identity.NativeIDs["engine"])
		}
	}
	// Sorted by DBInstanceIdentifier.
	if got[0].Identity.ImportID != "io-foo-rds0" {
		t.Errorf("first ImportID=%q, want io-foo-rds0 (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NameHint != "io-foo-rds0" {
		t.Errorf("first NameHint=%q", got[0].Identity.NameHint)
	}
	// Endpoint + subnet group propagate when present.
	if got[0].Identity.NativeIDs["endpoint_address"] != "io-foo-rds0.abc.us-east-1.rds.amazonaws.com" {
		t.Errorf("endpoint_address=%q", got[0].Identity.NativeIDs["endpoint_address"])
	}
	if got[0].Identity.NativeIDs["db_subnet_group_name"] != "io-foo-rds0-subnets" {
		t.Errorf("db_subnet_group_name=%q", got[0].Identity.NativeIDs["db_subnet_group_name"])
	}
	// Absent endpoint + subnet group do NOT add empty keys.
	if _, ok := got[1].Identity.NativeIDs["endpoint_address"]; ok {
		t.Errorf("second endpoint_address should be unset when Endpoint is nil")
	}
	if _, ok := got[1].Identity.NativeIDs["db_subnet_group_name"]; ok {
		t.Errorf("second db_subnet_group_name should be unset when DBSubnetGroup is nil")
	}
}

func TestDBInstanceDiscover_PaginatesUntilNoMarker(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{
			pages: []rds.DescribeDBInstancesOutput{
				{DBInstances: []rdstypes.DBInstance{dbInstanceWithStatus("io-foo-rds0", "arn1", "postgres", "available", "", "")}, Marker: aws.String("m1")},
				{DBInstances: []rdstypes.DBInstance{dbInstanceWithStatus("io-foo-rds1", "arn2", "postgres", "available", "", "")}, Marker: aws.String("m2")},
				{DBInstances: []rdstypes.DBInstance{dbInstanceWithStatus("io-foo-rds2", "arn3", "postgres", "available", "", "")}}, // terminal
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
}

func TestDBInstanceDiscover_FiltersByProjectPrefix(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{
			pages: []rds.DescribeDBInstancesOutput{
				{DBInstances: []rdstypes.DBInstance{
					dbInstanceWithStatus("io-foo-rds0", "arn1", "postgres", "available", "", ""),
					dbInstanceWithStatus("other-rds", "arn2", "postgres", "available", "", ""),
					dbInstanceWithStatus("io-foo-rds1", "arn3", "postgres", "available", "", ""),
				}},
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (prefix filter)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.ImportID == "other-rds" {
			t.Errorf("non-prefix-matching instance leaked: %s", ir.Identity.ImportID)
		}
	}
}

func TestDBInstanceDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{err: errDBInstanceSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errDBInstanceSeed) {
		t.Errorf("err=%v, want errors.Is(err, errDBInstanceSeed)", err)
	}
}

func TestDBInstanceDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{
			pages: []rds.DescribeDBInstancesOutput{
				{DBInstances: []rdstypes.DBInstance{
					dbInstanceWithStatus("io-foo-rds0", "arn:aws:rds:us-east-1:123:db:io-foo-rds0", "postgres", "available", "ep.example.com", "io-foo-rds0-subnets"),
				}},
			},
		}
	}}
	got, err := d.DiscoverByID(context.Background(), "io-foo-rds0", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != dbInstanceTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "io-foo-rds0" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["arn"] != "arn:aws:rds:us-east-1:123:db:io-foo-rds0" {
		t.Errorf("arn=%q", got.Identity.NativeIDs["arn"])
	}
	if got.Identity.NativeIDs["endpoint_address"] != "ep.example.com" {
		t.Errorf("endpoint_address=%q", got.Identity.NativeIDs["endpoint_address"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestDBInstanceDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{err: &rdstypes.DBInstanceNotFoundFault{Message: aws.String("not found")}}
	}}
	_, err := d.DiscoverByID(context.Background(), "io-foo-missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestDBInstanceDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeDBInstanceClient{
		"us-east-1": {pages: []rds.DescribeDBInstancesOutput{
			{DBInstances: []rdstypes.DBInstance{dbInstanceWithStatus("io-foo-east", "arn-east", "postgres", "available", "", "")}},
		}},
		"eu-west-1": {pages: []rds.DescribeDBInstancesOutput{
			{DBInstances: []rdstypes.DBInstance{dbInstanceWithStatus("io-foo-west", "arn-west", "postgres", "available", "", "")}},
		}},
	}
	var seenRegions []string
	d := &dbInstanceDiscoverer{new: func(region string) dbInstanceClient {
		seenRegions = append(seenRegions, region)
		return fakes[region]
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenRegions) != 2 {
		t.Errorf("region closure invocations=%v, want 2", seenRegions)
	}
	if len(fakes["us-east-1"].calls) == 0 || len(fakes["eu-west-1"].calls) == 0 {
		t.Error("expected one DescribeDBInstances call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestDBInstanceDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{
			pages: []rds.DescribeDBInstancesOutput{
				{DBInstances: []rdstypes.DBInstance{
					dbInstanceWithStatus("io-foo-prod", "arn1", "postgres", "available", "", "", rdsTag("env", "prod")),
					dbInstanceWithStatus("io-foo-stag", "arn2", "postgres", "available", "", "", rdsTag("env", "staging")),
				}},
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:      "io-foo",
		Regions:      []string{"us-east-1"},
		AccountID:    "123",
		TagSelectors: []TagSelector{{Key: "env", Value: "prod"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (env=prod only)", len(got))
	}
	if got[0].Identity.ImportID != "io-foo-prod" {
		t.Errorf("ImportID=%q, want io-foo-prod", got[0].Identity.ImportID)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

func TestDBInstanceDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeDBInstanceClient{
		"us-east-1": {pages: []rds.DescribeDBInstancesOutput{
			{DBInstances: []rdstypes.DBInstance{dbInstanceWithStatus("io-foo-east", "arn-east", "postgres", "available", "", "")}},
		}},
		"eu-west-1": {pages: []rds.DescribeDBInstancesOutput{
			{DBInstances: []rdstypes.DBInstance{dbInstanceWithStatus("io-foo-west", "arn-west", "postgres", "available", "", "")}},
		}},
	}
	d := &dbInstanceDiscoverer{new: func(region string) dbInstanceClient { return fakes[region] }}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	finishes := map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != dbInstanceSlug {
				t.Errorf("service_start.service=%q, want %s", e.Service, dbInstanceSlug)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != dbInstanceSlug {
				t.Errorf("service_finish.service=%q, want %s", e.Service, dbInstanceSlug)
			}
			finishes[e.Region]++
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
}

func TestDBInstanceDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{
			pages: []rds.DescribeDBInstancesOutput{
				{DBInstances: []rdstypes.DBInstance{
					dbInstanceWithStatus("io-foo-a", "arn-a", "postgres", "available", "", ""),
					dbInstanceWithStatus("io-foo-b", "arn-b", "postgres", "available", "", ""),
				}},
			},
		}
	}}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	var items []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "item_found" {
			items = append(items, e)
		}
	}
	if len(items) != len(got) {
		t.Errorf("item_found count=%d, want %d (one per emitted resource)", len(items), len(got))
	}
	for _, it := range items {
		if it.Service != dbInstanceSlug {
			t.Errorf("item.service=%q, want %s", it.Service, dbInstanceSlug)
		}
		if it.TFType != dbInstanceTFType {
			t.Errorf("item.tf_type=%q, want %s", it.TFType, dbInstanceTFType)
		}
	}
}

func TestDBInstanceDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient { return &fakeDBInstanceClient{} }}
	cases := []string{
		"",
		"   ",
		"name with spaces",
		"name/slash",
		"arn:aws:rds:us-east-1:123:db:something", // arn-shaped: contains :
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// TestDBInstanceDiscover_SkipsDeletedAndDeletingState pins the
// tombstone skip-list — instances in DBInstanceStatus="deleting" or
// "deleted" are dropped before tag-fetch fan-out. RDS keeps these
// rows visible for ~1 hour after deletion but terraform import
// rejects them; emitting them would produce a manifest entry the
// operator cannot resolve.
func TestDBInstanceDiscover_SkipsDeletedAndDeletingState(t *testing.T) {
	t.Parallel()
	d := &dbInstanceDiscoverer{new: func(_ string) dbInstanceClient {
		return &fakeDBInstanceClient{
			pages: []rds.DescribeDBInstancesOutput{
				{DBInstances: []rdstypes.DBInstance{
					dbInstanceWithStatus("io-foo-live", "arn-live", "postgres", "available", "", ""),
					dbInstanceWithStatus("io-foo-going", "arn-going", "postgres", "deleting", "", ""),
					dbInstanceWithStatus("io-foo-gone", "arn-gone", "postgres", "deleted", "", ""),
				}},
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only io-foo-live should pass)", len(got))
	}
	if got[0].Identity.ImportID != "io-foo-live" {
		t.Errorf("ImportID=%q, want io-foo-live", got[0].Identity.ImportID)
	}
}
