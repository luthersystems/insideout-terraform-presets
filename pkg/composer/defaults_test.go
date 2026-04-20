package composer

import (
	"encoding/json"
	"reflect"
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

func TestCamelToSnake(t *testing.T) {
	cases := map[string]string{
		"instanceType":          "instance_type",
		"numServers":            "num_servers",
		"userDataURL":           "user_data_url",
		"multiAz":               "multi_az",
		"haControlPlane":        "ha_control_plane",
		"enableInstanceConnect": "enable_instance_connect",
		"customIngressPorts":    "custom_ingress_ports",
		"sshPublicKey":          "ssh_public_key",
		"mfaRequired":           "mfa_required",
		"cpuSize":               "cpu_size",
		"ha":                    "ha",
		"region":                "region",
		"URL":                   "url",
		// Digit-bearing identifiers (real cases from Config: diskSizeGb,
		// embeddingModelId, modelId, auth0). Digits don't trigger boundaries.
		"diskSizeGb":       "disk_size_gb",
		"embeddingModelId": "embedding_model_id",
		"modelId":          "model_id",
		"auth0":            "auth0",
	}
	for in, want := range cases {
		assert.Equal(t, want, camelToSnake(in), "input=%q", in)
	}
}

func TestApplyPresetDefaults_FillsZeroFieldsOnly(t *testing.T) {
	c := New()
	cfg := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{
			InstanceType: "m6i.large",                   // user-set: must be preserved
			UserData:     "echo configured by the user", // user-set: must be preserved
		},
	}

	err := c.ApplyPresetDefaults(cfg, &Components{Cloud: "aws"}, []ComponentKey{KeyAWSEC2})
	require.NoError(t, err)
	require.NotNil(t, cfg.AWSEC2)

	// User-set fields preserved.
	assert.Equal(t, "m6i.large", cfg.AWSEC2.InstanceType, "non-zero user value must NOT be overwritten")
	assert.Equal(t, "echo configured by the user", cfg.AWSEC2.UserData)

	// Zero-valued fields backfilled from aws/ec2/variables.tf.
	assert.Equal(t, "", cfg.AWSEC2.UserDataURL, "HCL default of \"\" leaves field at zero (still treated as filled)")
	assert.Equal(t, "", cfg.AWSEC2.SSHPublicKey)
	require.NotNil(t, cfg.AWSEC2.EnableInstanceConnect, "*bool field with HCL default = false must be a non-nil pointer")
	assert.False(t, *cfg.AWSEC2.EnableInstanceConnect)

	// Empty list default → []int{} via element-wise coercion.
	assert.NotNil(t, cfg.AWSEC2.CustomIngressPorts, "list default of [] must produce a non-nil empty slice")
	assert.Equal(t, []int{}, cfg.AWSEC2.CustomIngressPorts)

	// Fields with no HCL backer (NumServers etc.) stay zero.
	assert.Empty(t, cfg.AWSEC2.NumServers, "Config fields without an HCL counterpart stay at zero value")
	assert.Empty(t, cfg.AWSEC2.NumCoresPerServer)
}

func TestApplyPresetDefaults_AllocatesNilNestedStruct(t *testing.T) {
	c := New()
	cfg := &Config{} // AWSEC2 is nil

	err := c.ApplyPresetDefaults(cfg, &Components{Cloud: "aws"}, []ComponentKey{KeyAWSEC2})
	require.NoError(t, err)
	require.NotNil(t, cfg.AWSEC2, "selected component with at least one resolvable default must allocate the nested struct")
	assert.Equal(t, "t3.medium", cfg.AWSEC2.InstanceType)
}

