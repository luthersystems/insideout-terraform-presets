package importid

import (
	"encoding/json"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestForResource_AppendsRegionForAWSRegionAwareResource(t *testing.T) {
	t.Parallel()
	attrs, err := json.Marshal(&generated.AWSS3Bucket{
		Bucket: generated.LiteralOf("io-uploads"),
		Region: generated.LiteralOf("us-west-2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.io_uploads",
			Region:   "us-east-1",
			ImportID: "io-uploads",
		},
		Attrs: attrs,
	}

	if got, want := ForResource(ir), "io-uploads@us-west-2"; got != want {
		t.Fatalf("ForResource() = %q, want %q", got, want)
	}
}

func TestForResource_UsesIdentityRegionWhenAttrsDoNotCarryRegion(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.jobs",
			Region:   "us-east-1",
			ImportID: "https://sqs.us-east-1.amazonaws.com/123456789012/jobs",
		},
	}

	if got, want := ForResource(ir), "https://sqs.us-east-1.amazonaws.com/123456789012/jobs@us-east-1"; got != want {
		t.Fatalf("ForResource() = %q, want %q", got, want)
	}
}

func TestForResource_KeepsRawIDWhenRegionUnknown(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.io_uploads",
			ImportID: "io-uploads",
		},
	}

	if got, want := ForResource(ir), "io-uploads"; got != want {
		t.Fatalf("ForResource() = %q, want %q", got, want)
	}
}

func TestForResource_DoesNotAppendRegionForAWSGlobalResource(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_iam_policy",
			Address:  "aws_iam_policy.readonly",
			Region:   "us-east-1",
			ImportID: "arn:aws:iam::123456789012:policy/readonly",
		},
	}

	if got, want := ForResource(ir), "arn:aws:iam::123456789012:policy/readonly"; got != want {
		t.Fatalf("ForResource() = %q, want %q", got, want)
	}
}

func TestForResource_DoesNotAppendRegionForGCP(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     "google_pubsub_topic",
			Address:  "google_pubsub_topic.events",
			Region:   "us-central1",
			ImportID: "projects/test/topics/events",
		},
	}

	if got, want := ForResource(ir), "projects/test/topics/events"; got != want {
		t.Fatalf("ForResource() = %q, want %q", got, want)
	}
}

func TestForResource_KeepsExistingRegionSuffix(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.io_uploads",
			Region:   "us-east-1",
			ImportID: "io-uploads@us-east-1",
		},
	}

	if got, want := ForResource(ir), "io-uploads@us-east-1"; got != want {
		t.Fatalf("ForResource() = %q, want %q", got, want)
	}
}
