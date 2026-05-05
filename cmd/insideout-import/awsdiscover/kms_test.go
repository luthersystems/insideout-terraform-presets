package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

type fakeKMSClient struct {
	pages []kms.ListAliasesOutput
	err   error
	calls []kms.ListAliasesInput

	describeByID  map[string]*kmstypes.KeyMetadata
	describeErr   error
	describeCalls []string
}

func (f *fakeKMSClient) ListAliases(_ context.Context, in *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &kms.ListAliasesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeKMSClient) DescribeKey(_ context.Context, in *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	id := aws.ToString(in.KeyId)
	f.describeCalls = append(f.describeCalls, id)
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if md, ok := f.describeByID[id]; ok {
		return &kms.DescribeKeyOutput{KeyMetadata: md}, nil
	}
	return nil, &kmstypes.NotFoundException{}
}

func TestKMSDiscover_FiltersByAliasContainsProject(t *testing.T) {
	t.Parallel()
	d := &kmsDiscoverer{new: func() kmsClient {
		return &fakeKMSClient{pages: []kms.ListAliasesOutput{
			{Aliases: []kmstypes.AliasListEntry{
				{
					AliasName:   aws.String("alias/io-foo-data"),
					TargetKeyId: aws.String("uuid-1"),
					AliasArn:    aws.String("arn:aws:kms:us-east-1:123:alias/io-foo-data"),
				},
				{
					AliasName:   aws.String("alias/aws/lambda"),
					TargetKeyId: aws.String("uuid-aws"),
				},
				{
					AliasName:   aws.String("alias/legacy-key"),
					TargetKeyId: aws.String("uuid-legacy"),
				},
				{
					AliasName: aws.String("alias/io-foo-orphan"),
					// Missing TargetKeyId — not a real customer-managed key.
				},
			}},
		}}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (alias contains project, has TargetKeyId, not aws-managed)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-data" {
		t.Errorf("NameHint=%q (alias trimmed of 'alias/' prefix)", got[0].Identity.NameHint)
	}
	if got[0].Identity.ImportID != "uuid-1" {
		t.Errorf("ImportID=%q, want uuid-1 (key UUID from TargetKeyId)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NativeIDs["arn"] != "arn:aws:kms:us-east-1:123:key/uuid-1" {
		t.Errorf("NativeIDs[arn]=%q (synthesized from region/accountID)", got[0].Identity.NativeIDs["arn"])
	}
}

func TestKMSDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &kmsDiscoverer{new: func() kmsClient {
		return &fakeKMSClient{err: errors.New("AccessDenied")}
	}}
	_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestKMSDiscoverByID_AcceptsKeyARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:kms:us-east-1:123:key/uuid-1"
	d := &kmsDiscoverer{new: func() kmsClient {
		return &fakeKMSClient{describeByID: map[string]*kmstypes.KeyMetadata{
			arn: {KeyId: aws.String("uuid-1"), Arn: aws.String(arn)},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_kms_key" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "uuid-1" {
		t.Errorf("ImportID=%q, want uuid-1", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["arn"] != arn {
		t.Errorf("NativeIDs[arn]=%q", got.Identity.NativeIDs["arn"])
	}
}

func TestKMSDiscoverByID_AcceptsBareUUID(t *testing.T) {
	t.Parallel()
	d := &kmsDiscoverer{new: func() kmsClient {
		return &fakeKMSClient{describeByID: map[string]*kmstypes.KeyMetadata{
			"uuid-1": {KeyId: aws.String("uuid-1"), Arn: aws.String("arn:aws:kms:us-east-1:123:key/uuid-1")},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "uuid-1", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "uuid-1" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestKMSDiscoverByID_AcceptsAliasName(t *testing.T) {
	t.Parallel()
	d := &kmsDiscoverer{new: func() kmsClient {
		return &fakeKMSClient{describeByID: map[string]*kmstypes.KeyMetadata{
			"alias/io-foo-data": {KeyId: aws.String("uuid-1"), Arn: aws.String("arn:aws:kms:us-east-1:123:key/uuid-1")},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "alias/io-foo-data", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "uuid-1" {
		t.Errorf("ImportID=%q (resolved alias to UUID)", got.Identity.ImportID)
	}
}

func TestKMSDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &kmsDiscoverer{new: func() kmsClient { return &fakeKMSClient{} }}
	_, err := d.DiscoverByID(context.Background(), "uuid-missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestKMSDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &kmsDiscoverer{new: func() kmsClient { return &fakeKMSClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket",
		"arn:aws:kms:us-east-1:123:grant/abc",
		"name with spaces",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
