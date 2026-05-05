package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type fakeS3Client struct {
	listOut *s3.ListBucketsOutput
	listErr error

	headByName  map[string]bool
	headErr     error
	headCalls   []string
	notFoundTyp string // "NotFound" or "NoSuchBucket" to control which typed error is returned
}

func (f *fakeS3Client) ListBuckets(_ context.Context, _ *s3.ListBucketsInput, _ ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOut == nil {
		return &s3.ListBucketsOutput{}, nil
	}
	return f.listOut, nil
}

func (f *fakeS3Client) HeadBucket(_ context.Context, in *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	name := aws.ToString(in.Bucket)
	f.headCalls = append(f.headCalls, name)
	if f.headErr != nil {
		return nil, f.headErr
	}
	if f.headByName[name] {
		return &s3.HeadBucketOutput{}, nil
	}
	switch f.notFoundTyp {
	case "NoSuchBucket":
		return nil, &s3types.NoSuchBucket{}
	default:
		return nil, &s3types.NotFound{}
	}
}

func TestS3Discover_FiltersByPrefix(t *testing.T) {
	t.Parallel()
	d := &s3Discoverer{new: func() s3Client {
		return &fakeS3Client{listOut: &s3.ListBucketsOutput{Buckets: []s3types.Bucket{
			{Name: aws.String("io-foo-uploads")},
			{Name: aws.String("io-foo-archive")},
			{Name: aws.String("legacy-data")},
		}}}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (prefix-filtered)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-archive" {
		t.Errorf("got[0].NameHint=%q (sort order)", got[0].Identity.NameHint)
	}
	if got[0].Identity.NativeIDs["arn"] != "arn:aws:s3:::io-foo-archive" {
		t.Errorf("NativeIDs[arn]=%q", got[0].Identity.NativeIDs["arn"])
	}
}

func TestS3Discover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &s3Discoverer{new: func() s3Client {
		return &fakeS3Client{listErr: errors.New("AccessDenied")}
	}}
	_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestS3DiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &s3Discoverer{new: func() s3Client {
		return &fakeS3Client{headByName: map[string]bool{"io-foo-uploads": true}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:s3:::io-foo-uploads", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_s3_bucket" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-uploads" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["arn"] != "arn:aws:s3:::io-foo-uploads" {
		t.Errorf("NativeIDs[arn]=%q", got.Identity.NativeIDs["arn"])
	}
}

func TestS3DiscoverByID_AcceptsBareName(t *testing.T) {
	t.Parallel()
	d := &s3Discoverer{new: func() s3Client {
		return &fakeS3Client{headByName: map[string]bool{"io-foo-uploads": true}}
	}}
	got, err := d.DiscoverByID(context.Background(), "io-foo-uploads", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NameHint != "io-foo-uploads" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
}

func TestS3DiscoverByID_NotFoundTyped(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"NotFound", "NoSuchBucket"} {
		typ := typ
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			d := &s3Discoverer{new: func() s3Client {
				return &fakeS3Client{notFoundTyp: typ}
			}}
			_, err := d.DiscoverByID(context.Background(), "missing-bucket", "us-east-1", "123")
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("typ=%s: err=%v, want ErrNotFound", typ, err)
			}
		})
	}
}

func TestS3DiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &s3Discoverer{new: func() s3Client { return &fakeS3Client{} }}
	cases := []string{
		"",
		"arn:aws:lambda:us-east-1:123:function:foo",
		"arn:aws:s3:::a-bucket/object/key",
		"name with spaces",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
