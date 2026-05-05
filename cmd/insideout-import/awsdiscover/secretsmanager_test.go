package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type fakeSMClient struct {
	pages []secretsmanager.ListSecretsOutput
	calls []secretsmanager.ListSecretsInput
	err   error

	// DescribeSecret wiring for DiscoverByID tests.
	describeByID  map[string]*secretsmanager.DescribeSecretOutput
	describeErr   error
	describeCalls []string
}

func (f *fakeSMClient) ListSecrets(_ context.Context, in *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &secretsmanager.ListSecretsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeSMClient) DescribeSecret(_ context.Context, in *secretsmanager.DescribeSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error) {
	id := aws.ToString(in.SecretId)
	f.describeCalls = append(f.describeCalls, id)
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if out, ok := f.describeByID[id]; ok {
		return out, nil
	}
	return nil, &smtypes.ResourceNotFoundException{}
}

func TestSecretsManagerDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &secretsManagerDiscoverer{new: func() smClient {
		return &fakeSMClient{
			pages: []secretsmanager.ListSecretsOutput{
				{
					SecretList: []smtypes.SecretListEntry{
						{Name: aws.String("io-foo/db-password"), ARN: aws.String("arn:aws:secretsmanager:us-east-1:123:secret:io-foo/db-password-AbC")},
						{Name: aws.String("io-foo/api-token"), ARN: aws.String("arn:aws:secretsmanager:us-east-1:123:secret:io-foo/api-token-XyZ")},
					},
				},
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for _, ir := range got {
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["arn"] == "" {
			t.Error("NativeIDs[arn] empty")
		}
	}
}

func TestSecretsManagerDiscover_UsesServerSideTagFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeSMClient{}
	d := &secretsManagerDiscoverer{new: func() smClient { return fake }}
	if _, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123"); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one ListSecrets call")
	}
	// Assert by content, not by index: the discoverer is free to add
	// further filters later (e.g., DeletionStatus) without breaking this
	// test as long as the tag-key + tag-value pair is still present.
	byKey := map[smtypes.FilterNameStringType][]string{}
	for _, f := range fake.calls[0].Filters {
		byKey[f.Key] = f.Values
	}
	if got, want := byKey[smtypes.FilterNameStringTypeTagKey], []string{"Project"}; !equalStrings(got, want) {
		t.Errorf("tag-key filter = %v, want %v", got, want)
	}
	if got, want := byKey[smtypes.FilterNameStringTypeTagValue], []string{"io-foo"}; !equalStrings(got, want) {
		t.Errorf("tag-value filter = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSecretsManagerDiscover_EmptyProjectNoFilters(t *testing.T) {
	t.Parallel()
	fake := &fakeSMClient{}
	d := &secretsManagerDiscoverer{new: func() smClient { return fake }}
	if _, err := d.Discover(context.Background(), "", "us-east-1", "123"); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 || len(fake.calls[0].Filters) != 0 {
		t.Errorf("expected no filters for empty project; got %+v", fake.calls)
	}
}

func TestSecretsManagerDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &secretsManagerDiscoverer{new: func() smClient {
		return &fakeSMClient{
			pages: []secretsmanager.ListSecretsOutput{
				{SecretList: []smtypes.SecretListEntry{{Name: aws.String("a"), ARN: aws.String("arn-a")}}, NextToken: aws.String("t1")},
				{SecretList: []smtypes.SecretListEntry{{Name: aws.String("b"), ARN: aws.String("arn-b")}}}, // terminal
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestSecretsManagerDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &secretsManagerDiscoverer{new: func() smClient { return &fakeSMClient{err: errors.New("AccessDenied")} }}
	_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSecretsManagerDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:secretsmanager:us-east-1:123:secret:io-foo/db-AbCdEf"
	d := &secretsManagerDiscoverer{new: func() smClient {
		return &fakeSMClient{describeByID: map[string]*secretsmanager.DescribeSecretOutput{
			arn: {ARN: aws.String(arn), Name: aws.String("io-foo/db")},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_secretsmanager_secret" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo/db" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["arn"] != arn {
		t.Errorf("NativeIDs[arn]=%q, want %q", got.Identity.NativeIDs["arn"], arn)
	}
}

func TestSecretsManagerDiscoverByID_AcceptsBareName(t *testing.T) {
	t.Parallel()
	d := &secretsManagerDiscoverer{new: func() smClient {
		return &fakeSMClient{describeByID: map[string]*secretsmanager.DescribeSecretOutput{
			"io-foo/db": {ARN: aws.String("arn:test"), Name: aws.String("io-foo/db")},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "io-foo/db", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NameHint != "io-foo/db" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
}

func TestSecretsManagerDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &secretsManagerDiscoverer{new: func() smClient { return &fakeSMClient{} }}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestSecretsManagerDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &secretsManagerDiscoverer{new: func() smClient { return &fakeSMClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket",
		"arn:aws:secretsmanager:us-east-1:123:rotation-schedule:io-foo",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
