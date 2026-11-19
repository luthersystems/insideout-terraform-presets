package policygen

import (
	"bytes"
	"encoding/json"
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
var emptyComponents = map[composer.ComponentKey]string{}

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

// preflightContext is PreflightIAMContext as a map, with the template
// placeholders as values (the placeholders are what the policy compares
// against here). passedTo overrides iam:PassedToService when not empty.
func preflightContext(passedTo string) map[string]string {
	ctx := map[string]string{}
	for _, e := range PreflightIAMContext(PlaceholderProject, PlaceholderWorkloadBoundaryARN) {
		ctx[e.Key] = e.Values[0]
	}
	if passedTo != "" {
		ctx["iam:PassedToService"] = passedTo
	}
	return ctx
}

// TestPreflightActionsAllowed is the other issue #147 rule: the preflight
// must never check an action the policy does not allow, or every scoped
// deploy fails before it starts.
func TestPreflightActionsAllowed(t *testing.T) {
	res := generate(t)
	ctx := preflightContext("")
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
	extra := map[string]bool{}
	for _, a := range r.ExtraActions {
		extra[a] = true
	}
	for k, actions := range composer.AWSIAMActions {
		for _, a := range actions {
			svc, _, _ := strings.Cut(a, ":")
			assert.True(t, deploy[svc] || extra[a], "%s: seed action %s names service %s, which the scan does not find, and is not a typeExtraActions action", k, a, svc)
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
		{"detach a customer role's policy", Request{"iam:DetachRolePolicy", customerRole, with("aws:ResourceTag/Project", "customer")}, false},
		{"retrust a customer role", Request{"iam:UpdateAssumeRolePolicy", customerRole, ctx}, false},
		{"retrust another project's role", Request{"iam:UpdateAssumeRolePolicy", otherProjectRole, ctx}, false},
		{"tag a project role on create", Request{"iam:TagRole", role, without("aws:ResourceTag/Project")}, true},
		{"pass a project role", Request{"iam:PassRole", role, ctx}, true},
		{"pass a customer role", Request{"iam:PassRole", customerRole, ctx}, false},
		{"pass a role to a service no preset uses", Request{"iam:PassRole", role, with("iam:PassedToService", "iam.amazonaws.com")}, false},
		// sts.amazonaws.com is the GitHub OIDC audience in a trust policy,
		// not a service that takes a role.
		{"pass a role to STS", Request{"iam:PassRole", role, with("iam:PassedToService", "sts.amazonaws.com")}, false},
		{"remove a boundary", Request{"iam:DeleteRolePermissionsBoundary", role, ctx}, false},
		{"edit the deploy role", Request{"iam:AttachRolePolicy", PlaceholderDeployRoleARN, ctx}, false},
		{"edit the workload boundary", Request{"iam:CreatePolicyVersion", PlaceholderWorkloadBoundaryARN, nil}, false},
		{"edit the deploy policy", Request{"iam:CreatePolicyVersion", PlaceholderDeployPolicyARN, nil}, false},
		{"tear down a legacy project role", Request{"iam:DeleteRole", "arn:aws:iam::111122223333:role/app", ctx}, true},
		{"tear down a customer role", Request{"iam:DeleteRole", customerRole, with("aws:ResourceTag/Project", "customer")}, false},
		{"detach a legacy project role's policy", Request{"iam:DetachRolePolicy", "arn:aws:iam::111122223333:role/app", ctx}, true},
		{"attach a policy to a legacy project role", Request{"iam:AttachRolePolicy", "arn:aws:iam::111122223333:role/app", ctx}, false},
		{"tag a customer role with the project", Request{"iam:TagRole", customerRole, without("aws:ResourceTag/Project")}, false},
		{"write a project policy", Request{"iam:CreatePolicyVersion", policy, nil}, true},
		{"rewrite a customer policy", Request{"iam:CreatePolicyVersion", customerPolicy, nil}, false},
		{"delete the customer's GitHub OIDC provider", Request{"iam:DeleteOpenIDConnectProvider", githubOIDC, nil}, false},
		// aws/githubactions with create_oidc_provider (#936).
		{"create the GitHub OIDC provider", Request{"iam:CreateOpenIDConnectProvider", githubOIDC, ctx}, true},
		{"create the GitHub OIDC provider untagged", Request{"iam:CreateOpenIDConnectProvider", githubOIDC, nil}, false},
		{"delete the project's GitHub OIDC provider", Request{"iam:DeleteOpenIDConnectProvider", githubOIDC, ctx}, true},
		{"retag another project's GitHub OIDC provider", Request{"iam:TagOpenIDConnectProvider", githubOIDC, with("aws:ResourceTag/Project", "other")}, false},
		{"create another issuer's OIDC provider", Request{"iam:CreateOpenIDConnectProvider", "arn:aws:iam::111122223333:oidc-provider/accounts.google.com", ctx}, false},
		// aws/grafana with IAM Identity Center login (#936).
		{"register Grafana with Identity Center", Request{"sso:CreateManagedApplicationInstance", "*", nil}, true},
		{"read the organization for Grafana", Request{"organizations:DescribeOrganization", "*", nil}, true},
		{"leave the organization", Request{"organizations:LeaveOrganization", "*", nil}, false},
		{"create an Identity Center user", Request{"identitystore:CreateUser", "*", nil}, false},
		{"create the EKS OIDC provider", Request{"iam:CreateOpenIDConnectProvider", eksOIDC, ctx}, true},
		{"create the EKS OIDC provider untagged", Request{"iam:CreateOpenIDConnectProvider", eksOIDC, nil}, false},
		{"tag a new EKS OIDC provider", Request{"iam:TagOpenIDConnectProvider", eksOIDC, without("aws:ResourceTag/Project")}, true},
		{"retag another project's EKS OIDC provider", Request{"iam:TagOpenIDConnectProvider", eksOIDC, with("aws:ResourceTag/Project", "other")}, false},
		{"delete the project's EKS OIDC provider", Request{"iam:DeleteOpenIDConnectProvider", eksOIDC, ctx}, true},
		{"delete a customer's EKS OIDC provider", Request{"iam:DeleteOpenIDConnectProvider", eksOIDC, with("aws:ResourceTag/Project", "customer")}, false},
		{"create a service-linked role", Request{"iam:CreateServiceLinkedRole", "arn:aws:iam::111122223333:role/aws-service-role/eks.amazonaws.com/AWSServiceRoleForAmazonEKS", nil}, true},
		{"delete a preset's service-linked role", Request{"iam:DeleteServiceLinkedRole", "arn:aws:iam::111122223333:role/aws-service-role/kafka.amazonaws.com/AWSServiceRoleForKafka", nil}, true},
		{"delete another service-linked role", Request{"iam:DeleteServiceLinkedRole", "arn:aws:iam::111122223333:role/aws-service-role/eks.amazonaws.com/AWSServiceRoleForAmazonEKS", nil}, false},
		{"create a project role in GovCloud", Request{"iam:CreateRole", "arn:aws-us-gov:iam::111122223333:role" + path + "app", ctx}, true},
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
		"iam:CreateRole":                  "arn:*:iam::*:role/insideout/p/preflight",
		"iam:AttachRolePolicy":            "arn:*:iam::*:role/insideout/p/preflight",
		"iam:PassRole":                    "arn:*:iam::*:role/insideout/p/preflight",
		"iam:CreatePolicy":                "arn:*:iam::*:policy/insideout/p/preflight",
		"iam:CreateInstanceProfile":       "arn:*:iam::*:instance-profile/insideout/p/preflight",
		"iam:CreateOpenIDConnectProvider": "arn:*:iam::*:oidc-provider/oidc.eks.preflight.amazonaws.com/id/preflight",
		"iam:CreateServiceLinkedRole":     "arn:*:iam::*:role/aws-service-role/preflight.amazonaws.com/preflight",
		"iam:DeleteServiceLinkedRole":     "arn:*:iam::*:role/aws-service-role/preflight.amazonaws.com/preflight",
		"iam:GetRole":                     "*",
		"iam:GetPolicy":                   "*",
		"iam:ListInstanceProfiles":        "*",
		"s3:CreateBucket":                 "*",
		"s3:PutBucketPolicy":              "*",
		"ec2:AssociateIamInstanceProfile": "*",
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

// TestPolicySize checks the size IAM sees: the template fills the
// placeholders before it creates the policy, and the values are longer than
// the placeholders. Measured with placeholders only, the write policy was
// 6,056 characters and passed, but 6,568 with long values, over the limit.
func TestPolicySize(t *testing.T) {
	res := generate(t)
	for name, d := range map[string]Document{WriteFile: res.Write, BoundaryFile: res.Boundary} {
		assert.LessOrEqual(t, WorstCaseSize(d), ManagedPolicyLimit, name)
		assert.GreaterOrEqual(t, WorstCaseSize(d), MinifiedSize(d), name)
	}
}

// TestWorstCaseValuesFillEveryPlaceholder keeps WorstCaseValues in step
// with the placeholders, so a new placeholder is measured too.
func TestWorstCaseValuesFillEveryPlaceholder(t *testing.T) {
	res := generate(t)
	for name, d := range map[string]Document{WriteFile: res.Write, BoundaryFile: res.Boundary} {
		b, err := json.Marshal(d)
		require.NoError(t, err)
		assert.NotContains(t, Substitute(string(b), WorstCaseValues), "${", name)
	}
}

// TestStatementNamesInReport: the policy has no Sid (IAM counts each one
// against the size limit), so the report names the statements in order.
func TestStatementNamesInReport(t *testing.T) {
	res := generate(t)
	require.Len(t, res.Report.WriteStatements, len(res.Write.Statement))
	assert.Equal(t, "StackServices", res.Report.WriteStatements[0])
	for _, st := range res.Write.Statement {
		assert.Empty(t, st.Sid)
	}
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

	null := Document{Statement: []Statement{{Effect: "Allow", Action: []string{"iam:TagRole"}, Resource: star,
		Condition: map[string]map[string]any{"Null": {"aws:ResourceTag/Project": "true"}}}}}
	assert.True(t, Allows(Request{"iam:TagRole", "*", nil}, null), "Null true matches a missing key")
	assert.False(t, Allows(Request{"iam:TagRole", "*", map[string]string{"aws:ResourceTag/Project": "p"}}, null))
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
	assert.Equal(t, []string{"m/aws_iam_role.b", "m/aws_iam_role.r"}, s.IAMWithoutPath)
}

// offInPresets are IAM objects of a registry module that take no path but
// that the preset never creates (count 0 with the inputs the preset passes).
// The list may only shrink. If a preset turns one on, the scoped policy
// refuses it, and the row must go with a path for it.
var offInPresets = map[string]string{
	"aws_eks ./aws_iam_policy.cni_ipv6_policy":         "needs an IPv6 cluster; aws/eks sets no ip_family",
	"aws_vpc ./aws_iam_policy.vpc_flow_log_cloudwatch": "needs flow logs; aws/vpc sets no enable_flow_log",
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
		var unpathed []string
		for _, o := range c.IAMWithoutPath {
			if _, off := offInPresets[string(k)+" "+o]; !off {
				unpathed = append(unpathed, o)
			}
		}
		assert.Empty(t, unpathed, "%s: IAM objects with no path", k)
	}
	for row := range offInPresets {
		k, o, _ := strings.Cut(row, " ")
		assert.Contains(t, r.Components[composer.ComponentKey(k)].IAMWithoutPath, o, "offInPresets row %q is stale; delete it", row)
	}

	// The scan only sees that the argument is set. Check that each preset
	// sets it from its input, so the template can pass the values in.
	want := map[string]string{"path": "var.iam_scope.path", "permissions_boundary": "var.iam_scope.permissions_boundary"}
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
		"iam_role_path":                      "var.iam_scope.path",
		"iam_role_permissions_boundary":      "var.iam_scope.permissions_boundary",
		"encryption_policy_path":             "var.iam_scope.path",
		"node_iam_role_path":                 "var.iam_scope.path",
		"node_iam_role_permissions_boundary": "var.iam_scope.permissions_boundary",
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
				assert.Contains(t, string(src[attr.Expr.Range().Start.Byte:attr.Expr.Range().End.Byte]), ref, arg)
			}
		}
	}
	assert.True(t, found, `aws/eks/main.tf has no module "eks"`)
}

