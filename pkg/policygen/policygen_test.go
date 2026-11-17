package policygen

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
		assert.True(t, Allows(Request{Action: a, Resource: "*", Context: ctx}, res.Write),
			"preflight action %s is not allowed by %s", a, WriteFile)
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

// TestRoleEscalationDenied checks the hole in the old placeholder: a role
// created or changed without the workload boundary would let the deploy
// role grant itself anything.
func TestRoleEscalationDenied(t *testing.T) {
	res := generate(t)
	p := res.Report.Principals[0]
	withBoundary := preflightContext(p)
	noBoundary := preflightContext(p)
	delete(noBoundary, "iam:PermissionsBoundary")
	otherBoundary := preflightContext(p)
	otherBoundary["iam:PermissionsBoundary"] = "arn:aws:iam::aws:policy/AdministratorAccess"
	untagged := preflightContext(p)
	delete(untagged, "aws:RequestTag/Project")
	notProject := preflightContext(p)
	notProject["aws:ResourceTag/Project"] = "someone-else"
	otherService := preflightContext("iam.amazonaws.com")

	cases := []struct {
		name string
		req  Request
		want bool
	}{
		{"create role with boundary", Request{"iam:CreateRole", "*", withBoundary}, true},
		{"create role without boundary", Request{"iam:CreateRole", "*", noBoundary}, false},
		{"create role with another boundary", Request{"iam:CreateRole", "*", otherBoundary}, false},
		{"create role without Project tag", Request{"iam:CreateRole", "*", untagged}, false},
		{"attach policy without boundary", Request{"iam:AttachRolePolicy", "*", noBoundary}, false},
		{"put inline policy without boundary", Request{"iam:PutRolePolicy", "*", noBoundary}, false},
		{"pass a project role", Request{"iam:PassRole", "*", withBoundary}, true},
		{"pass another project's role", Request{"iam:PassRole", "*", notProject}, false},
		{"pass a role to a service no preset uses", Request{"iam:PassRole", "*", otherService}, false},
		{"remove a boundary", Request{"iam:DeleteRolePermissionsBoundary", "*", withBoundary}, false},
		{"edit another role with boundary", Request{"iam:AttachRolePolicy", "arn:aws:iam::111122223333:role/" + "x", withBoundary}, true},
		{"edit the deploy role", Request{"iam:AttachRolePolicy", PlaceholderDeployRoleARN, withBoundary}, false},
		{"edit the deploy policy", Request{"iam:CreatePolicyVersion", "arn:aws:iam::111122223333:policy/insideout-write", nil}, false},
		{"assume any role", Request{"sts:AssumeRole", "*", withBoundary}, false},
		{"iam wildcard", Request{"iam:CreateUser", "*", withBoundary}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, Allows(c.req, res.Write))
		})
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
