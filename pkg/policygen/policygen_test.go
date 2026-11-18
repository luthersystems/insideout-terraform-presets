package policygen

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emptyComponents are AWS components whose preset creates nothing, so the
// scan finds no services for them. The list may only shrink: a new entry
// means a component the policy would silently not cover. Each row names the
// issue that removes it.
var emptyComponents = map[composer.ComponentKey]string{
	composer.KeyAWSCodePipeline:  "#936",
	composer.KeyAWSGitHubActions: "#936",
	composer.KeyAWSGrafana:       "#936",
}

// committedDir is where `go run ./cmd/policygen` writes, relative to this
// package.
var committedDir = filepath.Join("..", "..", "policies", "aws")

func defaultOpts() Options {
	return Options{Presets: terraformpresets.FS, IncludeImported: true}
}

func generate(t *testing.T) Result {
	t.Helper()
	res, err := Generate(defaultOpts())
	require.NoError(t, err)
	return res
}

// TestEveryTypeMapped fails when a preset, a pinned registry module or an
// import type uses an aws_* type with no row in services.go. Without the
// row the type's service would be missing from the policy.
func TestEveryTypeMapped(t *testing.T) {
	r, err := Scan(defaultOpts())
	require.NoError(t, err)
	assert.Empty(t, r.UnmappedTypes, "add a typePrefixService or noAPITypes row in services.go")
	assert.Empty(t, r.UnmappedManaged, "add a managedPolicyServices row in managed.go")
	assert.Empty(t, r.MissingRegistry, "run `go run ./cmd/policygen registry`")
	assert.Empty(t, r.UnexpectedRemote, "a remote module the generator can not scan")
}

// TestEveryComponentHasServices is the issue #147 rule: a component with no
// actions in the policy would fail at apply once the scoped policy is on.
func TestEveryComponentHasServices(t *testing.T) {
	r, err := Scan(defaultOpts())
	require.NoError(t, err)
	for _, k := range AWSComponents() {
		c, ok := r.Components[k]
		require.True(t, ok, "component %s not scanned", k)
		_, allowed := emptyComponents[k]
		if allowed {
			assert.Empty(t, c.Services, "%s now has services; delete its emptyComponents row", k)
			continue
		}
		assert.NotEmpty(t, c.Services, "%s (%s): no AWS services found", k, c.Preset)
	}
	for k := range emptyComponents {
		assert.Equal(t, "aws", composer.CloudFor(k), "emptyComponents row %s is not an AWS component", k)
	}
}

// preflightContext is what the preflight must send as SimulatePrincipalPolicy
// ContextEntries for the scoped policy, with the template placeholders as
// values (the placeholders are what the policy compares against here).
func preflightContext(passedTo string) map[string]string {
	return map[string]string{
		"iam:PermissionsBoundary": PlaceholderWorkloadBoundaryARN,
		"aws:ResourceTag/Project": PlaceholderProject,
		"aws:RequestTag/Project":  PlaceholderProject,
		"iam:PassedToService":     passedTo,
	}
}

// TestPreflightActionsAllowed is the other issue #147 rule: the preflight
// must never check an action the policy does not allow, or every scoped
// deploy fails before it starts.
func TestPreflightActionsAllowed(t *testing.T) {
	res := generate(t)
	require.NotEmpty(t, res.Report.Principals)
	ctx := preflightContext(res.Report.Principals[0])
	for _, a := range composer.RequiredAWSIAMActions(AWSComponents()) {
		res0 := PreflightResource(a, PlaceholderProject)
		assert.True(t, Allows(Request{Action: a, Resource: res0, Context: ctx}, res.Write),
			"preflight action %s on %s is not allowed by %s", a, res0, WriteFile)
	}
}

// TestPreflightServicesScanned fails when the preflight seed names a service
// no preset uses: the seed is stale, or the scan misses a type.
func TestPreflightServicesScanned(t *testing.T) {
	r, err := Scan(defaultOpts())
	require.NoError(t, err)
	deploy := map[string]bool{}
	for _, s := range r.DeployServices {
		deploy[s] = true
	}
	for k, actions := range composer.AWSIAMActions {
		for _, a := range actions {
			svc, _, _ := strings.Cut(a, ":")
			assert.True(t, deploy[svc], "%s: seed action %s names service %s, which the scan does not find", k, a, svc)
		}
	}
}

