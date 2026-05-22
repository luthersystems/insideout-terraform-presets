package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
)

// TestIsAWSManagedPolicyARN pins the AWS-managed vs customer-owned IAM
// policy distinction (#652): an AWS-managed policy's ARN account field
// is the literal string "aws", across all three partitions.
func TestIsAWSManagedPolicyARN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		arn  string
		want bool
	}{
		{"aws partition managed", "arn:aws:iam::aws:policy/AWSAccountUsageReportAccess", true},
		{"govcloud managed", "arn:aws-us-gov:iam::aws:policy/AdministratorAccess", true},
		{"china managed", "arn:aws-cn:iam::aws:policy/ReadOnlyAccess", true},
		{"aws managed job-function path", "arn:aws:iam::aws:policy/job-function/Billing", true},
		{"customer-owned policy", "arn:aws:iam::123456789012:policy/my-app-policy", false},
		{"customer-owned govcloud", "arn:aws-us-gov:iam::123456789012:policy/my-policy", false},
		{"non-policy ARN", "arn:aws:s3:::my-bucket", false},
		{"empty", "", false},
		{"bare name", "AWSAccountUsageReportAccess", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isAWSManagedPolicyARN(tt.arn); got != tt.want {
				t.Errorf("isAWSManagedPolicyARN(%q)=%v, want %v", tt.arn, got, tt.want)
			}
		})
	}
}

// skipManagedConfig is testConfig with the production AWS-managed-policy
// SkipIdentifier hook wired in, matching the aws_iam_policy config.
func skipManagedConfig() cloudControlConfig {
	cfg := cloudControlConfig{
		TFType:                 "aws_iam_policy",
		CloudFormationType:     "AWS::IAM::ManagedPolicy",
		Slug:                   "iam_policy",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("ManagedPolicyName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	}
	cfg.SkipIdentifier = isAWSManagedPolicyARN
	return cfg
}

// TestCloudControlDiscover_LeaksAWSManagedPolicyWithoutSkipIdentifier is
// the contrast case for TestCloudControlDiscover_SkipsAWSManagedPolicies:
// it documents that a config with no SkipIdentifier hook emits every
// identifier ListResources returns — including AWS-managed IAM policies
// (the #652 leak). It pins that the filtering is driven entirely by the
// SkipIdentifier hook and nothing else, so the Skips… test's pass is
// attributable to the hook.
func TestCloudControlDiscover_LeaksAWSManagedPolicyWithoutSkipIdentifier(t *testing.T) {
	t.Parallel()
	const managed = "arn:aws:iam::aws:policy/AWSAccountUsageReportAccess"
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{listPage("", managed)},
		propsByIdentifier: map[string]map[string]any{
			managed: {"ManagedPolicyName": "AWSAccountUsageReportAccess"},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(), // no SkipIdentifier — the buggy config
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Identity.ImportID != managed {
		t.Fatalf("expected the unfiltered discoverer to leak the AWS-managed policy; got %d resource(s): %+v", len(got), got)
	}
}

// TestCloudControlDiscover_SkipsAWSManagedPolicies pins that the
// SkipIdentifier hook drops AWS-managed IAM policies before the
// GetResource fan-out: they never reach the emitted set, and — proving
// the filter runs at the identifier stage, not post-fetch — GetResource
// is never even called for them (#652).
func TestCloudControlDiscover_SkipsAWSManagedPolicies(t *testing.T) {
	t.Parallel()
	const (
		customer = "arn:aws:iam::123456789012:policy/my-app-policy"
		managedA = "arn:aws:iam::aws:policy/AWSAccountUsageReportAccess"
		managedB = "arn:aws:iam::aws:policy/AdministratorAccess"
	)
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", customer, managedA, managedB),
		},
		propsByIdentifier: map[string]map[string]any{
			// Only the customer-owned policy should ever be fetched.
			customer: {"ManagedPolicyName": "my-app-policy"},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            skipManagedConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123456789012",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d resources, want 1 (only the customer-owned policy)", len(got))
	}
	if got[0].Identity.ImportID != customer {
		t.Errorf("emitted ImportID=%q, want %q", got[0].Identity.ImportID, customer)
	}
	for _, called := range fake.getResourceCalls {
		if called == managedA || called == managedB {
			t.Errorf("GetResource was called for AWS-managed policy %q; SkipIdentifier must drop it before the fan-out", called)
		}
	}
}

// TestCloudControlDiscoverByID_SkipsAWSManagedPolicy pins that a
// dep-chase reaching an AWS-managed policy ARN gets ErrNotSupported
// (so the loop drops the reference) and that no GetResource call is
// issued for it.
func TestCloudControlDiscoverByID_SkipsAWSManagedPolicy(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{}
	d := &cloudControlDiscoverer{
		cfg:            skipManagedConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	_, err := d.DiscoverByID(context.Background(),
		"arn:aws:iam::aws:policy/AWSAccountUsageReportAccess", "us-east-1", "123456789012")
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("err=%v, want ErrNotSupported", err)
	}
	if len(fake.getResourceCalls) != 0 {
		t.Errorf("GetResource called %v; an AWS-managed policy must be skipped before any API call", fake.getResourceCalls)
	}
}

// TestCloudControlDiscoverByID_CustomerPolicyNotSkipped pins the
// negative: a customer-owned policy ARN is NOT skipped by the hook —
// DiscoverByID proceeds to the GetResource call.
func TestCloudControlDiscoverByID_CustomerPolicyNotSkipped(t *testing.T) {
	t.Parallel()
	const customer = "arn:aws:iam::123456789012:policy/my-app-policy"
	fake := &fakeCloudControlClient{
		propsByIdentifier: map[string]map[string]any{
			customer: {"ManagedPolicyName": "my-app-policy"},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            skipManagedConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	ir, err := d.DiscoverByID(context.Background(), customer, "us-east-1", "123456789012")
	if err != nil {
		t.Fatalf("customer-owned policy must not be skipped: %v", err)
	}
	if ir.Identity.ImportID != customer {
		t.Errorf("ImportID=%q, want %q", ir.Identity.ImportID, customer)
	}
}
