package policygen

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// Placeholders the template fills with templatefile() when it creates the
// policies. They are Terraform template syntax on purpose: the generated JSON
// is account-independent, and the template knows these values only at apply.
const (
	// PlaceholderWorkloadBoundaryARN is the ARN of the workload boundary
	// policy (insideout-workload-boundary.json once created).
	PlaceholderWorkloadBoundaryARN = "${workload_boundary_arn}"
	// PlaceholderProject is the project's Project tag value, also the
	// project's segment of IAMPath.
	PlaceholderProject = "${project}"
	// PlaceholderDeployRoleARN is the ARN of the role the deploy policy is
	// attached to.
	PlaceholderDeployRoleARN = "${deploy_role_arn}"
)

// Document is an IAM policy document.
type Document struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}

// Statement is one IAM policy statement. Condition maps an operator
// (StringEquals) to a key to one value or a list of values.
type Statement struct {
	Sid       string                    `json:"Sid"`
	Effect    string                    `json:"Effect"`
	Action    []string                  `json:"Action"`
	Resource  []string                  `json:"Resource"`
	Condition map[string]map[string]any `json:"Condition,omitempty"`
}

// Options select the generator's inputs.
type Options struct {
	// Presets is the preset tree (terraformpresets.FS in production).
	Presets fs.FS
	// IncludeImported adds the services of every reverse-import type, since
	// import manages resources through the same deploy role (#147, Q3).
	IncludeImported bool
	// ExtraServices are services a caller adds for Terraform outside the
	// presets: the template's platform stack (vm-provision, #147 Q2).
	ExtraServices []string
}

// ComponentReport is one AWS component's scan, mapped to services.
type ComponentReport struct {
	Preset   string   `json:"preset"`
	Services []string `json:"services"`
	// RolesWithoutBoundary must take the workload boundary before the
	// scoped policy can deploy this component (#147 step 2).
	RolesWithoutBoundary []string `json:"roles_without_boundary,omitempty"`
	// IAMWithoutPath must take the project's IAM path, for the same reason.
	IAMWithoutPath []string `json:"iam_without_path,omitempty"`
}

// Report is the generator's full input after mapping, kept beside the
// policies so a reviewer sees why each service is in them.
type Report struct {
	Components      map[composer.ComponentKey]ComponentReport `json:"components"`
	ImportServices  []string                                  `json:"import_services,omitempty"`
	ExtraServices   []string                                  `json:"extra_services,omitempty"`
	DeployServices  []string                                  `json:"deploy_services"`
	BoundaryExtra   []string                                  `json:"boundary_extra_services"`
	Principals      []string                                  `json:"pass_role_principals"`
	ManagedPolicies []string                                  `json:"managed_policies"`
	// Unmapped lists what the tables do not cover; Generate fails when it
	// is not empty, and the tests name each item.
	UnmappedTypes    []string `json:"-"`
	UnmappedManaged  []string `json:"-"`
	MissingRegistry  []string `json:"-"`
	UnexpectedRemote []string `json:"-"`
}

// Result is the generator output.
type Result struct {
	Write    Document
	Boundary Document
	Report   Report
}

// allowedRemoteSources are non-registry module sources the presets may call
// because they create no AWS resource. Any other remote source fails, since
// the generator can not see what it creates.
var allowedRemoteSources = map[string]bool{
	"github.com/luthersystems/tf-modules.git//luthername": true,
}

