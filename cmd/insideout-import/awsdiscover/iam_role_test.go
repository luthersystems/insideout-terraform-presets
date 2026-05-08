package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

type fakeIAMRoleClient struct {
	pages []iam.ListRolesOutput
	err   error
	calls []iam.ListRolesInput

	getByName map[string]*iamtypes.Role
	getErr    error
	getCalls  []string

	// ListRoleTags wiring (#291).
	tagsByRole map[string][]iamtypes.Tag
	tagsErr    error
	tagsCalls  []string
}

func (f *fakeIAMRoleClient) ListRoles(_ context.Context, in *iam.ListRolesInput, _ ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &iam.ListRolesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeIAMRoleClient) GetRole(_ context.Context, in *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	name := aws.ToString(in.RoleName)
	f.getCalls = append(f.getCalls, name)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if r, ok := f.getByName[name]; ok {
		return &iam.GetRoleOutput{Role: r}, nil
	}
	return nil, &iamtypes.NoSuchEntityException{}
}

func (f *fakeIAMRoleClient) ListRoleTags(_ context.Context, in *iam.ListRoleTagsInput, _ ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error) {
	name := aws.ToString(in.RoleName)
	f.tagsCalls = append(f.tagsCalls, name)
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if t, ok := f.tagsByRole[name]; ok {
		return &iam.ListRoleTagsOutput{Tags: t}, nil
	}
	return &iam.ListRoleTagsOutput{}, nil
}

func TestIAMRoleDiscover_FiltersByPrefix(t *testing.T) {
	t.Parallel()
	d := &iamRoleDiscoverer{new: func(_ string) iamRoleClient {
		return &fakeIAMRoleClient{pages: []iam.ListRolesOutput{
			{Roles: []iamtypes.Role{
				{RoleName: aws.String("io-foo-handler"), Arn: aws.String("arn:aws:iam::123:role/io-foo-handler")},
				{RoleName: aws.String("OtherTeamRole"), Arn: aws.String("arn:aws:iam::123:role/OtherTeamRole")},
				{RoleName: aws.String("io-foo-worker"), Arn: aws.String("arn:aws:iam::123:role/io-foo-worker")},
			}},
		}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (prefix-filtered)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-handler" {
		t.Errorf("got[0].NameHint=%q (sort order)", got[0].Identity.NameHint)
	}
}

func TestIAMRoleDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	d := &iamRoleDiscoverer{new: func(_ string) iamRoleClient {
		return &fakeIAMRoleClient{pages: []iam.ListRolesOutput{
			{Roles: []iamtypes.Role{
				{RoleName: aws.String("a"), Arn: aws.String("arn:test:a")},
				{RoleName: aws.String("b"), Arn: aws.String("arn:test:b")},
			}},
		}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestIAMRoleDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &iamRoleDiscoverer{new: func(_ string) iamRoleClient {
		return &fakeIAMRoleClient{err: errors.New("AccessDenied")}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIAMRoleDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:iam::123:role/io-foo-handler"
	d := &iamRoleDiscoverer{new: func(_ string) iamRoleClient {
		return &fakeIAMRoleClient{getByName: map[string]*iamtypes.Role{
			"io-foo-handler": {RoleName: aws.String("io-foo-handler"), Arn: aws.String(arn)},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_iam_role" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-handler" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["arn"] != arn {
		t.Errorf("NativeIDs[arn]=%q, want %q", got.Identity.NativeIDs["arn"], arn)
	}
}

func TestIAMRoleDiscoverByID_StripsPathFromARN(t *testing.T) {
	t.Parallel()
	d := &iamRoleDiscoverer{new: func(_ string) iamRoleClient {
		return &fakeIAMRoleClient{getByName: map[string]*iamtypes.Role{
			"my-role": {RoleName: aws.String("my-role"), Arn: aws.String("arn:test")},
		}}
	}}
	// Path-prefixed ARN — GetRole takes the bare name.
	_, err := d.DiscoverByID(context.Background(),
		"arn:aws:iam::123:role/service-roles/my-role", "us-east-1", "123")
	if err != nil {
		t.Fatalf("expected path stripped to bare name; got %v", err)
	}
}

func TestIAMRoleDiscoverByID_AcceptsBareName(t *testing.T) {
	t.Parallel()
	d := &iamRoleDiscoverer{new: func(_ string) iamRoleClient {
		return &fakeIAMRoleClient{getByName: map[string]*iamtypes.Role{
			"io-foo-handler": {RoleName: aws.String("io-foo-handler"), Arn: aws.String("arn:test")},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "io-foo-handler", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NameHint != "io-foo-handler" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
}

func TestIAMRoleDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &iamRoleDiscoverer{new: func(_ string) iamRoleClient { return &fakeIAMRoleClient{} }}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestIAMRoleDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &iamRoleDiscoverer{new: func(_ string) iamRoleClient { return &fakeIAMRoleClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket",
		"arn:aws:iam::123:user/me", // user, not role
		"arn:aws:iam::123:policy/ReadOnly",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
