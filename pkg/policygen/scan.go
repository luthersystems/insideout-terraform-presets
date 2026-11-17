package policygen

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ModuleScan is what one Terraform module (with the local modules it calls)
// asks of AWS IAM. It is the generator's raw input: the policy is built from
// the union of the scans, never from a hand list per component.
type ModuleScan struct {
	// Types are the aws_* resource and data types the module declares.
	Types []string `json:"types"`
	// ManagedPolicies are AWS managed policy names attached to roles
	// (for example "AmazonEKS_CNI_Policy", "service-role/AWSLambdaBasicExecutionRole").
	ManagedPolicies []string `json:"managed_policies,omitempty"`
	// Principals are the service principals in the trust policies of the
	// aws_iam_role resources: the services the deploy role passes roles to.
	Principals []string `json:"principals,omitempty"`
	// RegistrySources are the Terraform registry modules it calls, as
	// "namespace/name/provider@version".
	RegistrySources []string `json:"registry_sources,omitempty"`
	// RemoteSources are the other remote module sources it calls (git URLs).
	RemoteSources []string `json:"remote_sources,omitempty"`
}

var (
	// An AWS managed policy ARN, written literally or through the eks
	// module's "${local.iam_role_policy_prefix}/<name>" form.
	managedPolicyRE = regexp.MustCompile(`(?:iam::aws:policy|iam_role_policy_prefix\})/((?:service-role/)?[A-Za-z0-9_-]+)`)
	principalRE     = regexp.MustCompile(`"([a-z0-9-]+(?:\.[a-z0-9-]+)*\.amazonaws\.com)"`)
	policyDocRefRE  = regexp.MustCompile(`data\.aws_iam_policy_document\.([A-Za-z0-9_-]+)`)
	registryRE      = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9-]+/[a-z0-9]+$`)
)

// ScanModule scans the module in dir of fsys, and every local module
// ("./x", "../x") it calls. Registry and remote sources are recorded, not
// followed: their content is not in fsys.
func ScanModule(fsys fs.FS, dir string) (ModuleScan, error) {
	acc := newAcc()
	if err := scanDir(fsys, path.Clean(dir), acc, map[string]bool{}); err != nil {
		return ModuleScan{}, err
	}
	return acc.result(), nil
}

type acc struct {
	types, managed, principals, registry, remote map[string]bool
}

func newAcc() *acc {
	return &acc{map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *acc) result() ModuleScan {
	return ModuleScan{
		Types:           keys(a.types),
		ManagedPolicies: keys(a.managed),
		Principals:      keys(a.principals),
		RegistrySources: keys(a.registry),
		RemoteSources:   keys(a.remote),
	}
}

func scanDir(fsys fs.FS, dir string, a *acc, seen map[string]bool) error {
	if seen[dir] {
		return nil
	}
	seen[dir] = true
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("read module %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		p := path.Join(dir, e.Name())
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		locals, err := scanFile(p, src, a)
		if err != nil {
			return err
		}
		for _, l := range locals {
			if err := scanDir(fsys, path.Join(dir, l), a, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// scanFile records one file's types, policies, principals and module
// sources, and returns the local module paths it calls.
func scanFile(name string, src []byte, a *acc) ([]string, error) {
	f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse %s: %s", name, diags.Error())
	}
	body := f.Body.(*hclsyntax.Body)
	text := func(r hcl.Range) string { return string(src[r.Start.Byte:r.End.Byte]) }

	for _, m := range managedPolicyRE.FindAllStringSubmatch(string(src), -1) {
		a.managed[m[1]] = true
	}

	docs := map[string]string{} // aws_iam_policy_document name -> source text
	var trustTexts []string
	var locals []string
	for _, b := range body.Blocks {
		switch b.Type {
		case "resource", "data":
			if len(b.Labels) == 0 || !strings.HasPrefix(b.Labels[0], "aws_") {
				continue
			}
			a.types[b.Labels[0]] = true
			if b.Type == "data" && b.Labels[0] == "aws_iam_policy_document" && len(b.Labels) > 1 {
				docs[b.Labels[1]] = text(b.Range())
			}
			if b.Type == "resource" && b.Labels[0] == "aws_iam_role" {
				if attr, ok := b.Body.Attributes["assume_role_policy"]; ok {
					trustTexts = append(trustTexts, text(attr.Expr.Range()))
				}
			}
		case "module":
			src := literal(b.Body.Attributes["source"])
			ver := literal(b.Body.Attributes["version"])
			switch {
			case src == "":
			case strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../"):
				locals = append(locals, src)
			case registryRE.MatchString(src):
				a.registry[src+"@"+ver] = true
			default:
				a.remote[src] = true
			}
		}
	}
	// A trust policy is jsonencode()d inline or read from a policy document
	// data source; follow the reference so its principals count.
	for _, t := range trustTexts {
		for _, m := range principalRE.FindAllStringSubmatch(t, -1) {
			a.principals[m[1]] = true
		}
		for _, ref := range policyDocRefRE.FindAllStringSubmatch(t, -1) {
			for _, m := range principalRE.FindAllStringSubmatch(docs[ref[1]], -1) {
				a.principals[m[1]] = true
			}
		}
	}
	return locals, nil
}

// literal returns attr's value when it is a plain string literal, else "".
func literal(attr *hclsyntax.Attribute) string {
	if attr == nil {
		return ""
	}
	v, diags := attr.Expr.Value(nil)
	if diags.HasErrors() || !v.IsKnown() || v.IsNull() || !v.Type().Equals(cty.String) {
		return ""
	}
	return v.AsString()
}

// merge adds b into a.
func (a *acc) merge(b ModuleScan) {
	for _, s := range b.Types {
		a.types[s] = true
	}
	for _, s := range b.ManagedPolicies {
		a.managed[s] = true
	}
	for _, s := range b.Principals {
		a.principals[s] = true
	}
	for _, s := range b.RegistrySources {
		a.registry[s] = true
	}
	for _, s := range b.RemoteSources {
		a.remote[s] = true
	}
}
