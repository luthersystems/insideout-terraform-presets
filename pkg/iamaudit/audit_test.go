package iamaudit

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cttypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures in testdata/ are CloudTrail records in the documented record
// format (eventVersion 1.09), modeled on a terraform deploy by the durable
// <short_project_id>-luther-terraform role and the account-setup
// …-account-admin-0 role, plus records the audit must skip: another role, an
// IAM user, a service event and an event outside the window.

var (
	windowStart = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	windowEnd   = time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
)

func terraformRoles() RoleFilter {
	return RoleFilter{Patterns: []*regexp.Regexp{regexp.MustCompile(`:role/[^/]+-luther-terraform$`)}}
}

func loadPolicy(t *testing.T, name string) *Policy {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	p, err := ParsePolicy(name, data)
	require.NoError(t, err)
	return p
}

func collectFixture(t *testing.T, roles RoleFilter) Result {
	t.Helper()
	c := &Collector{Roles: roles, Start: windowStart, End: windowEnd}
	require.NoError(t, (&FileSource{Paths: []string{"testdata/trail-records.json"}}).Feed(context.Background(), c))
	return c.Result()
}

func actions(us []Usage) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Action
	}
	return out
}

func usageOf(t *testing.T, res Result, action string) Usage {
	t.Helper()
	for _, u := range res.Used {
		if u.Action == action {
			return u
		}
	}
	t.Fatalf("action %s not in %v", action, actions(res.Used))
	return Usage{}
}

func TestCollectTerraformRole(t *testing.T) {
	res := collectFixture(t, terraformRoles())

	assert.Equal(t, 25, res.EventsScanned)
	assert.Equal(t, 19, res.EventsMatched)
	assert.Equal(t, []string{"arn:aws:iam::111122223333:role/a1b2c3d4-luther-terraform"}, res.Roles)
	assert.Equal(t, []string{"us-east-1", "us-west-2"}, res.Regions)
	assert.Equal(t, []string{
		"apigateway:PATCH",
		"apigateway:POST",
		"cloudfront:CreateDistribution",
		"cloudwatch:PutMetricAlarm",
		"ec2:CreateNatGateway",
		"ec2:CreateVpc",
		"ec2:DescribeVpcs",
		"ec2:RunInstances",
		"iam:CreateRole",
		"kms:Decrypt",
		"lambda:CreateFunction",
		"lambda:GetFunction",
		"organizations:DescribeOrganization",
		"s3:PutBucketPublicAccessBlock",
		"s3:PutEncryptionConfiguration",
		"ssm:GetParameter",
		"sts:GetCallerIdentity",
		"tag:GetResources",
	}, actions(res.Used))

	run := usageOf(t, res, "ec2:RunInstances")
	assert.Equal(t, 2, run.Calls)
	assert.Equal(t, time.Date(2026, 11, 10, 10, 3, 0, 0, time.UTC), run.FirstSeen)
	assert.Equal(t, time.Date(2026, 11, 10, 10, 3, 30, 0, time.UTC), run.LastSeen)
	assert.Empty(t, run.EventNames, "event name equals the action")

	enc := usageOf(t, res, "s3:PutEncryptionConfiguration")
	assert.Equal(t, []string{"s3:PutBucketEncryption"}, enc.EventNames)

	assert.Equal(t, 1, usageOf(t, res, "kms:Decrypt").ViaService)
	assert.True(t, usageOf(t, res, "apigateway:POST").Approximate)
	assert.Equal(t, map[string]int{"Client.UnauthorizedOperation": 1}, usageOf(t, res, "ec2:CreateNatGateway").ErrorCodes)
}

func TestCollectExactARNs(t *testing.T) {
	res := collectFixture(t, RoleFilter{ARNs: []string{"arn:aws:iam::111122223333:role/a1b2c3d4-or-test-luther-account-admin-0"}})
	assert.Equal(t, []string{"kms:CreateKey", "s3:CreateBucket"}, actions(res.Used))
}

func TestCollectEmptyFilterSelectsNothing(t *testing.T) {
	res := collectFixture(t, RoleFilter{})
	assert.Equal(t, 0, res.EventsMatched)
	assert.Empty(t, res.Used)
}