// AWSComponents returns the AWS component keys, sorted.
func AWSComponents() []composer.ComponentKey {
	var out []composer.ComponentKey
	for _, k := range composer.AllComponentKeys {
		if composer.CloudFor(k) == "aws" {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Scan maps every AWS preset to services, without building policies. It
// returns the report even when something is unmapped, for the tests.
func Scan(opts Options) (Report, error) {
	reg, err := RegistryScans()
	if err != nil {
		return Report{}, err
	}
	r := Report{Components: map[composer.ComponentKey]ComponentReport{}}
	deploy, principals, managed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	unmappedTypes, unmappedManaged, missingReg, badRemote := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}

	addTypes := func(types []string, into map[string]bool) {
		for _, t := range types {
			svc, needs, ok := ServiceForType(t)
			switch {
			case !ok:
				unmappedTypes[t] = true
			case needs:
				into[svc] = true
			}
		}
	}

	for _, k := range AWSComponents() {
		dir := composer.GetPresetPath("aws", k, nil)
		s, err := ScanModule(opts.Presets, dir)
		if err != nil {
			return Report{}, fmt.Errorf("component %s: %w", k, err)
		}
		a := newAcc()
		a.merge(s)
		for _, src := range s.RegistrySources {
			rs, ok := reg[src]
			if !ok {
				missingReg[src] = true
				continue
			}
			a.merge(rs)
		}
		full := a.result()
		// Check the merged scan, so a remote module that a registry
		// module calls is caught too.
		for _, src := range full.RemoteSources {
			if !allowedRemoteSources[strings.SplitN(src, "?", 2)[0]] {
				badRemote[src] = true
			}
		}
		svcs := map[string]bool{}
		addTypes(full.Types, svcs)
		for svc := range svcs {
			deploy[svc] = true
		}
		for _, p := range full.Principals {
			principals[p] = true
		}
		for _, m := range full.ManagedPolicies {
			managed[m] = true
		}
		r.Components[k] = ComponentReport{Preset: dir, Services: keys(svcs), RolesWithoutBoundary: full.RolesWithoutBoundary, IAMWithoutPath: full.IAMWithoutPath}
	}

	if opts.IncludeImported {
		imp := map[string]bool{}
		var types []string
		for _, t := range generated.RegisteredTypes() {
			if strings.HasPrefix(t, "aws_") {
				types = append(types, t)
			}
		}
		addTypes(types, imp)
		for svc := range imp {
			deploy[svc] = true
		}
		r.ImportServices = keys(imp)
	}
	for _, s := range opts.ExtraServices {
		deploy[s] = true
	}
	r.ExtraServices = append([]string(nil), opts.ExtraServices...)
	sort.Strings(r.ExtraServices)

	boundaryExtra := map[string]bool{}
	for m := range managed {
		svcs, ok := managedPolicyServices[m]
		if !ok {
			unmappedManaged[m] = true
			continue
		}
		for _, s := range svcs {
			if !deploy[s] {
				boundaryExtra[s] = true
			}
		}
	}

	r.DeployServices = keys(deploy)
	r.BoundaryExtra = keys(boundaryExtra)
	r.Principals = keys(principals)
	r.ManagedPolicies = keys(managed)
	r.UnmappedTypes = keys(unmappedTypes)
	r.UnmappedManaged = keys(unmappedManaged)
	r.MissingRegistry = keys(missingReg)
	r.UnexpectedRemote = keys(badRemote)
	return r, nil
}

// Generate scans the presets and builds both policies. It fails when any
// type, managed policy or module source is not covered, so the policy is
// never silently narrower than the presets.
func Generate(opts Options) (Result, error) {
	r, err := Scan(opts)
	if err != nil {
		return Result{}, err
	}
	var problems []string
	if len(r.UnmappedTypes) > 0 {
		problems = append(problems, "unmapped aws_* types (add a prefix row in services.go): "+strings.Join(r.UnmappedTypes, ", "))
	}
	if len(r.UnmappedManaged) > 0 {
		problems = append(problems, "unmapped AWS managed policies (add a row in managed.go): "+strings.Join(r.UnmappedManaged, ", "))
	}
	if len(r.MissingRegistry) > 0 {
		problems = append(problems, "registry modules not in registry_types.json (run `go run ./cmd/policygen registry`): "+strings.Join(r.MissingRegistry, ", "))
	}
	if len(r.UnexpectedRemote) > 0 {
		problems = append(problems, "remote module sources the generator can not scan: "+strings.Join(r.UnexpectedRemote, ", "))
	}
	if len(problems) > 0 {
		return Result{}, fmt.Errorf("policygen: %s", strings.Join(problems, "; "))
	}
	return Result{
		Write:    writePolicy(r),
		Boundary: boundaryPolicy(r),
		Report:   r,
	}, nil
}

func serviceWildcards(svcs []string) []string {
	var out []string
	for _, s := range svcs {
		// IAM and STS are never granted whole: their statements below are
		// what keeps the role from granting itself more.
		if s == "iam" || s == "sts" {
			continue
		}
		out = append(out, s+":*")
	}
	sort.Strings(out)
	return out
}

func eq(key string, v any) map[string]map[string]any {
	return map[string]map[string]any{"StringEquals": {key: v}}
}

var star = []string{"*"}

// IAMPath is the IAM path of every role, customer managed policy and
// instance profile a preset creates for a project. The deploy policy allows
// IAM writes only under it.
//
// Why a path and not only the Project tag (#147): a path is part of the ARN,
// so one Resource scope covers create, change, pass and delete, and a
// customer's role or policy outside the path is never writable. A tag can be
// added to a customer's role, and a role tagged by other Terraform (the
// platform stack) may lack the boundary. The template must keep its own
// roles and policies (the deploy role, InsideOutWrite, the boundaries) off
// this path.
func IAMPath(project string) string { return "/insideout/" + project + "/" }

// projectARN is an IAM ARN pattern for kind under the project's path.
func projectARN(kind string) string {
	return "arn:aws:iam::*:" + kind + IAMPath(PlaceholderProject) + "*"
}

// roleWriteActions change what a project role may do. They also need the
// workload boundary on the role, so a role the deploy role controls can never
// do more than the boundary allows. Without that the deploy role could make
// an admin role and use it (#935).
var roleWriteActions = []string{
	"iam:AttachRolePolicy",
	"iam:DeleteRolePolicy",
	"iam:DetachRolePolicy",
	"iam:PutRolePermissionsBoundary",
	"iam:PutRolePolicy",
}

// projectRoleActions act on a project role without changing its permissions.
var projectRoleActions = []string{
	"iam:DeleteRole",
	"iam:TagRole",
	"iam:UntagRole",
	"iam:UpdateAssumeRolePolicy",
	"iam:UpdateRole",
	"iam:UpdateRoleDescription",
}

// projectPolicyActions manage the project's customer managed policies. A
// policy grants nothing until it is attached, and attaching needs the
// boundary on the role.
var projectPolicyActions = []string{
	"iam:CreatePolicy",
	"iam:CreatePolicyVersion",
	"iam:DeletePolicy",
	"iam:DeletePolicyVersion",
	"iam:SetDefaultPolicyVersion",
	"iam:TagPolicy",
	"iam:UntagPolicy",
}

var instanceProfileActions = []string{
	"iam:AddRoleToInstanceProfile",
	"iam:CreateInstanceProfile",
	"iam:DeleteInstanceProfile",
	"iam:RemoveRoleFromInstanceProfile",
	"iam:TagInstanceProfile",
	"iam:UntagInstanceProfile",
}

// oidcProviderActions manage the EKS cluster's OIDC provider (the eks
// registry module). They are limited to EKS issuer URLs, so the customer's
// other providers (GitHub Actions) stay out of reach.
var oidcProviderActions = []string{
	"iam:AddClientIDToOpenIDConnectProvider",
	"iam:CreateOpenIDConnectProvider",
	"iam:DeleteOpenIDConnectProvider",
	"iam:RemoveClientIDFromOpenIDConnectProvider",
	"iam:TagOpenIDConnectProvider",
	"iam:UntagOpenIDConnectProvider",
	"iam:UpdateOpenIDConnectProviderThumbprint",
}

// EKSOIDCProviderARN is the ARN pattern of an EKS cluster's OIDC provider.
const EKSOIDCProviderARN = "arn:aws:iam::*:oidc-provider/oidc.eks.*.amazonaws.com/id/*"

// ServiceLinkedRoleARN is the ARN pattern of every service-linked role. AWS
// owns their path and policies, so they can not be put under IAMPath.
const ServiceLinkedRoleARN = "arn:aws:iam::*:role/aws-service-role/*"

// PreflightResource returns the resource ARN the preflight must simulate an
// IAM action on, for the scoped policy. Other actions use "*".
//
// Why: the scoped policy allows IAM writes only on project ARNs, so
// SimulatePrincipalPolicy with no ResourceArns reports them as denied even
// when a deploy would succeed (#147 step 3).
func PreflightResource(action, project string) string {
	path := IAMPath(project)
	switch {
	case action == "iam:CreateServiceLinkedRole":
		return "arn:aws:iam::*:role/aws-service-role/preflight"
	case strings.Contains(action, "OpenIDConnectProvider"):
		return "arn:aws:iam::*:oidc-provider/oidc.eks.preflight.amazonaws.com/id/preflight"
	case strings.Contains(action, "InstanceProfile"):
		return "arn:aws:iam::*:instance-profile" + path + "preflight"
	case strings.Contains(action, "Policy") && !strings.Contains(action, "Role"):
		return "arn:aws:iam::*:policy" + path + "preflight"
	case strings.HasPrefix(action, "iam:") && !strings.HasPrefix(action, "iam:Get") && !strings.HasPrefix(action, "iam:List"):
		return "arn:aws:iam::*:role" + path + "preflight"
	}
	return "*"
}

func writePolicy(r Report) Document {
	roles := []string{projectARN("role")}
	return Document{Version: "2012-10-17", Statement: []Statement{
		{Sid: "StackServices", Effect: "Allow", Action: serviceWildcards(r.DeployServices), Resource: star},
		{Sid: "CallerIdentity", Effect: "Allow", Action: []string{"sts:GetCallerIdentity"}, Resource: star},
		{Sid: "IAMRead", Effect: "Allow", Action: []string{"iam:Get*", "iam:List*"}, Resource: star},
		// The Project tag is still required on create: the InsideOut
		// inspector finds resources by it.
		{Sid: "CreateProjectRolesWithWorkloadBoundary", Effect: "Allow", Action: []string{"iam:CreateRole"}, Resource: roles,
			Condition: map[string]map[string]any{"StringEquals": {
				"iam:PermissionsBoundary": PlaceholderWorkloadBoundaryARN,
				"aws:RequestTag/Project":  PlaceholderProject,
			}}},
		{Sid: "ChangeProjectRolesWithWorkloadBoundary", Effect: "Allow", Action: roleWriteActions, Resource: roles,
			Condition: eq("iam:PermissionsBoundary", PlaceholderWorkloadBoundaryARN)},
		{Sid: "ManageProjectRoles", Effect: "Allow", Action: projectRoleActions, Resource: roles},
		{Sid: "PassProjectRolesToStackServices", Effect: "Allow", Action: []string{"iam:PassRole"}, Resource: roles,
			Condition: eq("iam:PassedToService", r.Principals)},
		{Sid: "ProjectPolicies", Effect: "Allow", Action: projectPolicyActions, Resource: []string{projectARN("policy")}},
		{Sid: "ProjectInstanceProfiles", Effect: "Allow", Action: instanceProfileActions, Resource: []string{projectARN("instance-profile")}},
		{Sid: "EKSOIDCProviders", Effect: "Allow", Action: oidcProviderActions, Resource: []string{EKSOIDCProviderARN}},
		{Sid: "ServiceLinkedRoles", Effect: "Allow", Action: []string{
			"iam:CreateServiceLinkedRole", "iam:DeleteServiceLinkedRole", "iam:GetServiceLinkedRoleDeletionStatus",
		}, Resource: []string{ServiceLinkedRoleARN}},
		// Defense in depth: the boundary and the deploy role are off the
		// project path, so no Allow reaches them. These Denies keep it so if
		// the template ever moves them onto it.
		{Sid: "DenyEditWorkloadBoundary", Effect: "Deny", Action: []string{
			"iam:CreatePolicyVersion", "iam:DeletePolicy", "iam:DeletePolicyVersion", "iam:SetDefaultPolicyVersion",
		}, Resource: []string{PlaceholderWorkloadBoundaryARN}},
		{Sid: "DenyEditDeployRole", Effect: "Deny", Action: []string{
			"iam:AttachRolePolicy", "iam:DeleteRole", "iam:DeleteRolePermissionsBoundary", "iam:DeleteRolePolicy",
			"iam:DetachRolePolicy", "iam:PutRolePermissionsBoundary", "iam:PutRolePolicy", "iam:TagRole",
			"iam:UntagRole", "iam:UpdateAssumeRolePolicy",
		}, Resource: []string{PlaceholderDeployRoleARN}},
		{Sid: "DenyBoundaryRemoval", Effect: "Deny", Action: []string{"iam:DeleteRolePermissionsBoundary"}, Resource: star},
	}}
}

func boundaryPolicy(r Report) Document {
	all := map[string]bool{}
	for _, s := range r.DeployServices {
		all[s] = true
	}
	for _, s := range r.BoundaryExtra {
		all[s] = true
	}
	return Document{Version: "2012-10-17", Statement: []Statement{
		{Sid: "StackServices", Effect: "Allow", Action: serviceWildcards(keys(all)), Resource: star},
		{Sid: "CallerIdentity", Effect: "Allow", Action: []string{"sts:GetCallerIdentity"}, Resource: star},
		{Sid: "IAMRead", Effect: "Allow", Action: []string{"iam:Get*", "iam:List*"}, Resource: star},
		{Sid: "ServiceLinkedRoles", Effect: "Allow", Action: []string{"iam:CreateServiceLinkedRole"}, Resource: []string{ServiceLinkedRoleARN}},
		// Managed policies such as AmazonSageMakerFullAccess pass the role to
		// the service. Same limits as the deploy role.
		{Sid: "PassProjectRolesToStackServices", Effect: "Allow", Action: []string{"iam:PassRole"}, Resource: []string{projectARN("role")},
			Condition: eq("iam:PassedToService", r.Principals)},
	}}
}

// Marshal renders a document as stable, indented JSON.
func Marshal(d Document) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// MinifiedSize is the size IAM counts against its limit: the JSON without
// whitespace. A managed policy may be at most 6,144 characters.
func MinifiedSize(d Document) int {
	b, _ := json.Marshal(d)
	return len(b)
}

// ManagedPolicyLimit is IAM's size limit for a customer managed policy.
const ManagedPolicyLimit = 6144

// File names of the generated output, relative to the output directory.
const (
	WriteFile    = "insideout-write.json"
	BoundaryFile = "insideout-workload-boundary.json"
	ReportFile   = "policygen-report.json"
)

// Files renders a result as the files `policygen` writes. It fails when a
// policy is over the IAM size limit, since IAM would refuse to create it.
//
// Why here and not in the command: the golden test compares the committed
// files with this output, so both must render them the same way.
func Files(res Result) (map[string][]byte, error) {
	files := map[string][]byte{}
	for name, d := range map[string]Document{WriteFile: res.Write, BoundaryFile: res.Boundary} {
		if n := MinifiedSize(d); n > ManagedPolicyLimit {
			return nil, fmt.Errorf("%s is %d characters, over the IAM limit of %d", name, n, ManagedPolicyLimit)
		}
		b, err := Marshal(d)
		if err != nil {
			return nil, err
		}
		files[name] = b
	}
	rep, err := json.MarshalIndent(res.Report, "", "  ")
	if err != nil {
		return nil, err
	}
	files[ReportFile] = append(rep, '\n')
	return files, nil
}