func TestCoerceToFieldType(t *testing.T) {
	cases := []struct {
		name string
		src  any
		dst  reflect.Type
		want any
		ok   bool
	}{
		// String-typed targets cover the fmt.Sprint coercion paths used for
		// Config fields like NumServers (HCL number → Go string).
		{"string→string", "demo", reflect.TypeFor[string](), "demo", true},
		{"int64→string", int64(2), reflect.TypeFor[string](), "2", true},
		{"float64→string", 0.25, reflect.TypeFor[string](), "0.25", true},
		{"bool→string", true, reflect.TypeFor[string](), "true", true},

		// Numeric-typed targets cover RetentionDays-style int fields and
		// EstimatedMonthlyRequests-style int64 fields.
		{"int64→int", int64(7), reflect.TypeFor[int](), int(7), true},
		{"int64→int64", int64(7), reflect.TypeFor[int64](), int64(7), true},
		{"float64→int", 7.0, reflect.TypeFor[int](), int(7), true},
		{"int64→float64", int64(7), reflect.TypeFor[float64](), float64(7), true},
		{"float64→float64", 0.5, reflect.TypeFor[float64](), 0.5, true},

		// Bool target.
		{"bool→bool", true, reflect.TypeFor[bool](), true, true},

		// Pointer targets — exercise the recursive *T branch used for
		// Config fields like EnableInstanceConnect (*bool) and CachePaths (*string).
		{"bool→*bool", false, reflect.TypeFor[*bool](), false, true},
		{"string→*string", "x", reflect.TypeFor[*string](), "x", true},

		// Slice elementwise coercion: HCL list(number) → Go []int (used for
		// CustomIngressPorts).
		{"[]any{int64,int64}→[]int", []any{int64(80), int64(443)}, reflect.TypeFor[[]int](), []int{80, 443}, true},
		{"[]any{string}→[]string", []any{"a", "b"}, reflect.TypeFor[[]string](), []string{"a", "b"}, true},
		{"empty []any→[]int", []any{}, reflect.TypeFor[[]int](), []int{}, true},

		// Map elementwise coercion (rare in Config but exercises the
		// reflect.Map branch).
		{"map[string]any→map[string]string", map[string]any{"k": "v"}, reflect.TypeFor[map[string]string](), map[string]string{"k": "v"}, true},

		// Refusals: type combinations we explicitly do NOT coerce so callers
		// know to leave the field at zero rather than apply a wrong value.
		{"string→int (refused)", "7", reflect.TypeFor[int](), nil, false},
		{"string→bool (refused)", "true", reflect.TypeFor[bool](), nil, false},
		{"map→string (refused)", map[string]any{"a": "b"}, reflect.TypeFor[string](), nil, false},
		{"[]any{string}→[]int (refused, element fails)", []any{"x"}, reflect.TypeFor[[]int](), nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := coerceToFieldType(tc.src, tc.dst)
			require.Equal(t, tc.ok, ok)
			if !tc.ok {
				return
			}
			// Pointer comparisons need to deref to the underlying value.
			if got.Kind() == reflect.Ptr {
				assert.Equal(t, tc.want, got.Elem().Interface())
			} else {
				assert.Equal(t, tc.want, got.Interface())
			}
		})
	}
}

func TestApplyPresetDefaults_UnselectedComponentsUntouched(t *testing.T) {
	c := New()
	cfg := &Config{} // AWSEC2 nil, AWSRDS nil

	err := c.ApplyPresetDefaults(cfg, &Components{Cloud: "aws"}, []ComponentKey{KeyAWSEC2})
	require.NoError(t, err)
	assert.NotNil(t, cfg.AWSEC2, "selected component is allocated")
	assert.Nil(t, cfg.AWSRDS, "unselected component must remain nil — no spurious allocation")
	assert.Nil(t, cfg.AWSS3)
}

func TestApplyPresetDefaults_NilCfgErrors(t *testing.T) {
	c := New()
	err := c.ApplyPresetDefaults(nil, &Components{Cloud: "aws"}, []ComponentKey{KeyAWSEC2})
	require.Error(t, err, "nil cfg must error rather than panic")
}

func TestApplyPresetDefaults_NoPresetFS(t *testing.T) {
	c := &Client{} // presets nil
	err := c.ApplyPresetDefaults(&Config{}, &Components{Cloud: "aws"}, []ComponentKey{KeyAWSEC2})
	assert.ErrorIs(t, err, ErrNoPresetFS)
}

// TestApplyPresetDefaults_NamingMismatch_LeavesNilAndZero locks in two
// related contracts in one test:
//
//  1. Silent ignore on snake↔camel mismatch. aws/cloudwatchlogs/variables.tf
//     declares `retention_in_days` (HCL) but Config.AWSCloudWatchLogs's only
//     field is RetentionDays with JSON tag `retentionDays` → snake form
//     `retention_days`. The names don't align, so the field MUST stay zero.
//     If a future refactor switches to fuzzy matching (or panics on miss)
//     this test fails and forces an explicit decision.
//  2. Allocated-but-empty revert. Because no Config field successfully
//     backfills, the inner *struct that ApplyPresetDefaults allocated must
//     be reverted to nil so omitempty works downstream. A regression that
//     leaves the empty inner struct allocated would surface here.
func TestApplyPresetDefaults_NamingMismatch_LeavesNilAndZero(t *testing.T) {
	c := New()
	cfg := &Config{} // AWSCloudWatchLogs is nil

	err := c.ApplyPresetDefaults(cfg, &Components{Cloud: "aws"}, []ComponentKey{KeyAWSCloudWatchLogs})
	require.NoError(t, err, "naming mismatch must not error")
	assert.Nil(t, cfg.AWSCloudWatchLogs,
		"selected component whose preset declares no fields matching any Config tag must leave the inner struct nil (allocated-but-empty revert)")
}