func TestDiffAgainstPlaceholderPolicy(t *testing.T) {
	res := collectFixture(t, terraformRoles())
	rep := Diff(res, DiffOptions{
		Source:   "file",
		Start:    windowStart,
		End:      windowEnd,
		Policies: []*Policy{loadPolicy(t, "insideout-write.json")},
		Boundary: loadPolicy(t, "insideout-deploy-boundary.json"),
	})

	assert.False(t, rep.OK())
	assert.Equal(t, []Gap{
		{Action: "organizations:DescribeOrganization", Calls: 1, Reason: "not allowed by the policy", AlreadyRefused: true},
		{Action: "ssm:GetParameter", Calls: 1, Reason: "not allowed by the policy"},
	}, rep.UsedNotAllowed)
	assert.Equal(t, []string{"ec2:CreateNatGateway", "organizations:DescribeOrganization"}, rep.Denied)
	// ec2:RunInstances and lambda:CreateFunction pass a role, and the
	// placeholder allows iam:PassRole only under a Condition.
	assert.Equal(t, []string{"iam:PassRole"}, rep.UsedScoped)
	assert.Equal(t, []string{"organizations", "ssm", "tag"}, rep.ServicesBeyondComposerSeed)

	never := map[string]bool{}
	for _, p := range rep.AllowedNeverUsed {
		never[p.Pattern] = true
	}
	assert.True(t, never["acm:*"])
	assert.True(t, never["sts:AssumeRole"])
	assert.True(t, never["tag:TagResources"])
	assert.False(t, never["ec2:*"], "ec2 was used")
	assert.False(t, never["tag:GetResources"], "used")
	assert.False(t, never["iam:PassRole"], "unobservable, not a drop candidate")

	unobs := map[string]string{}
	for _, p := range rep.Unobservable {
		unobs[p.Pattern] = p.Statement
	}
	assert.Equal(t, map[string]string{
		"iam:PassRole":   "insideout-write.json#IAMPassRoleToProvisioningServicesOnly",
		"sts:TagSession": "insideout-write.json#STSAndCallerIdentity",
	}, unobs)
}

func TestDiffBoundaryDenies(t *testing.T) {
	res := collectFixture(t, terraformRoles())
	adminEquivalent, err := ParsePolicy("phase0", []byte(`{"Statement":[{"Sid":"AdminEquivalentPhase0","Effect":"Allow","Action":"*","Resource":"*"}]}`))
	require.NoError(t, err)
	rep := Diff(res, DiffOptions{Policies: []*Policy{adminEquivalent}, Boundary: loadPolicy(t, "insideout-deploy-boundary.json")})
	assert.Equal(t, []Gap{{
		Action:         "organizations:DescribeOrganization",
		Calls:          1,
		Reason:         "denied by the boundary: insideout-deploy-boundary.json#DenyOrgAccountAndBillingControlPlane",
		AlreadyRefused: true,
	}}, rep.UsedNotAllowed)
	// The boundary refuses it today too, so narrowing to the boundary
	// changes nothing: the gate passes.
	assert.True(t, rep.OK())
	assert.Empty(t, rep.AllowedNeverUsed, `"*" was used`)
}

// A gap with a call that succeeded fails the gate even when other calls of
// the same action were refused.
func TestGapPartlyRefused(t *testing.T) {
	g := newGap(Usage{Action: "ssm:GetParameter", Calls: 3, ErrorCodes: map[string]int{"AccessDeniedException": 2}}, "x")
	assert.False(t, g.AlreadyRefused)
	g = newGap(Usage{Action: "ssm:GetParameter", Calls: 2, ErrorCodes: map[string]int{"AccessDeniedException": 1, "Client.UnauthorizedOperation": 1}}, "x")
	assert.True(t, g.AlreadyRefused)
	g = newGap(Usage{Action: "ssm:GetParameter", Calls: 1, ErrorCodes: map[string]int{"ParameterNotFound": 1}}, "x")
	assert.False(t, g.AlreadyRefused, "a non-auth error is a call the role was allowed to make")
}

func TestDiffScopedAllow(t *testing.T) {
	c := &Collector{Roles: RoleFilter{ARNs: []string{"arn:aws:iam::1:role/r"}}}
	c.Add(roleEvent("arn:aws:iam::1:role/r", "s3.amazonaws.com", "PutObject"))
	p, err := ParsePolicy("p", []byte(`{"Statement":{"Effect":"Allow","Action":"s3:*","Resource":"arn:aws:s3:::state/*"}}`))
	require.NoError(t, err)
	rep := Diff(c.Result(), DiffOptions{Policies: []*Policy{p}})
	assert.True(t, rep.OK())
	assert.Equal(t, []string{"s3:PutObject"}, rep.UsedScoped)
}

