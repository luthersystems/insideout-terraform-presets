package runner

import (
	"strings"
	"testing"
)

func TestProvidersTF(t *testing.T) {
	got := string(ProvidersTF("us-east-1"))

	if !strings.Contains(got, `region = "us-east-1"`) {
		t.Error("should contain region")
	}
	if !strings.Contains(got, `hashicorp/aws`) {
		t.Error("should contain AWS provider source")
	}
	if !strings.Contains(got, `>= 6.0`) {
		t.Error("should contain AWS provider version constraint")
	}
	if !strings.Contains(got, `>= 1.5`) {
		t.Error("should contain terraform version constraint")
	}
}

func TestProvidersTFDifferentRegion(t *testing.T) {
	got := string(ProvidersTF("eu-west-1"))
	if !strings.Contains(got, `region = "eu-west-1"`) {
		t.Errorf("should contain eu-west-1, got: %s", got)
	}
}