// TestPreflightPassedToServiceInPolicy: the scoped policy allows
// iam:PassRole only to the services it lists, so the preflight's
// iam:PassedToService must be one of them.
func TestPreflightPassedToServiceInPolicy(t *testing.T) {
	res := generate(t)
	assert.Contains(t, res.Report.Principals, PreflightPassedToService)
}

// TestPreflightIAMChecks: every action with a project resource is in
// exactly one check, on its PreflightResource; actions on "*" are in none.
func TestPreflightIAMChecks(t *testing.T) {
	actions := []string{"iam:CreateRole", "iam:PassRole", "iam:CreatePolicy", "iam:GetRole", "s3:CreateBucket", "iam:CreateRole"}
	got := PreflightIAMChecks(actions, "p")
	assert.Equal(t, []IAMResourceCheck{
		{Actions: []string{"iam:CreatePolicy"}, ResourceARNs: []string{"arn:*:iam::*:policy/insideout/p/preflight"}},
		{Actions: []string{"iam:CreateRole", "iam:PassRole"}, ResourceARNs: []string{"arn:*:iam::*:role/insideout/p/preflight"}},
	}, got)
	assert.Empty(t, PreflightIAMChecks([]string{"s3:CreateBucket"}, "p"))

	b, err := json.Marshal(got[0])
	require.NoError(t, err)
	assert.JSONEq(t, `{"actions":["iam:CreatePolicy"],"resourceArns":["arn:*:iam::*:policy/insideout/p/preflight"]}`, string(b))
}

