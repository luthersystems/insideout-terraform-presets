package reversedisco

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport"
)

// Both adapters must satisfy the engine's dep-chase (Discoverer) and
// selection-closure (ClosureDiscoverer) surfaces, otherwise the engine falls
// back to the selection_closure_unavailable diagnostic and skips closure +
// dep-chase (luthersystems/mars#195).
var (
	_ reverseimport.Discoverer        = awsAggAdapter{}
	_ reverseimport.ClosureDiscoverer = awsAggAdapter{}
	_ reverseimport.Discoverer        = gcpAggAdapter{}
	_ reverseimport.ClosureDiscoverer = gcpAggAdapter{}
)

func TestNewRejectsUnknownCloud(t *testing.T) {
	d, cleanup, err := New(context.Background(), "azure", "", "", "", AWSAssumeRole{})
	if err == nil {
		t.Fatalf("New(cloud=azure) err = nil, want unknown-cloud error")
	}
	if d != nil {
		t.Fatalf("New(cloud=azure) discoverer = %v, want nil", d)
	}
	// cleanup is always non-nil and safe to call even on the error path.
	cleanup()
}

func TestNewAWSReturnsClosureCapableDiscoverer(t *testing.T) {
	// The AWS path only loads SDK config (no network call), so it is safe in
	// a unit test. The point is to prove New returns a value that satisfies
	// the closure surface — the wiring the Mars job was missing.
	d, cleanup, err := New(context.Background(), "aws", "us-west-2", "", "", AWSAssumeRole{})
	if err != nil {
		t.Fatalf("New(cloud=aws) err = %v", err)
	}
	defer cleanup()
	if d == nil {
		t.Fatal("New(cloud=aws) discoverer = nil, want non-nil")
	}
	if _, ok := d.(reverseimport.ClosureDiscoverer); !ok {
		t.Fatalf("New(cloud=aws) discoverer %T does not implement reverseimport.ClosureDiscoverer", d)
	}
}

// TestNewAWSAssumesRoleWhenAuthPresent proves the #739 credential fix: when a
// RoleARN is resolved, New wraps the discoverer's AWS config with an STS
// AssumeRole provider for that role/external-id, so the discoverer's direct SDK
// calls run as the customer-account role (the same principal Terraform's
// provider blocks assume) — not the ambient pod/CLI credentials. When no role
// is present the config is left on ambient credentials so the local CLI keeps
// working unchanged.
func TestNewAWSAssumesRoleWhenAuthPresent(t *testing.T) {
	// Swap the assume-role applier for a recorder so the test asserts the
	// wiring without standing up a live STS endpoint.
	var got AWSAssumeRole
	calls := 0
	orig := applyAWSAssumeRole
	applyAWSAssumeRole = func(cfg aws.Config, auth AWSAssumeRole) aws.Config {
		got = auth
		calls++
		return orig(cfg, auth)
	}
	t.Cleanup(func() { applyAWSAssumeRole = orig })

	want := AWSAssumeRole{
		RoleARN:    "arn:aws:iam::031780745048:role/customer-terraform",
		ExternalID: "io-ext-id",
	}
	_, cleanup, err := New(context.Background(), "aws", "us-east-1", "", "", want)
	if err != nil {
		t.Fatalf("New(cloud=aws) err = %v", err)
	}
	defer cleanup()
	if calls != 1 {
		t.Fatalf("applyAWSAssumeRole called %d times, want 1", calls)
	}
	if got != want {
		t.Fatalf("assume-role auth = %#v, want %#v", got, want)
	}
}

// TestAWSParentScope_KeysByParentCFNType proves the #739 scoping fix builds the
// per-CloudFormation-type selected-parent scope from the closure request: each
// selected parent whose Terraform type is a known Cloud Control type contributes
// its identifier (ImportID, falling back to NameHint) under the parent's CFN
// type; parents not routed through Cloud Control are skipped.
func TestAWSParentScope_KeysByParentCFNType(t *testing.T) {
	parents := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", ImportID: "io-uploads"}},
		{Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", ImportID: "io-logs"}},
		// NameHint fallback when ImportID is empty.
		{Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", NameHint: "/app/api"}},
		// A type with no Cloud Control backing is skipped (no panic, no entry).
		{Identity: imported.ResourceIdentity{Type: "aws_not_a_real_type", ImportID: "x"}},
	}
	scope := awsParentScope(parents)

	wantBuckets := []string{"io-logs", "io-uploads"}
	if got := scope["AWS::S3::Bucket"]; !reflect.DeepEqual(got, wantBuckets) {
		t.Errorf("AWS::S3::Bucket scope = %v, want %v", got, wantBuckets)
	}
	// The bucket-policy child shares the bucket's identifier, so it is scoped
	// by the same selected bucket names — no account-wide BucketPolicy list.
	if got := scope["AWS::S3::BucketPolicy"]; !reflect.DeepEqual(got, wantBuckets) {
		t.Errorf("AWS::S3::BucketPolicy scope = %v, want %v (identifier-shared child)", got, wantBuckets)
	}
	if got := scope["AWS::Logs::LogGroup"]; !reflect.DeepEqual(got, []string{"/app/api"}) {
		t.Errorf("AWS::Logs::LogGroup scope = %v, want [/app/api]", got)
	}
	for cfn := range scope {
		switch cfn {
		case "AWS::S3::Bucket", "AWS::S3::BucketPolicy", "AWS::Logs::LogGroup":
		default:
			t.Errorf("unexpected scope key %q (unknown-type parent should be skipped)", cfn)
		}
	}
	// Guard the awsdiscover seam this relies on.
	if cfn, ok := awsdiscover.CloudFormationTypeForTF("aws_s3_bucket"); !ok || cfn != "AWS::S3::Bucket" {
		t.Errorf("CloudFormationTypeForTF(aws_s3_bucket) = (%q, %v), want (AWS::S3::Bucket, true)", cfn, ok)
	}
	if _, ok := awsdiscover.CloudFormationTypeForTF("aws_not_a_real_type"); ok {
		t.Error("CloudFormationTypeForTF should return false for an unknown type")
	}
}

// TestApplyAWSAssumeRole verifies the credential-provider wiring directly: an
// empty RoleARN leaves Credentials untouched (ambient, local-CLI path), and a
// non-empty RoleARN swaps in a distinct provider (the assume-role hop). It does
// not call STS — construction is lazy — so it is a pure unit test.
func TestApplyAWSAssumeRole(t *testing.T) {
	base := aws.Config{Region: "us-east-1", Credentials: aws.AnonymousCredentials{}}

	unchanged := applyAWSAssumeRole(base, AWSAssumeRole{})
	if unchanged.Credentials != base.Credentials {
		t.Fatalf("empty RoleARN changed Credentials: %T", unchanged.Credentials)
	}
	// Whitespace-only RoleARN is treated as absent.
	if blank := applyAWSAssumeRole(base, AWSAssumeRole{RoleARN: "   "}); blank.Credentials != base.Credentials {
		t.Fatalf("whitespace RoleARN changed Credentials: %T", blank.Credentials)
	}

	wrapped := applyAWSAssumeRole(base, AWSAssumeRole{RoleARN: "arn:aws:iam::000000000000:role/x"})
	if wrapped.Credentials == base.Credentials || wrapped.Credentials == nil {
		t.Fatalf("non-empty RoleARN did not swap in an assume-role provider: %T", wrapped.Credentials)
	}
}
