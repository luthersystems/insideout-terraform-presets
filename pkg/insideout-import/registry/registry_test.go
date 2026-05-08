package registry

import (
	"reflect"
	"sort"
	"testing"
)

func TestSupportedDiscoverTypes_AWS(t *testing.T) {
	t.Parallel()
	want := []string{
		"aws_cloudwatch_log_group",
		"aws_dynamodb_table",
		"aws_iam_policy",
		"aws_iam_role",
		"aws_kms_key",
		"aws_lambda_function",
		"aws_s3_bucket",
		"aws_secretsmanager_secret",
		"aws_sqs_queue",
	}
	got := SupportedDiscoverTypes(ProviderAWS)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SupportedDiscoverTypes(%q) = %v, want %v", ProviderAWS, got, want)
	}
}

func TestSupportedDiscoverTypes_GCP(t *testing.T) {
	t.Parallel()
	want := []string{
		"google_compute_network",
		"google_pubsub_subscription",
		"google_pubsub_topic",
		"google_secret_manager_secret",
		"google_storage_bucket",
	}
	got := SupportedDiscoverTypes(ProviderGCP)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SupportedDiscoverTypes(%q) = %v, want %v", ProviderGCP, got, want)
	}
}

func TestSupportedDiscoverTypes_Unknown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		provider string
	}{
		{name: "empty", provider: ""},
		{name: "azure", provider: "azure"},
		{name: "AWS_uppercase", provider: "AWS"},
		{name: "whitespace", provider: " aws "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SupportedDiscoverTypes(tc.provider); got != nil {
				t.Errorf("SupportedDiscoverTypes(%q) = %v, want nil", tc.provider, got)
			}
		})
	}
}

func TestSupportedDiscoverTypes_ReturnsCopy(t *testing.T) {
	t.Parallel()
	first := SupportedDiscoverTypes(ProviderAWS)
	if len(first) == 0 {
		t.Fatal("expected non-empty AWS type list")
	}
	first[0] = "MUTATED"

	second := SupportedDiscoverTypes(ProviderAWS)
	if second[0] == "MUTATED" {
		t.Errorf("mutating returned slice leaked into the package; second call returned %v", second)
	}
}

func TestSupportedDiscoverTypes_Sorted(t *testing.T) {
	t.Parallel()
	for _, provider := range SupportedProviders() {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			got := SupportedDiscoverTypes(provider)
			if !sort.StringsAreSorted(got) {
				t.Errorf("SupportedDiscoverTypes(%q) not sorted: %v", provider, got)
			}
		})
	}
}

func TestSupportedProviders(t *testing.T) {
	t.Parallel()
	want := []string{ProviderAWS, ProviderGCP}
	got := SupportedProviders()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SupportedProviders() = %v, want %v", got, want)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("SupportedProviders() not sorted: %v", got)
	}
}
