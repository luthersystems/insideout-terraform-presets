package main

import (
	"reflect"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	reversejob "github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

func TestRequestRegions(t *testing.T) {
	req := reversejob.Request{
		Resources: []reversejob.ResourceSpec{
			{Identity: imported.ResourceIdentity{Region: "us-west-2"}},
			{Identity: imported.ResourceIdentity{Region: "us-east-1"}},
			{Identity: imported.ResourceIdentity{Region: "us-west-2"}},
			{Identity: imported.ResourceIdentity{}},
		},
	}
	got := requestRegions(req, "")
	want := []string{"us-east-1", "us-west-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requestRegions() = %v, want %v", got, want)
	}
}

func TestRequestRegionsUsesOverride(t *testing.T) {
	req := reversejob.Request{
		Resources: []reversejob.ResourceSpec{
			{Identity: imported.ResourceIdentity{Region: "us-east-1"}},
		},
	}
	got := requestRegions(req, " eu-west-2 ")
	want := []string{"eu-west-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requestRegions() = %v, want %v", got, want)
	}
}