// An empty list is [] in the JSON report, not null: an empty
// used_not_allowed is what the Phase 2 gate reads.
func TestReportJSONEmptyLists(t *testing.T) {
	rep := Diff(Result{}, DiffOptions{})
	b, err := json.Marshal(rep)
	require.NoError(t, err)
	for _, field := range []string{"used", "roles", "regions", "used_not_allowed", "used_scoped", "denied", "allowed_never_used", "unobservable", "services_beyond_composer_seed"} {
		assert.Contains(t, string(b), `"`+field+`":[]`, field)
	}
}

func TestWriteText(t *testing.T) {
	res := collectFixture(t, terraformRoles())
	rep := Diff(res, DiffOptions{Source: "file", Start: windowStart, End: windowEnd, Policies: []*Policy{loadPolicy(t, "insideout-write.json")}, Notes: []string{"a note"}})
	var buf bytes.Buffer
	require.NoError(t, rep.WriteText(&buf))
	out := buf.String()
	for _, want := range []string{
		"Window:   2026-11-10T00:00:00Z to 2026-11-11T00:00:00Z",
		"Used but not allowed (2) -- each must be already refused before Phase 2:",
		"already refused today",
		"Phase 2 gate: FAIL",
		"ssm:GetParameter",
		"Allowed but never used",
		"Not logged by CloudTrail",
		"apigateway:POST",
		"(approximate)",
		"  - a note",
	} {
		assert.Contains(t, out, want)
	}
	assert.Error(t, rep.WriteText(failWriter{}))
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func roleEvent(role, src, name string) Event {
	var ev Event
	ev.EventSource, ev.EventName, ev.EventType = src, name, "AwsApiCall"
	ev.EventTime = windowStart.Add(time.Hour)
	ev.UserIdentity.Type = "AssumedRole"
	ev.UserIdentity.SessionContext.SessionIssuer = SessionIssuer{Type: "Role", ARN: role}
	return ev
}

func TestFileSourceLookupOutputAndDirs(t *testing.T) {
	dir := t.TempDir()
	// A trail log file as `aws s3 sync` leaves it: gzipped, in a dated tree.
	sub := filepath.Join(dir, "AWSLogs", "111122223333", "CloudTrail", "us-west-2", "2026", "11", "10")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	raw, err := os.ReadFile("testdata/trail-records.json")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sub, "log.json.gz"), gz(t, raw), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "ignored.txt"), []byte("x"), 0o600))

	c := &Collector{Roles: terraformRoles(), Start: windowStart, End: windowEnd}
	require.NoError(t, (&FileSource{Paths: []string{dir, "testdata/lookup-events.json"}}).Feed(context.Background(), c))
	res := c.Result()
	// 25 records from the trail file and 3 from the lookup-events file.
	assert.Equal(t, 28, res.EventsScanned)
	assert.Equal(t, 2, usageOf(t, res, "ec2:CreateVpc").Calls)
}

func TestFileSourceRejectsUnknownJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"foo":1}`), 0o600))
	err := (&FileSource{Paths: []string{p}}).Feed(context.Background(), &Collector{})
	assert.ErrorContains(t, err, "neither a trail log file")

	require.NoError(t, os.WriteFile(p, []byte(`{"Records":[{"eventTime":"not a time"}]}`), 0o600))
	err = (&FileSource{Paths: []string{p}}).Feed(context.Background(), &Collector{})
	assert.ErrorContains(t, err, "decode record 0")
}

func gz(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(data)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// fakeS3 serves objects from a map and pages listings two keys at a time.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	listed  []string
	got     []string
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := aws.ToString(in.Prefix)
	f.listed = append(f.listed, prefix)
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	startAfter := aws.ToString(in.ContinuationToken)
	var page []string
	for _, k := range keys {
		if k > startAfter {
			page = append(page, k)
		}
	}
	out := &s3.ListObjectsV2Output{}
	if len(page) > 2 {
		page = page[:2]
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String(page[1])
	}
	for _, k := range page {
		out.Contents = append(out.Contents, s3types.Object{Key: aws.String(k)})
	}
	return out, nil
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := aws.ToString(in.Key)
	f.got = append(f.got, key)
	data, ok := f.objects[key]
	if !ok {
		return nil, errors.New("NoSuchKey")
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func TestS3Source(t *testing.T) {
	raw, err := os.ReadFile("testdata/trail-records.json")
	require.NoError(t, err)
	pre := "AWSLogs/111122223333/CloudTrail/"
	f := &fakeS3{objects: map[string][]byte{
		pre + "us-west-2/2026/11/10/a.json.gz": gz(t, raw),
		pre + "us-west-2/2026/11/10/b.json.gz": gz(t, []byte(`{"Records":[]}`)),
		pre + "us-west-2/2026/11/10/c.json.gz": gz(t, []byte(`{"Records":[]}`)),
		// The day after the window is listed: late deliveries land there.
		pre + "us-west-2/2026/11/12/late.json.gz": gz(t, []byte(`{"Records":[]}`)),
		// Outside the listed days and regions: never read.
		pre + "us-west-2/2026/11/01/old.json.gz":   gz(t, raw),
		pre + "eu-west-1/2026/11/10/other.json.gz": gz(t, raw),
		// A digest or other non-log object is skipped.
		pre + "us-west-2/2026/11/10/x.txt": []byte("x"),
	}}
	c := &Collector{Roles: terraformRoles(), Start: windowStart, End: windowEnd}
	src := &S3Source{Client: f, Bucket: "trail", Prefix: "AWSLogs/111122223333/", Regions: []string{"us-west-2"}, Workers: 2}
	require.NoError(t, src.Feed(context.Background(), c))

	assert.Equal(t, []string{
		pre + "us-west-2/2026/11/10/",
		pre + "us-west-2/2026/11/10/", // second page
		pre + "us-west-2/2026/11/11/",
		pre + "us-west-2/2026/11/12/",
	}, f.listed)
	sort.Strings(f.got)
	assert.Equal(t, []string{
		pre + "us-west-2/2026/11/10/a.json.gz",
		pre + "us-west-2/2026/11/10/b.json.gz",
		pre + "us-west-2/2026/11/10/c.json.gz",
		pre + "us-west-2/2026/11/12/late.json.gz",
	}, f.got)
	assert.Equal(t, 19, c.Result().EventsMatched)
}

func TestS3SourceGetError(t *testing.T) {
	f := &fakeS3{objects: map[string][]byte{"AWSLogs/1/CloudTrail/us-west-2/2026/11/10/bad.json.gz": []byte("not gzip")}}
	src := &S3Source{Client: f, Bucket: "trail", Prefix: "AWSLogs/1", Regions: []string{"us-west-2"}}
	err := src.Feed(context.Background(), &Collector{Start: windowStart, End: windowEnd})
	assert.ErrorContains(t, err, "gunzip")
}

// fakeLookup serves the events of one region in pages of two.
type fakeLookup struct {
	events []string
	inputs []*cloudtrail.LookupEventsInput
}

func (f *fakeLookup) LookupEvents(_ context.Context, in *cloudtrail.LookupEventsInput, _ ...func(*cloudtrail.Options)) (*cloudtrail.LookupEventsOutput, error) {
	f.inputs = append(f.inputs, in)
	start := 0
	if in.NextToken != nil {
		start = len(aws.ToString(in.NextToken))
	}
	end := min(start+2, len(f.events))
	out := &cloudtrail.LookupEventsOutput{}
	for _, e := range f.events[start:end] {
		out.Events = append(out.Events, cttypes.Event{CloudTrailEvent: aws.String(e)})
	}
	// A nil event body (seen when CloudTrail redacts one) is skipped.
	out.Events = append(out.Events, cttypes.Event{})
	if end < len(f.events) {
		out.NextToken = aws.String(strings.Repeat("x", end))
	}
	return out, nil
}

func TestLookupSource(t *testing.T) {
	raw, err := os.ReadFile("testdata/trail-records.json")
	require.NoError(t, err)
	var file struct{ Records []json.RawMessage }
	require.NoError(t, json.Unmarshal(raw, &file))
	var west, east []string
	for _, r := range file.Records {
		var ev Event
		require.NoError(t, json.Unmarshal(r, &ev))
		if ev.AWSRegion == "us-east-1" {
			east = append(east, string(r))
		} else {
			west = append(west, string(r))
		}
	}
	fw, fe := &fakeLookup{events: west}, &fakeLookup{events: east}
	c := &Collector{Roles: terraformRoles(), Start: windowStart, End: windowEnd}
	src := &LookupSource{Clients: map[string]LookupEventsAPI{"us-west-2": fw, "us-east-1": fe}, Interval: time.Millisecond}
	require.NoError(t, src.Feed(context.Background(), c))

	assert.Equal(t, 25, c.Result().EventsScanned)
	assert.Equal(t, 19, c.Result().EventsMatched)
	require.NotEmpty(t, fw.inputs)
	assert.Equal(t, windowStart, aws.ToTime(fw.inputs[0].StartTime))
	assert.Equal(t, windowEnd, aws.ToTime(fw.inputs[0].EndTime))
	assert.Len(t, fw.inputs, (len(west)+1)/2)
}

func TestLookupSourceBadEvent(t *testing.T) {
	src := &LookupSource{Clients: map[string]LookupEventsAPI{"us-west-2": &fakeLookup{events: []string{"{"}}}}
	err := src.Feed(context.Background(), &Collector{Start: windowStart, End: windowEnd})
	assert.ErrorContains(t, err, "lookup events in us-west-2")
}

func TestAWSSourcesNeedAWindow(t *testing.T) {
	for _, src := range []Source{&LookupSource{}, &S3Source{}} {
		assert.ErrorIs(t, src.Feed(context.Background(), &Collector{Start: windowStart}), errNoWindow)
	}
}

func TestCompileRolePatterns(t *testing.T) {
	res, err := CompileRolePatterns([]string{`-luther-terraform$`})
	require.NoError(t, err)
	assert.True(t, RoleFilter{Patterns: res}.Match("arn:aws:iam::1:role/x-luther-terraform"))
	_, err = CompileRolePatterns([]string{"("})
	assert.Error(t, err)
}

// Digest and Insights files sit beside the logs in a synced bucket and hold
// no API calls, and a path given twice must not count its calls twice.
func TestFileSourceSkipsDigestsAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "CloudTrail", "us-west-2")
	digests := filepath.Join(dir, "CloudTrail-Digest", "us-west-2")
	for _, d := range []string{logs, digests} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	raw, err := os.ReadFile("testdata/trail-records.json")
	require.NoError(t, err)
	logFile := filepath.Join(logs, "log.json.gz")
	require.NoError(t, os.WriteFile(logFile, gz(t, raw), 0o600))
	// A digest file is JSON that is neither format: read, it would fail.
	require.NoError(t, os.WriteFile(filepath.Join(digests, "d.json.gz"), gz(t, []byte(`{"digestStartTime":"x"}`)), 0o600))

	c := &Collector{Roles: terraformRoles(), Start: windowStart, End: windowEnd}
	require.NoError(t, (&FileSource{Paths: []string{dir, logFile}}).Feed(context.Background(), c))
	assert.Equal(t, 25, c.Result().EventsScanned)
}

func TestS3SourceDedupesRegions(t *testing.T) {
	raw, err := os.ReadFile("testdata/trail-records.json")
	require.NoError(t, err)
	f := &fakeS3{objects: map[string][]byte{"p/CloudTrail/us-west-2/2026/11/10/a.json.gz": gz(t, raw)}}
	c := &Collector{Roles: terraformRoles(), Start: windowStart, End: windowEnd}
	src := &S3Source{Client: f, Bucket: "trail", Prefix: "p", Regions: []string{"us-west-2", "us-west-2"}}
	require.NoError(t, src.Feed(context.Background(), c))
	assert.Equal(t, 25, c.Result().EventsScanned)
}

// iam:PassRole has no event, so a policy that lacks it fails the gate through
// the calls that pass a role.
func TestDiffImpliedPassRole(t *testing.T) {
	res := collectFixture(t, terraformRoles())
	noPass, err := ParsePolicy("nopass", []byte(`{"Statement":[{"Effect":"Allow","NotAction":"iam:*","Resource":"*"}]}`))
	require.NoError(t, err)
	rep := Diff(res, DiffOptions{Policies: []*Policy{noPass}})
	var pass *Gap
	for i := range rep.UsedNotAllowed {
		if rep.UsedNotAllowed[i].Action == "iam:PassRole" {
			pass = &rep.UsedNotAllowed[i]
		}
	}
	require.NotNil(t, pass, "%v", rep.UsedNotAllowed)
	assert.Contains(t, pass.Reason, "implied by apigateway:PATCH, ec2:RunInstances, lambda:CreateFunction")
	assert.False(t, rep.OK())
}

// A run that saw none of the roles' calls is no evidence: it fails.
func TestReportNoEventsFails(t *testing.T) {
	rep := Diff(Result{EventsScanned: 10}, DiffOptions{})
	assert.False(t, rep.OK())
	var buf bytes.Buffer
	require.NoError(t, rep.WriteText(&buf))
	assert.Contains(t, buf.String(), "Phase 2 gate: FAIL (no event of the audited roles")
}