// TestRoleEscalationDenied checks the holes in the old placeholder and the
// ones the review of #937 found: a role created or changed without the
// workload boundary, and IAM writes on the customer's own roles, policies
// and OIDC providers.
func TestRoleEscalationDenied(t *testing.T) {
	res := generate(t)
	p := res.Report.Principals[0]
	ctx := preflightContext(p)
	without := func(k string) map[string]string {
		c := preflightContext(p)
		delete(c, k)
		return c
	}
	with := func(k, v string) map[string]string {
		c := preflightContext(p)
		c[k] = v
		return c
	}
	path := IAMPath(PlaceholderProject)
	role := "arn:aws:iam::111122223333:role" + path + "app"
	policy := "arn:aws:iam::111122223333:policy" + path + "app"
	customerRole := "arn:aws:iam::111122223333:role/customer-admin"
	otherProjectRole := "arn:aws:iam::111122223333:role/insideout/other/app"
	customerPolicy := "arn:aws:iam::111122223333:policy/customer-ci"
	githubOIDC := "arn:aws:iam::111122223333:oidc-provider/token.actions.githubusercontent.com"
	eksOIDC := "arn:aws:iam::111122223333:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/ABC"

	cases := []struct {
		name string
		req  Request
		want bool
	}{
		{"create project role with boundary", Request{"iam:CreateRole", role, ctx}, true},
		{"create project role without boundary", Request{"iam:CreateRole", role, without("iam:PermissionsBoundary")}, false},
		{"create project role with another boundary", Request{"iam:CreateRole", role, with("iam:PermissionsBoundary", "arn:aws:iam::aws:policy/AdministratorAccess")}, false},
		{"create project role without Project tag", Request{"iam:CreateRole", role, without("aws:RequestTag/Project")}, false},
		{"create role off the project path", Request{"iam:CreateRole", customerRole, ctx}, false},
		{"attach policy without boundary", Request{"iam:AttachRolePolicy", role, without("iam:PermissionsBoundary")}, false},
		{"put inline policy without boundary", Request{"iam:PutRolePolicy", role, without("iam:PermissionsBoundary")}, false},
		{"attach policy to a project role", Request{"iam:AttachRolePolicy", role, ctx}, true},
		{"give a customer role the boundary", Request{"iam:PutRolePermissionsBoundary", customerRole, ctx}, false},
		{"detach a customer role's policy", Request{"iam:DetachRolePolicy", customerRole, ctx}, false},
		{"retrust a customer role", Request{"iam:UpdateAssumeRolePolicy", customerRole, ctx}, false},
		{"retrust another project's role", Request{"iam:UpdateAssumeRolePolicy", otherProjectRole, ctx}, false},
		{"tag a project role on create", Request{"iam:TagRole", role, without("aws:ResourceTag/Project")}, true},
		{"pass a project role", Request{"iam:PassRole", role, ctx}, true},
		{"pass a customer role", Request{"iam:PassRole", customerRole, ctx}, false},
		{"pass a role to a service no preset uses", Request{"iam:PassRole", role, with("iam:PassedToService", "iam.amazonaws.com")}, false},
		{"remove a boundary", Request{"iam:DeleteRolePermissionsBoundary", role, ctx}, false},
		{"edit the deploy role", Request{"iam:AttachRolePolicy", PlaceholderDeployRoleARN, ctx}, false},
		{"edit the workload boundary", Request{"iam:CreatePolicyVersion", PlaceholderWorkloadBoundaryARN, nil}, false},
		{"write a project policy", Request{"iam:CreatePolicyVersion", policy, nil}, true},
		{"rewrite a customer policy", Request{"iam:CreatePolicyVersion", customerPolicy, nil}, false},
		{"delete the customer's GitHub OIDC provider", Request{"iam:DeleteOpenIDConnectProvider", githubOIDC, nil}, false},
		{"create the EKS OIDC provider", Request{"iam:CreateOpenIDConnectProvider", eksOIDC, nil}, true},
		{"create a service-linked role", Request{"iam:CreateServiceLinkedRole", "arn:aws:iam::111122223333:role/aws-service-role/es.amazonaws.com/AWSServiceRoleForAmazonOpenSearchService", nil}, true},
		{"assume any role", Request{"sts:AssumeRole", "*", ctx}, false},
		{"create a user", Request{"iam:CreateUser", "*", ctx}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, Allows(c.req, res.Write))
		})
	}
}

// TestPreflightResource pins the ARN the preflight simulates for each kind
// of IAM action.
func TestPreflightResource(t *testing.T) {
	cases := map[string]string{
		"iam:CreateRole":                  "arn:aws:iam::*:role/insideout/p/preflight",
		"iam:AttachRolePolicy":            "arn:aws:iam::*:role/insideout/p/preflight",
		"iam:PassRole":                    "arn:aws:iam::*:role/insideout/p/preflight",
		"iam:CreatePolicy":                "arn:aws:iam::*:policy/insideout/p/preflight",
		"iam:CreateInstanceProfile":       "arn:aws:iam::*:instance-profile/insideout/p/preflight",
		"iam:CreateOpenIDConnectProvider": "arn:aws:iam::*:oidc-provider/oidc.eks.preflight.amazonaws.com/id/preflight",
		"iam:CreateServiceLinkedRole":     "arn:aws:iam::*:role/aws-service-role/preflight",
		"iam:GetRole":                     "*",
		"s3:CreateBucket":                 "*",
	}
	for a, want := range cases {
		assert.Equal(t, want, PreflightResource(a, "p"), a)
	}
}