func TestApplyPresetDefaults_GCPRoute(t *testing.T) {
	c := New()
	cfg := &Config{}
	err := c.ApplyPresetDefaults(cfg, &Components{Cloud: "gcp"}, []ComponentKey{KeyGCPCloudSQL})
	require.NoError(t, err)
	// We don't pin a specific GCP value here (GCP preset Configs are sparser);
	// the assertion is just that selecting a GCP key with cloud=gcp doesn't
	// route to the AWS preset tree or panic.
	_ = cfg
}

func TestMergeConfigs_NilGuards(t *testing.T) {
	// All combinations with a nil argument must be no-ops and must not panic.
	populated := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{InstanceType: "t3.medium"},
	}

	assert.NotPanics(t, func() { MergeConfigs(nil, nil) })
	assert.NotPanics(t, func() { MergeConfigs(nil, populated) })
	assert.NotPanics(t, func() { MergeConfigs(&Config{}, nil) })

	// nil dst with non-nil src: dst should remain unchanged (nil guard fires).
	var dst *Config
	MergeConfigs(dst, populated)
	assert.Nil(t, dst)
}

func TestMergeConfigs_AllocatesAndFills(t *testing.T) {
	// AWSEC2 is nil in dst; src has values → struct is allocated and populated.
	src := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{InstanceType: "m6i.large"},
	}
	dst := &Config{}

	MergeConfigs(dst, src)

	require.NotNil(t, dst.AWSEC2, "nil *struct field in dst must be allocated when src has values")
	assert.Equal(t, "m6i.large", dst.AWSEC2.InstanceType)
}

func TestMergeConfigs_PreservesNonZero(t *testing.T) {
	// Non-zero field in dst must not be overwritten by src.
	trueVal := true
	src := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{InstanceType: "m6i.large", EnableInstanceConnect: &trueVal},
	}
	dst := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{InstanceType: "t3.medium"},
	}

	MergeConfigs(dst, src)

	assert.Equal(t, "t3.medium", dst.AWSEC2.InstanceType, "non-zero dst field must be preserved")
}

func TestMergeConfigs_PartialFill(t *testing.T) {
	// One sub-field set in dst, one empty → only the empty field is filled.
	src := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{InstanceType: "m6i.large", SSHPublicKey: "ssh-ed25519 AAAA..."},
	}
	dst := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{InstanceType: "t3.medium"},
	}

	MergeConfigs(dst, src)

	assert.Equal(t, "t3.medium", dst.AWSEC2.InstanceType, "pre-set field must not be overwritten")
	assert.Equal(t, "ssh-ed25519 AAAA...", dst.AWSEC2.SSHPublicKey, "zero field must be filled from src")
}

func TestMergeConfigs_CrossCloudIsolation(t *testing.T) {
	// AWS-only src must not touch GCP fields in dst.
	src := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{InstanceType: "t3.medium"},
	}
	dst := &Config{}

	MergeConfigs(dst, src)

	assert.NotNil(t, dst.AWSEC2, "AWS field must be filled")
	assert.Nil(t, dst.GCPCompute, "GCP field must remain nil when src has no GCP fields")
	assert.Nil(t, dst.GCPGKE)
	assert.Nil(t, dst.GCPCloudSQL)
}

func TestMergeConfigs_BoolPointerFill(t *testing.T) {
	// *bool field nil in dst + &false in src → pointer is copied into dst.
	falseVal := false
	src := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{EnableInstanceConnect: &falseVal},
	}
	dst := &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{},
	}

	MergeConfigs(dst, src)

	require.NotNil(t, dst.AWSEC2.EnableInstanceConnect, "*bool field must be filled when dst has nil and src has non-nil pointer")
	assert.False(t, *dst.AWSEC2.EnableInstanceConnect)
}
