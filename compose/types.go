// Package compose is a generic Terraform composition engine that generates
// complete Terraform stacks from module specifications. It supports multiple
// instances of the same preset, explicit cross-module wiring, and produces
// all necessary Terraform files (main.tf, variables.tf, outputs.tf, etc.).
package compose

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
)

// StackSpec describes a complete Terraform stack to compose.
// The caller provides fully-resolved modules in dependency order with
// all variable values and cross-module wiring pre-computed.
type StackSpec struct {
	// Modules lists modules in dependency order (first = no deps).
	// Order determines the sequence of module blocks in main.tf.
	Modules []ModuleSpec

	// RootVars declares additional root-level variables beyond those
	// auto-generated from module variable namespacing.
	RootVars map[string]VarSpec

	// Providers configures providers.tf. If nil, no providers.tf is emitted.
	Providers *ProvidersSpec

	// TerraformVersion is written to .terraform-version. If empty, omitted.
	TerraformVersion string

	// PresetFS is the filesystem containing preset modules.
	// If nil, defaults to the embedded FS from this repository.
	PresetFS fs.FS
}

// ModuleSpec describes one module instance in the composed stack.
type ModuleSpec struct {
	// Name is the Terraform module block name (e.g., "vpc", "lambda_api").
	// Must be unique within the stack and a valid Terraform identifier.
	Name string

	// PresetPath is the path within PresetFS (e.g., "aws/vpc", "gcp/cloudsql").
	PresetPath string

	// SourcePath overrides the module source in the emitted module block.
	// If empty, defaults to "./modules/<Name>".
	SourcePath string

	// Values maps variable names to concrete values for .auto.tfvars.
	// Only variables NOT in Wiring should appear here.
	// Supported types: string, bool, int, int64, float64, []any, map[string]any.
	Values map[string]any

	// Wiring maps variable names to raw HCL expressions for cross-module refs.
	// Example: {"vpc_id": "module.vpc.vpc_id"}
	Wiring map[string]string

	// Providers maps provider aliases for the module block.
	// Example: {"aws": "aws", "aws.us_east_1": "aws.us_east_1"}
	Providers map[string]string

	// ExcludeOutputs suppresses re-exporting this module's outputs.
	ExcludeOutputs bool
}

// ProvidersSpec configures providers.tf generation.
type ProvidersSpec struct {
	// Raw, if non-empty, is used verbatim as providers.tf content.
	Raw []byte

	// Cloud is "aws" or "gcp". Used with Region to generate standard providers.tf.
	// Ignored if Raw is set.
	Cloud string

	// Region for the default provider.
	Region string

	// AWSAssumeRole, if true, adds bootstrap_role_arn / external_id variables
	// and a dynamic assume_role block to the AWS provider.
	AWSAssumeRole bool

	// ExtraProviderBlocks holds additional provider aliases.
	ExtraProviderBlocks []ProviderBlock

	// ExtraVarDecls is prepended verbatim to providers.tf before the terraform block.
	ExtraVarDecls []byte
}

// ProviderBlock describes an additional provider alias.
type ProviderBlock struct {
	Type     string            // e.g., "aws", "google"
	Alias    string            // e.g., "us_east_1"
	Settings map[string]string // e.g., {"region": "us-east-1"}
}

// VarSpec defines schema metadata for a Terraform variable.
type VarSpec struct {
	Type      string   // "string" | "number" | "bool" | "list(string)" | "map(string)" | "any"
	Enum      []string // for string enum validation
	Min       *float64 // for number min validation
	Max       *float64 // for number max validation
	MinItems  *int     // for list min items validation
	MaxItems  *int     // for list max items validation
	Sensitive bool
	Doc       string // optional description
}

// RawExpr wraps a raw HCL expression string (e.g., "var.vpc_id" or
// "module.vpc.vpc_id"). It cannot be converted to cty and is used
// as a marker for setting raw attribute tokens in emitted HCL.
type RawExpr struct {
	Expr string
}

// VarEntry is a name-value pair for .auto.tfvars emission.
type VarEntry struct {
	Name  string
	Value any
}

// VarMeta describes a variable block discovered from a preset's .tf files.
type VarMeta struct {
	Name       string
	HasDefault bool
	TypeExpr   string // canonicalized: "string", "number", "bool", "list(string)", "map(string)", "any"
}

// OutputMeta describes an output block discovered from a preset's .tf files.
type OutputMeta struct {
	Name        string
	Description string
	Sensitive   bool
}

// validIdentifier matches valid Terraform identifiers.
var validIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// validateSpec checks a StackSpec for structural errors before composition.
func validateSpec(spec *StackSpec) error {
	if spec == nil {
		return fmt.Errorf("spec must not be nil")
	}
	names := map[string]bool{}
	for i, m := range spec.Modules {
		if m.Name == "" {
			return fmt.Errorf("module at index %d has empty Name", i)
		}
		if !validIdentifier.MatchString(m.Name) {
			return fmt.Errorf("module %q: Name must be a valid Terraform identifier (alphanumeric + underscore, not starting with digit)", m.Name)
		}
		if names[m.Name] {
			return fmt.Errorf("duplicate module Name %q", m.Name)
		}
		names[m.Name] = true
		if m.PresetPath == "" {
			return fmt.Errorf("module %q has empty PresetPath", m.Name)
		}
	}
	return nil
}

// nsKey returns the namespaced variable name: "<moduleName>_<varName>".
func nsKey(moduleName, varName string) string {
	return moduleName + "_" + varName
}


/* ====================== type inference ====================== */

func inferSimpleType(v any) string {
	switch x := v.(type) {
	case nil:
		return "any"
	case bool:
		return "bool"
	case string:
		return "string"
	case int, int64, float64:
		return "number"
	case []any:
		if len(x) == 0 {
			return "any"
		}
		allStr := true
		for _, it := range x {
			if _, ok := it.(string); !ok {
				allStr = false
				break
			}
		}
		if allStr {
			return "list(string)"
		}
		return "any"
	case map[string]any:
		for _, it := range x {
			if _, ok := it.(string); !ok {
				return "any"
			}
		}
		return "map(string)"
	default:
		return "any"
	}
}

func renderSpecType(t string) string {
	switch t {
	case "string", "number", "bool", "any", "map(string)", "list(string)":
		return t
	default:
		return "any"
	}
}

/* ====================== validation ====================== */

func validateRequired(vars []VarMeta, wiring map[string]string, vals map[string]any, module string) error {
	for _, v := range vars {
		if _, isWired := wiring[v.Name]; isWired {
			continue
		}
		if v.HasDefault {
			continue
		}
		if _, ok := vals[v.Name]; !ok {
			return fmt.Errorf("module %s requires variable %q (no default and no value provided)", module, v.Name)
		}
	}
	return nil
}

/* ====================== helpers ====================== */

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