// TestBoundaryCoversManagedPolicies: a role keeps what its AWS managed
// policies grant only when the boundary allows those services too.
func TestBoundaryCoversManagedPolicies(t *testing.T) {
	res := generate(t)
	for _, m := range res.Report.ManagedPolicies {
		for _, svc := range managedPolicyServices[m] {
			assert.True(t, Allows(Request{Action: svc + ":SomeAction", Resource: "*"}, res.Boundary),
				"boundary does not allow %s, which managed policy %s grants", svc, m)
		}
	}
	for _, svc := range res.Report.DeployServices {
		if svc == "iam" || svc == "sts" {
			continue
		}
		assert.True(t, Allows(Request{Action: svc + ":SomeAction", Resource: "*"}, res.Boundary),
			"boundary does not allow stack service %s", svc)
	}
	assert.False(t, Allows(Request{Action: "iam:CreateRole", Resource: "*"}, res.Boundary),
		"a workload role must not create roles")
}

// TestNoUnusedManagedRows keeps managed.go free of rows no preset uses,
// so the table stays a list of what the presets attach.
func TestNoUnusedManagedRows(t *testing.T) {
	r, err := Scan(defaultOpts())
	require.NoError(t, err)
	used := map[string]bool{}
	for _, m := range r.ManagedPolicies {
		used[m] = true
	}
	for m := range managedPolicyServices {
		assert.True(t, used[m], "managed.go row %s: no preset attaches it; delete the row", m)
	}
}

func TestPolicySize(t *testing.T) {
	res := generate(t)
	assert.LessOrEqual(t, MinifiedSize(res.Write), ManagedPolicyLimit)
	assert.LessOrEqual(t, MinifiedSize(res.Boundary), ManagedPolicyLimit)
}

// TestCommittedPoliciesCurrent fails when policies/aws differs from the
// generator output, so a preset change that changes the policy shows in the
// same PR's diff.
func TestCommittedPoliciesCurrent(t *testing.T) {
	files, err := Files(generate(t))
	require.NoError(t, err)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		cur, err := os.ReadFile(filepath.Join(committedDir, n))
		require.NoError(t, err, "run `go run ./cmd/policygen`")
		assert.True(t, bytes.Equal(cur, files[n]), "%s is out of date; run `go run ./cmd/policygen`", n)
	}
}

func TestServiceForType(t *testing.T) {
	cases := map[string]struct {
		svc   string
		needs bool
		ok    bool
	}{
		"aws_lb_listener":                 {"elasticloadbalancing", true, true},
		"aws_cloudwatch_log_group":        {"logs", true, true},
		"aws_cloudwatch_metric_alarm":     {"cloudwatch", true, true},
		"aws_route_table":                 {"ec2", true, true},
		"aws_iam_policy_document":         {"", false, true},
		"aws_sfn_state_machine":           {"states", true, true},
		"aws_no_such_service_thing_xyzzy": {"", false, false},
	}
	for typ, want := range cases {
		svc, needs, ok := ServiceForType(typ)
		assert.Equal(t, want.ok, ok, typ)
		if ok {
			assert.Equal(t, want.svc, svc, typ)
			assert.Equal(t, want.needs, needs, typ)
		}
	}
}

func TestAllowsConditions(t *testing.T) {
	doc := Document{Statement: []Statement{
		{Effect: "Allow", Action: []string{"s3:*"}, Resource: star},
		{Effect: "Deny", Action: []string{"s3:DeleteBucket"}, Resource: star,
			Condition: map[string]map[string]any{"StringNotEquals": {"aws:ResourceTag/Project": "p"}}},
		{Effect: "Allow", Action: []string{"ec2:RunInstances"}, Resource: star,
			Condition: map[string]map[string]any{"StringEquals": {"ec2:Region": []string{"a", "b"}}}},
		{Effect: "Allow", Action: []string{"kms:Encrypt"}, Resource: star,
			Condition: map[string]map[string]any{"ArnLike": {"kms:x": "y"}}},
	}}
	assert.True(t, Allows(Request{"s3:GetObject", "*", nil}, doc))
	assert.False(t, Allows(Request{"s3:DeleteBucket", "*", nil}, doc), "missing key makes StringNotEquals deny")
	assert.True(t, Allows(Request{"s3:DeleteBucket", "*", map[string]string{"aws:ResourceTag/Project": "p"}}, doc))
	assert.True(t, Allows(Request{"ec2:RunInstances", "*", map[string]string{"ec2:Region": "b"}}, doc))
	assert.False(t, Allows(Request{"ec2:RunInstances", "*", map[string]string{"ec2:Region": "c"}}, doc))
	assert.False(t, Allows(Request{"kms:Encrypt", "*", map[string]string{"kms:x": "y"}}, doc), "unknown operator never matches")
	assert.False(t, Allows(Request{"sqs:SendMessage", "*", nil}, doc))
}

