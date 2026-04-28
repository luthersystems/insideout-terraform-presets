package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateGCPProjectID covers every branch of the new GCP project ID
// validator added for issue #157. The matrix locks in: cloud-gating (AWS
// is a no-op), the empty-required code, and the format-rejection code with
// representative invalid shapes (uppercase, too-short, leading digit,
// trailing hyphen, unsupported chars).
func TestValidateGCPProjectID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		cloud     string
		projectID string
		wantCode  string // empty = expect no issues
	}{
		{name: "aws cloud is a no-op even with empty project id", cloud: "aws", projectID: "", wantCode: ""},
		{name: "aws cloud is a no-op even with garbage project id", cloud: "aws", projectID: "NOT-VALID-AT-ALL!!", wantCode: ""},
		{name: "empty cloud is a no-op (defaults handled by composer)", cloud: "", projectID: "", wantCode: ""},
		{name: "GCP empty -> required", cloud: "gcp", projectID: "", wantCode: "gcp_project_id_required"},
		{name: "GCP whitespace-only -> required", cloud: "gcp", projectID: "   ", wantCode: "gcp_project_id_required"},
		{name: "GCP valid project id", cloud: "gcp", projectID: "my-prod-12345", wantCode: ""},
		{name: "GCP valid lowercase no hyphen", cloud: "gcp", projectID: "myproject1234", wantCode: ""},
		{name: "GCP uppercase rejected", cloud: "gcp", projectID: "My-Prod-12345", wantCode: "gcp_invalid_project_id"},
		{name: "GCP too short rejected", cloud: "gcp", projectID: "ab123", wantCode: "gcp_invalid_project_id"},
		{name: "GCP starts with digit rejected", cloud: "gcp", projectID: "1my-project", wantCode: "gcp_invalid_project_id"},
		{name: "GCP ends with hyphen rejected", cloud: "gcp", projectID: "my-project-", wantCode: "gcp_invalid_project_id"},
		{name: "GCP underscore rejected", cloud: "gcp", projectID: "my_project_123", wantCode: "gcp_invalid_project_id"},
		{name: "case-insensitive cloud match", cloud: "GCP", projectID: "", wantCode: "gcp_project_id_required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ValidateGCPProjectID(tc.cloud, tc.projectID)
			if tc.wantCode == "" {
				require.Empty(t, got, "expected no issues, got %#v", got)
				return
			}
			require.Len(t, got, 1, "expected exactly one issue")
			require.Equal(t, "gcp_project_id", got[0].Field)
			require.Equal(t, tc.wantCode, got[0].Code)
			require.NotEmpty(t, got[0].Reason)
		})
	}
}

// TestComposeStack_GCP_RequiresProjectID confirms the validator is wired into
// ComposeStackWithIssues — a GCP compose with empty GCPProjectID surfaces the
// dedicated issue at the structured layer (in addition to the existing
// missing_required_variable issue from the per-module project_id check). This
// is the bug from #157: catching it at compose time instead of after a
// multi-minute Terraform apply that fails with "Unknown project id".
func TestComposeStack_GCP_RequiresProjectID(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPVPC},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "io-abc123def456", // AWS-style prefix the bug let through
		Region:       "us-central1",
	})
	require.NoError(t, err, "ComposeStackWithIssues should not hard-error in non-strict mode")

	codes := map[string]bool{}
	for _, iss := range res.Issues {
		codes[iss.Code] = true
	}
	require.True(t, codes["gcp_project_id_required"],
		"expected gcp_project_id_required issue when GCPProjectID is empty; got: %#v", res.Issues)
}

// TestComposeStack_GCP_AcceptsProjectID confirms a valid GCPProjectID composes
// cleanly and lands as <key>_project_id in the per-module .auto.tfvars and as
// project_id in the root variables.tf.
func TestComposeStack_GCP_AcceptsProjectID(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPVPC},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "io-stacknaming",
		GCPProjectID: "my-real-gcp-12345",
		Region:       "us-central1",
	})
	require.NoError(t, err, "ComposeStack with valid GCPProjectID should succeed")

	tfvars, ok := out["/gcp_vpc.auto.tfvars"]
	require.True(t, ok, "should emit per-module tfvars")
	tfvarsStr := string(tfvars)
	// EmitAutoTFVars right-aligns the equals sign across keys, so a literal
	// "gcp_vpc_project = ..." substring would miss when the longer
	// "gcp_vpc_project_id" key bumps the column. Assert per-key + value.
	require.Regexp(t, `gcp_vpc_project_id\s*=\s*"my-real-gcp-12345"`, tfvarsStr,
		"per-module tfvars should namespace project_id with the component key")
	require.Regexp(t, `gcp_vpc_project\s*=\s*"io-stacknaming"`, tfvarsStr,
		"naming prefix continues to land under the existing project namespace")

	rootVars, ok := out["/variables.tf"]
	require.True(t, ok, "should emit root variables.tf")
	require.Contains(t, string(rootVars), `variable "gcp_vpc_project_id"`,
		"root variables.tf should declare the namespaced project_id variable")
}

// TestValidateOpts_DerivesCloudFromComps confirms ValidateOpts can recover the
// cloud from Comps when opts.Cloud is empty — the chat-preview path passes
// Comps without setting opts.Cloud explicitly.
func TestValidateOpts_DerivesCloudFromComps(t *testing.T) {
	t.Parallel()

	got := ValidateOpts(ComposeStackOpts{
		Comps: &Components{Cloud: "GCP"},
	})
	require.Len(t, got, 1, "GCP cloud derived from Comps should trigger the validator")
	require.Equal(t, "gcp_project_id_required", got[0].Code)
}