// TestPreflightChecksAllowedFilled runs the real payload (checks and
// context, with filled values) against the filled policy, for every action
// the presets need: a scoped deploy must pass its own preflight.
func TestPreflightChecksAllowedFilled(t *testing.T) {
	res := generate(t)
	const project = "io-abc123"
	const prefix = "arn:aws:iam::111122223333:"
	boundary := prefix + "policy/" + WorkloadBoundaryName(project)
	raw, err := Marshal(res.Write)
	require.NoError(t, err)
	var filled Document
	require.NoError(t, json.Unmarshal([]byte(Substitute(string(raw), map[string]string{
		PlaceholderProject:             project,
		PlaceholderWorkloadBoundaryARN: boundary,
		PlaceholderDeployRoleARN:       prefix + "role/abcd-luther-terraform",
		PlaceholderDeployPolicyARN:     prefix + "policy/abcd-luther-terraform-deploy",
	})), &filled))

	// ui-core pins "*" in the partition and account of resource ARNs and
	// ARN context values to the simulated principal's.
	pin := func(arn string) string { return strings.Replace(arn, "arn:*:iam::*:", prefix, 1) }
	ctx := map[string]string{}
	for _, e := range PreflightIAMContext(project, WorkloadBoundaryPreflightARN(project)) {
		ctx[e.Key] = pin(e.Values[0])
	}
	actions := composer.RequiredAWSIAMActions(AWSComponents())
	checked := map[string]bool{}
	for _, c := range PreflightIAMChecks(actions, project) {
		for _, a := range c.Actions {
			checked[a] = true
			for _, r := range c.ResourceARNs {
				assert.True(t, Allows(Request{Action: a, Resource: pin(r), Context: ctx}, filled), "%s on %s", a, pin(r))
			}
		}
	}
	for _, a := range actions {
		if !checked[a] {
			assert.True(t, Allows(Request{Action: a, Resource: "*", Context: ctx}, filled), "%s on *", a)
		}
	}
}
