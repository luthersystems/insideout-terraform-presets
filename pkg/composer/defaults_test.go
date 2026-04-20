package composer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleDefaults_PrimitiveShapes(t *testing.T) {
	files := map[string][]byte{
		"variables.tf": []byte(`
variable "name" {
  type    = string
  default = "demo"
}
variable "count" {
  type    = number
  default = 2
}
variable "ratio" {
  type    = number
  default = 0.25
}
variable "enabled" {
  type    = bool
  default = true
}
variable "nullable" {
  type    = string
  default = null
}
variable "ports" {
  type    = list(number)
  default = [80, 443]
}
variable "cidrs" {
  type    = list(string)
  default = ["0.0.0.0/0"]
}
variable "tags" {
  type    = map(string)
  default = { Env = "prod", Team = "core" }
}
variable "empty_list" {
  type    = list(string)
  default = []
}
variable "empty_map" {
  type    = map(string)
  default = {}
}
variable "obj" {
  type    = object({ name = string, port = number })
  default = { name = "x", port = 80 }
}
variable "required" {
  type = string
}
`),
	}

	got, err := ModuleDefaults(files)
	require.NoError(t, err)

	require.Contains(t, got, "nullable", "default = null must be present in the map")
	assert.Nil(t, got["nullable"], "default = null must round-trip as Go nil")

	assert.Equal(t, "demo", got["name"])
	assert.Equal(t, int64(2), got["count"], "whole numbers must stay int64, not float64")
	assert.Equal(t, 0.25, got["ratio"])
	assert.Equal(t, true, got["enabled"])
	assert.Equal(t, []any{int64(80), int64(443)}, got["ports"])
	assert.Equal(t, []any{"0.0.0.0/0"}, got["cidrs"])
	assert.Equal(t, map[string]any{"Env": "prod", "Team": "core"}, got["tags"])
	assert.Equal(t, []any{}, got["empty_list"])
	assert.Equal(t, map[string]any{}, got["empty_map"])
	assert.Equal(t, map[string]any{"name": "x", "port": int64(80)}, got["obj"],
		"object({...}) defaults must round-trip via the IsObjectType branch with int-preservation inside")
	assert.NotContains(t, got, "required", "variables without `default = ...` must be omitted")

	// JSON round-trip: ModuleDefaults claims to return JSON-marshalable Go
	// primitives. A regression returning cty.Value, big.Float, or other
	// non-marshalable types would slip through if we never marshalled.
	// json.Marshal promotes int64→float64, so we re-assert post-promotion shapes
	// to document the actual on-the-wire form reliable will see.
	b, err := json.Marshal(got)
	require.NoError(t, err, "PresetDefaults output must be JSON-marshalable")
	var rt map[string]any
	require.NoError(t, json.Unmarshal(b, &rt))
	assert.Equal(t, "demo", rt["name"])
	assert.Equal(t, float64(2), rt["count"], "JSON round-trip promotes int64 to float64; downstream callers see this shape")
	assert.Equal(t, []any{float64(80), float64(443)}, rt["ports"])
	assert.Nil(t, rt["nullable"])
}

func TestModuleDefaults_OmitsDynamicDefaults(t *testing.T) {
	files := map[string][]byte{
		"variables.tf": []byte(`
variable "base" {
  default = "x"
}
variable "ref" {
  # references another variable
  default = var.base
}
variable "loc" {
  # references a local
  default = local.something
}
variable "fn" {
  # function calls without an EvalContext also fail
  default = lookup({ a = 1 }, "a", 0)
}
`),
	}

	got, err := ModuleDefaults(files)
	require.NoError(t, err)

	assert.Contains(t, got, "base")
	assert.NotContains(t, got, "ref", "references to other variables must be omitted")
	assert.NotContains(t, got, "loc", "references to locals must be omitted")
	// "fn" uses lookup() which actually IS pure-stdlib but we evaluate with nil ctx;
	// hcl.Expression.Value(nil) refuses unknown function calls. Treat that as "non-static".
	assert.NotContains(t, got, "fn", "function calls without an EvalContext must be omitted")
}

