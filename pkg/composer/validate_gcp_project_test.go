package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateGCPProjectID covers every branch of the GCP project ID
// validator added for issue #157. The matrix locks in: cloud-gating
// (AWS / empty cloud are no-ops), the empty-required code, and the
// format-rejection code with representative invalid shapes. Boundary
// cases (5 / 6 / 30 / 31 chars) pin the regex's {4,28} length encoding
// against off-by-one mutations.
func TestValidateGCPProjectID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		cloud     string
		projectID string
		wantCode  string // empty = expect no issues
	}{
		// Cloud gating
		{name: "aws cloud is a no-op even with empty project id", cloud: "aws", projectID: "", wantCode: ""},
		{name: "aws cloud is a no-op even with garbage project id", cloud: "aws", projectID: "NOT-VALID-AT-ALL!!", wantCode: ""},
		{name: "empty cloud is a no-op (defaults handled by composer)", cloud: "", projectID: "", wantCode: ""},
		{name: "case-insensitive cloud match", cloud: "GCP", projectID: "", wantCode: "gcp_project_id_required"},

		// Required-empty branch
		{name: "GCP empty -> required", cloud: "gcp", projectID: "", wantCode: "gcp_project_id_required"},
		{name: "GCP whitespace-only -> required", cloud: "gcp", projectID: "   ", wantCode: "gcp_project_id_required"},

		// Format-valid branch
		{name: "valid project id with hyphens", cloud: "gcp", projectID: "my-prod-12345", wantCode: ""},
		{name: "valid project id no hyphen", cloud: "gcp", projectID: "myproject1234", wantCode: ""},
		{name: "valid at minimum length 6", cloud: "gcp", projectID: "abcde1", wantCode: ""},
		{name: "valid at maximum length 30", cloud: "gcp", projectID: "a234567890123456789012345678yz", wantCode: ""},

		// Format-invalid branch
		{name: "uppercase rejected", cloud: "gcp", projectID: "My-Prod-12345", wantCode: "gcp_invalid_project_id"},
		{name: "5 chars rejected (boundary)", cloud: "gcp", projectID: "abcd1", wantCode: "gcp_invalid_project_id"},
		{name: "31 chars rejected (boundary)", cloud: "gcp", projectID: "a2345678901234567890123456789yz", wantCode: "gcp_invalid_project_id"},
		{name: "starts with digit rejected", cloud: "gcp", projectID: "1my-project", wantCode: "gcp_invalid_project_id"},
		{name: "ends with hyphen rejected", cloud: "gcp", projectID: "my-project-", wantCode: "gcp_invalid_project_id"},
		{name: "underscore rejected", cloud: "gcp", projectID: "my_project_123", wantCode: "gcp_invalid_project_id"},
		{name: "trailing whitespace tolerated and trimmed (valid id)", cloud: "gcp", projectID: " my-prod-12345 ", wantCode: ""},
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

// TestValidateOpts pins the cloud-resolution rules ValidateOpts uses to
// gate the GCP project ID check. Each subtest documents an independent
// branch of the resolver — explicit opts.Cloud, fallback to Comps.Cloud,
// nil Comps, and mismatch between the two.
func TestValidateOpts(t *testing.T) {
	t.Parallel()

	t.Run("explicit opts.Cloud=gcp triggers validation", func(t *testing.T) {
		t.Parallel()
		got := ValidateOpts(ComposeStackOpts{Cloud: "gcp"})
		require.Len(t, got, 1)
		require.Equal(t, "gcp_project_id_required", got[0].Code)
	})

	t.Run("opts.Cloud=aws is a no-op even when Comps.Cloud=GCP", func(t *testing.T) {
		t.Parallel()
		// Mismatch case: explicit opts.Cloud wins over Comps.Cloud. This
		// matters for the chat-preview path where reliable might pass
		// Cloud="aws" deliberately while Comps still carries leftover GCP
		// fields from a prior turn.
		got := ValidateOpts(ComposeStackOpts{
			Cloud: "aws",
			Comps: &Components{Cloud: "GCP"},
		})
		require.Empty(t, got, "explicit opts.Cloud must override Comps-derived cloud")
	})

	t.Run("nil Comps with explicit Cloud=gcp still triggers", func(t *testing.T) {
		t.Parallel()
		got := ValidateOpts(ComposeStackOpts{Cloud: "gcp", Comps: nil})
		require.Len(t, got, 1)
		require.Equal(t, "gcp_project_id_required", got[0].Code)
	})

	t.Run("derives cloud from Comps when opts.Cloud empty", func(t *testing.T) {
		t.Parallel()
		// Chat-preview path: opts.Cloud unset, Comps populated.
		got := ValidateOpts(ComposeStackOpts{Comps: &Components{Cloud: "GCP"}})
		require.Len(t, got, 1)
		require.Equal(t, "gcp_project_id_required", got[0].Code)
	})

	t.Run("aws via Comps is a no-op", func(t *testing.T) {
		t.Parallel()
		got := ValidateOpts(ComposeStackOpts{Comps: &Components{Cloud: "AWS"}})
		require.Empty(t, got)
	})

	t.Run("valid GCPProjectID composes cleanly", func(t *testing.T) {
		t.Parallel()
		got := ValidateOpts(ComposeStackOpts{
			Cloud:        "gcp",
			GCPProjectID: "my-real-12345",
		})
		require.Empty(t, got)
	})
}

// TestComposeStack_GCP groups every stack-level integration assertion for
// the project-id split under one parent so it reads as a single decision
// table — the patterns the repo uses elsewhere (compose_bedrock_aoss_test.go).
func TestComposeStack_GCP(t *testing.T) {
	t.Parallel()

	t.Run("empty GCPProjectID surfaces gcp_project_id_required at compose time", func(t *testing.T) {
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
	})

	t.Run("AWS naming prefix never leaks into gcp_*_project_id (regression for #157)", func(t *testing.T) {
		t.Parallel()
		// The prod failure was Project="io-<sessionhash>" (which happens to
		// pass the GCP project ID character-set regex) silently flowing
		// through to var.project_id. This test pins that the prefix lands
		// in <key>_project (correct) and does NOT land in any *_project_id
		// key (which would silently re-introduce the bug).
		const prodPrefix = "io-abc123def456"
		c := newTestClient()
		res, err := c.ComposeStackWithIssues(ComposeStackOpts{
			Cloud:        "gcp",
			SelectedKeys: []ComponentKey{KeyGCPVPC},
			Comps:        &Components{},
			Cfg:          &Config{Region: "us-central1"},
			Project:      prodPrefix,
			// GCPProjectID intentionally empty — the bug case.
			Region: "us-central1",
		})
		require.NoError(t, err)

		// Inspect every per-module tfvars file. None of them should bind
		// the prefix to *_project_id; that's the regression surface.
		for path, body := range res.Files {
			if !strings.HasSuffix(path, ".auto.tfvars") {
				continue
			}
			require.NotRegexp(t,
				`(?m)^\S*_project_id\s*=\s*"`+prodPrefix+`"`,
				string(body),
				"prefix %q must not flow into any *_project_id binding (file %s)", prodPrefix, path)
		}

		// And confirm the prefix DID land where it belongs — the naming
		// namespace under <key>_project — so we know the test isn't
		// vacuously passing because the prefix dropped on the floor.
		vpcTfvars, ok := res.Files["/gcp_vpc.auto.tfvars"]
		require.True(t, ok)
		require.Regexp(t,
			`(?m)^gcp_vpc_project\s*=\s*"`+prodPrefix+`"$`,
			string(vpcTfvars),
			"naming prefix should still land in gcp_vpc_project")
	})

	t.Run("valid GCPProjectID composes cleanly and wires through to module block", func(t *testing.T) {
		t.Parallel()
		const prefix = "io-stacknaming"
		const projectID = "my-real-gcp-12345"
		c := newTestClient()
		out, err := c.ComposeStack(ComposeStackOpts{
			Cloud:        "gcp",
			SelectedKeys: []ComponentKey{KeyGCPVPC},
			Comps:        &Components{},
			Cfg:          &Config{Region: "us-central1"},
			Project:      prefix,
			GCPProjectID: projectID,
			Region:       "us-central1",
		})
		require.NoError(t, err, "ComposeStack with valid GCPProjectID should succeed")

		// Per-module tfvars: each gcp_* module gets both _project (prefix)
		// and _project_id (real ID) namespaced bindings. Iterate over every
		// emitted gcp_* tfvars file rather than assuming only the seed key,
		// so an implicit-dependency expansion (e.g. KeyGCPVPC pulling in
		// another module) doesn't reduce coverage.
		gcpTfvarsCount := 0
		for path, body := range out {
			if !strings.HasSuffix(path, ".auto.tfvars") || !strings.HasPrefix(path, "/gcp_") {
				continue
			}
			gcpTfvarsCount++
			s := string(body)
			// EmitAutoTFVars right-aligns the equals column when keys
			// differ in length — anchor with (?m)^...$ so the
			// shorter-key assertion doesn't accidentally match the
			// longer "_project_id" line.
			key := strings.TrimSuffix(strings.TrimPrefix(path, "/"), ".auto.tfvars")
			require.Regexp(t,
				`(?m)^`+key+`_project_id\s*=\s*"`+projectID+`"$`,
				s,
				"%s should bind <key>_project_id to the real GCP project ID", path)
			require.Regexp(t,
				`(?m)^`+key+`_project\s*=\s*"`+prefix+`"$`,
				s,
				"%s should bind <key>_project to the naming prefix", path)
		}
		require.GreaterOrEqual(t, gcpTfvarsCount, 1, "expected at least one gcp_* tfvars file")

		// Root variables.tf declares the namespaced project_id variable.
		rootVars, ok := out["/variables.tf"]
		require.True(t, ok)
		require.Contains(t, string(rootVars), `variable "gcp_vpc_project_id"`)

		// Module block in main.tf wires the namespaced root var into the
		// module's project_id input. This pins the namespacing pipeline
		// at compose.go: dropping the inputs[v.Name] = RawExpr{...} branch
		// for project_id would still emit tfvars (above) but break the
		// wiring.
		mainTF, ok := out["/main.tf"]
		require.True(t, ok)
		require.Regexp(t,
			`(?s)module "gcp_vpc"\s*\{[^}]*project_id\s*=\s*var\.gcp_vpc_project_id`,
			string(mainTF),
			"module gcp_vpc should reference var.gcp_vpc_project_id for its project_id input")
	})
}

// TestComposeSingle_GCP mirrors TestComposeStack_GCP for the single-module
// path. Both flows share maybeInjectGCPProjectID and the per-module
// ValidateGCPProjectID call but they are wired independently — a mutation
// that drops either site from composeSingleImpl would otherwise ship green.
func TestComposeSingle_GCP(t *testing.T) {
	t.Parallel()

	t.Run("empty GCPProjectID surfaces gcp_project_id_required", func(t *testing.T) {
		t.Parallel()
		c := newTestClient()
		res, err := c.ComposeSingleWithIssues(ComposeSingleOpts{
			Cloud:   "gcp",
			Key:     KeyGCPVPC,
			Comps:   &Components{},
			Cfg:     &Config{Region: "us-central1"},
			Project: "io-abc123def456",
			Region:  "us-central1",
		})
		require.NoError(t, err)

		codes := map[string]bool{}
		for _, iss := range res.Issues {
			codes[iss.Code] = true
		}
		require.True(t, codes["gcp_project_id_required"],
			"expected gcp_project_id_required issue from single-module path; got: %#v", res.Issues)
	})

	t.Run("valid GCPProjectID populates the namespaced tfvars", func(t *testing.T) {
		t.Parallel()
		const projectID = "single-real-12345"
		c := newTestClient()
		out, err := c.ComposeSingle(ComposeSingleOpts{
			Cloud:        "gcp",
			Key:          KeyGCPVPC,
			Comps:        &Components{},
			Cfg:          &Config{Region: "us-central1"},
			Project:      "io-stacknaming",
			GCPProjectID: projectID,
			Region:       "us-central1",
		})
		require.NoError(t, err, "ComposeSingle with valid GCPProjectID should succeed")

		tfvars, ok := out["/gcp_vpc.auto.tfvars"]
		require.True(t, ok, "single-module path should emit per-module tfvars")
		require.Regexp(t,
			`(?m)^gcp_vpc_project_id\s*=\s*"`+projectID+`"$`,
			string(tfvars))
	})
}

// TestValidateAll_PicksUpGCPOpts confirms the variadic seam on ValidateAll
// is wired so reliable's dry-run path (which goes through ValidateAll, not
// ComposeStackWithIssues) picks up the GCP project ID validation. Without
// the seam, the validator would only fire inside the compose flow and
// dry-run callers would miss the bug.
func TestValidateAll_PicksUpGCPOpts(t *testing.T) {
	t.Parallel()

	t.Run("no opts -> no GCP issue (preserves historical behaviour)", func(t *testing.T) {
		t.Parallel()
		out := ValidateAll(&Components{Cloud: "GCP"}, nil, nil, nil, nil, nil)
		for _, iss := range out {
			require.NotEqual(t, "gcp_project_id_required", iss.Code,
				"ValidateAll without opts must not emit GCP project ID issues — only ValidateOpts is responsible")
			require.NotEqual(t, "gcp_invalid_project_id", iss.Code)
		}
	})

	t.Run("opts with empty GCPProjectID surfaces the issue", func(t *testing.T) {
		t.Parallel()
		out := ValidateAll(nil, nil, nil, nil, nil, nil, ComposeStackOpts{
			Cloud: "gcp",
		})
		var found bool
		for _, iss := range out {
			if iss.Code == "gcp_project_id_required" {
				found = true
				break
			}
		}
		require.True(t, found, "expected gcp_project_id_required when ValidateAll is called with GCP opts and no project id; got: %#v", out)
	})

	t.Run("opts with valid GCPProjectID is silent on the GCP front", func(t *testing.T) {
		t.Parallel()
		out := ValidateAll(nil, nil, nil, nil, nil, nil, ComposeStackOpts{
			Cloud:        "gcp",
			GCPProjectID: "valid-prod-12345",
		})
		for _, iss := range out {
			require.NotEqual(t, "gcp_project_id_required", iss.Code)
			require.NotEqual(t, "gcp_invalid_project_id", iss.Code)
		}
	})
}