// TestScanTrustAcrossFiles: a role's trust document may live in another file
// of the module; its principal must still count, or PassRole to that service
// would be denied.
func TestScanTrustAcrossFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"m/main.tf": {Data: []byte(`
resource "aws_iam_role" "r" {
  assume_role_policy = data.aws_iam_policy_document.trust.json
}
resource "aws_iam_role" "b" {
  assume_role_policy   = "{}"
  permissions_boundary = var.boundary
}
`)},
		"m/data.tf": {Data: []byte(`
data "aws_iam_policy_document" "trust" {
  statement {
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}
`)},
	}
	s, err := ScanModule(fsys, "m")
	require.NoError(t, err)
	assert.Equal(t, []string{"lambda.amazonaws.com"}, s.Principals)
	assert.Equal(t, []string{"m/r"}, s.RolesWithoutBoundary)
}

// TestIAMTakesProjectPathAndBoundary is the step 2 rule of #147: the scoped
// deploy policy writes IAM only under the project's path and creates roles
// only with the workload boundary, so every IAM object a preset makes must
// take both from the iam_path and iam_permissions_boundary inputs.
func TestIAMTakesProjectPathAndBoundary(t *testing.T) {
	r, err := Scan(defaultOpts())
	require.NoError(t, err)
	for k, c := range r.Components {
		assert.Empty(t, c.RolesWithoutBoundary, "%s: roles with no permissions_boundary", k)
		assert.Empty(t, c.IAMWithoutPath, "%s: IAM objects with no path", k)
	}

	// The scan only sees that the argument is set. Check that each preset
	// sets it from its input, so the template can pass the values in.
	want := map[string]string{"path": "var.iam_path", "permissions_boundary": "var.iam_permissions_boundary"}
	files, err := fs.Glob(terraformpresets.FS, "aws/*/*.tf")
	require.NoError(t, err)
	for _, f := range files {
		src, err := fs.ReadFile(terraformpresets.FS, f)
		require.NoError(t, err)
		hf, diags := hclsyntax.ParseConfig(src, f, hcl.InitialPos)
		require.False(t, diags.HasErrors(), diags.Error())
		for _, b := range hf.Body.(*hclsyntax.Body).Blocks {
			if b.Type != "resource" || len(b.Labels) < 2 || !iamPathTypes[b.Labels[0]] {
				continue
			}
			for arg, ref := range want {
				if arg == "permissions_boundary" && b.Labels[0] != "aws_iam_role" {
					continue
				}
				attr, ok := b.Body.Attributes[arg]
				if !assert.True(t, ok, "%s %s.%s: no %s", f, b.Labels[0], b.Labels[1], arg) {
					continue
				}
				text := string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte])
				assert.Contains(t, text, ref, "%s %s.%s: %s must come from %s", f, b.Labels[0], b.Labels[1], arg, ref)
			}
		}
	}
}

// TestEKSModuleTakesProjectPathAndBoundary: the eks registry module makes the
// cluster role, the Auto Mode node role and the encryption policy, so the
// preset must hand it the path and the boundary.
func TestEKSModuleTakesProjectPathAndBoundary(t *testing.T) {
	src, err := fs.ReadFile(terraformpresets.FS, "aws/eks/main.tf")
	require.NoError(t, err)
	hf, diags := hclsyntax.ParseConfig(src, "aws/eks/main.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), diags.Error())
	want := map[string]string{
		"iam_role_path":                      "var.iam_path",
		"iam_role_permissions_boundary":      "var.iam_permissions_boundary",
		"encryption_policy_path":             "var.iam_path",
		"node_iam_role_path":                 "var.iam_path",
		"node_iam_role_permissions_boundary": "var.iam_permissions_boundary",
	}
	found := false
	for _, b := range hf.Body.(*hclsyntax.Body).Blocks {
		if b.Type != "module" || len(b.Labels) == 0 || b.Labels[0] != "eks" {
			continue
		}
		found = true
		for arg, ref := range want {
			attr, ok := b.Body.Attributes[arg]
			if assert.True(t, ok, "module.eks: no %s", arg) {
				assert.Equal(t, ref, string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte]), arg)
			}
		}
	}
	assert.True(t, found, `aws/eks/main.tf has no module "eks"`)
}