func TestModuleDefaults_SkipsNonTFFiles(t *testing.T) {
	files := map[string][]byte{
		"variables.tf":      []byte(`variable "kept" { default = "yes" }`),
		"user_data.sh.tmpl": []byte(`#!/bin/bash\nvariable "noise" { default = "ignored" }`),
		"providers.tfstate": []byte(`{"definitely": "not HCL"}`),
		"unparseable.tf":    []byte(`variable "broken" { default = ` + "`bad`" + `}`),
		"with_outputs.tf": []byte(`
variable "second" {
  default = 42
}
output "irrelevant" {
  value = "ignored"
}
`),
	}

	got, err := ModuleDefaults(files)
	require.NoError(t, err)
	assert.Equal(t, "yes", got["kept"])
	assert.Equal(t, int64(42), got["second"])
	assert.NotContains(t, got, "noise")
	assert.NotContains(t, got, "broken", "unparseable .tf files must be skipped, not error")
}

func TestPresetDefaults_EmbeddedFS_KnownVPCSamples(t *testing.T) {
	c := New() // uses embedded preset FS
	all, err := c.PresetDefaults()
	require.NoError(t, err)

	// aws/vpc must be present and carry the known authored defaults from
	// aws/vpc/variables.tf. If a future PR changes those defaults, this test
	// fails — that's the contract: HCL is the source of truth, the Go export
	// reflects it automatically, and a behavioural change is visible.
	vpc, ok := all["aws/vpc"]
	require.True(t, ok, "aws/vpc must appear in PresetDefaults()")

	assert.Equal(t, "demo", vpc["project"])
	assert.Equal(t, "us-east-1", vpc["region"])
	assert.Equal(t, "10.1.0.0/16", vpc["vpc_cidr"])
	assert.Equal(t, int64(2), vpc["az_count"])
	assert.Equal(t, true, vpc["enable_nat_gateway"])
	assert.Equal(t, true, vpc["single_nat_gateway"])
	assert.Equal(t, true, vpc["enable_private_subnets"])

	// `environment` has no default in aws/vpc — must be absent.
	assert.NotContains(t, vpc, "environment")

	// `eks_cluster_name` has `default = null` — must be present and nil.
	require.Contains(t, vpc, "eks_cluster_name")
	assert.Nil(t, vpc["eks_cluster_name"])
}

func TestPresetDefaults_EmbeddedFS_CoversBothClouds(t *testing.T) {
	c := New()
	all, err := c.PresetDefaults()
	require.NoError(t, err)

	var awsCount, gcpCount int
	for k := range all {
		switch {
		case strings.HasPrefix(k, "aws/"):
			awsCount++
		case strings.HasPrefix(k, "gcp/"):
			gcpCount++
		}
	}
	assert.Greater(t, awsCount, 0, "must surface AWS preset defaults")
	assert.Greater(t, gcpCount, 0, "must surface GCP preset defaults")

	// Cross-contamination guard: gcp/vpc::region is "us-central1" in HCL.
	// aws/vpc::region is "us-east-1". A bug that stuffed AWS defaults under
	// gcp/ keys (or vice versa) would survive a count-only check, so pin
	// the actual values per cloud.
	gcpVPC, ok := all["gcp/vpc"]
	require.True(t, ok, "gcp/vpc must appear in PresetDefaults()")
	assert.Equal(t, "us-central1", gcpVPC["region"],
		"gcp/vpc::region must be the GCP-authored default, not the aws/vpc default")

	awsVPC := all["aws/vpc"]
	assert.Equal(t, "us-east-1", awsVPC["region"],
		"aws/vpc::region must be the AWS-authored default, not the gcp/vpc default")
}

func TestPresetDefaults_NoPresetFS(t *testing.T) {
	c := &Client{} // bypass New(); presets remains nil
	_, err := c.PresetDefaults()
	assert.ErrorIs(t, err, ErrNoPresetFS)
}
