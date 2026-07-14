package imported

import "testing"

func TestIsTerraformStateBucketType(t *testing.T) {
	if TerraformStateBucketType != "aws_s3_bucket" {
		t.Fatalf("TerraformStateBucketType = %q, want aws_s3_bucket", TerraformStateBucketType)
	}
	cases := []struct {
		tfType string
		want   bool
	}{
		{"aws_s3_bucket", true},
		{"  aws_s3_bucket  ", true}, // whitespace trimmed, matches the consumer's prior inline gate
		{"aws_s3_bucket_versioning", false},
		{"google_storage_bucket", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsTerraformStateBucketType(c.tfType); got != c.want {
			t.Errorf("IsTerraformStateBucketType(%q) = %v, want %v", c.tfType, got, c.want)
		}
	}
}
