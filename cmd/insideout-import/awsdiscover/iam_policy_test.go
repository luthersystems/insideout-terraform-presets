package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

type fakeIAMPolicyClient struct {
	pages []iam.ListPoliciesOutput
	err   error
	calls []iam.ListPoliciesInput

	getByARN map[string]*iamtypes.Policy
	getErr   error
	getCalls []string
}

func (f *fakeIAMPolicyClient) ListPolicies(_ context.Context, in *iam.ListPoliciesInput, _ ...func(*iam.Options)) (*iam.ListPoliciesOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &iam.ListPoliciesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeIAMPolicyClient) GetPolicy(_ context.Context, in *iam.GetPolicyInput, _ ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	arn := aws.ToString(in.PolicyArn)
	f.getCalls = append(f.getCalls, arn)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if p, ok := f.getByARN[arn]; ok {
		return &iam.GetPolicyOutput{Policy: p}, nil
	}
	return nil, &iamtypes.NoSuchEntityException{}
}

func TestIAMPolicyDiscover_FiltersByPrefixAndScope(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMPolicyClient{pages: []iam.ListPoliciesOutput{
		{Policies: []iamtypes.Policy{
			{PolicyName: aws.String("io-foo-readonly"), Arn: aws.String("arn:aws:iam::123:policy/io-foo-readonly")},
			{PolicyName: aws.String("LegacyPolicy"), Arn: aws.String("arn:aws:iam::123:policy/LegacyPolicy")},
		}},
	}}
	d := &iamPolicyDiscoverer{new: func() iamPolicyClient { return fake }}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-readonly" {
		t.Errorf("NameHint=%q", got[0].Identity.NameHint)
	}
	if len(fake.calls) == 0 || fake.calls[0].Scope != iamtypes.PolicyScopeTypeLocal {
		t.Errorf("ListPolicies should pass Scope=Local; got %v", fake.calls)
	}
}

func TestIAMPolicyDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &iamPolicyDiscoverer{new: func() iamPolicyClient {
		return &fakeIAMPolicyClient{err: errors.New("AccessDenied")}
	}}
	_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIAMPolicyDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:iam::123:policy/io-foo-readonly"
	d := &iamPolicyDiscoverer{new: func() iamPolicyClient {
		return &fakeIAMPolicyClient{getByARN: map[string]*iamtypes.Policy{
			arn: {PolicyName: aws.String("io-foo-readonly"), Arn: aws.String(arn)},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_iam_policy" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-readonly" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.ImportID != arn {
		t.Errorf("ImportID=%q, want %q (policy import id is the arn)", got.Identity.ImportID, arn)
	}
}

func TestIAMPolicyDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &iamPolicyDiscoverer{new: func() iamPolicyClient { return &fakeIAMPolicyClient{} }}
	_, err := d.DiscoverByID(context.Background(),
		"arn:aws:iam::123:policy/missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestIAMPolicyDiscoverByID_RejectsBareName(t *testing.T) {
	t.Parallel()
	d := &iamPolicyDiscoverer{new: func() iamPolicyClient { return &fakeIAMPolicyClient{} }}
	_, err := d.DiscoverByID(context.Background(), "io-foo-readonly", "us-east-1", "123")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("err=%v, want ErrNotSupported (bare names not allowed for policies)", err)
	}
}

func TestIAMPolicyDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &iamPolicyDiscoverer{new: func() iamPolicyClient { return &fakeIAMPolicyClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket",
		"arn:aws:iam::123:role/io-foo",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
