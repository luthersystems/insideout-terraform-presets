package composer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	cty "github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

type validationRegistry struct {
	variables map[moduleVarKey]moduleVariableValidator
}

type moduleVarKey struct {
	component ComponentKey
	variable  string
}

type moduleVariableValidator struct {
	name    string
	typ     cty.Type
	rules   []moduleValidationRule
	allowed []string
}

type moduleValidationRule struct {
	condition    hcl.Expression
	errorMessage string
}

type validationFailure struct {
	code   string
	reason string
}

var (
	defaultValidationRegistryOnce sync.Once
	defaultValidationRegistryVal  *validationRegistry
	defaultValidationRegistryErr  error
)

func defaultValidationRegistry() (*validationRegistry, error) {
	defaultValidationRegistryOnce.Do(func() {
		defaultValidationRegistryVal, defaultValidationRegistryErr = buildDefaultValidationRegistry()
	})
	return defaultValidationRegistryVal, defaultValidationRegistryErr
}

func buildDefaultValidationRegistry() (*validationRegistry, error) {
	client := New()
	reg := &validationRegistry{variables: map[moduleVarKey]moduleVariableValidator{}}
	for _, component := range AllComponentKeys {
		presetPath := GetPresetPath(CloudFor(component), component, &Components{})
		files, err := client.GetPresetFiles(presetPath)
		if err != nil {
			return nil, fmt.Errorf("load preset %s: %w", presetPath, err)
		}
		vars, err := discoverModuleVariableValidators(files)
		if err != nil {
			return nil, fmt.Errorf("discover validators for %s: %w", presetPath, err)
		}
		for name, validator := range vars {
			reg.variables[moduleVarKey{component: component, variable: name}] = validator
		}
	}
	return reg, nil
}

func discoverModuleVariableValidators(files map[string][]byte) (map[string]moduleVariableValidator, error) {
	out := map[string]moduleVariableValidator{}
	paths := make([]string, 0, len(files))
	for p := range files {
		if strings.HasSuffix(strings.ToLower(p), ".tf") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	for _, p := range paths {
		f, diags := hclsyntax.ParseConfig(files[p], p, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s", diags.Error())
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, block := range body.Blocks {
			if block.Type != "variable" || len(block.Labels) != 1 {
				continue
			}
			name := block.Labels[0]
			validator := moduleVariableValidator{
				name: name,
				typ:  cty.DynamicPseudoType,
			}
			if attr, ok := block.Body.Attributes["type"]; ok && attr.Expr != nil {
				typ, typeDiags := typeexpr.TypeConstraint(attr.Expr)
				if !typeDiags.HasErrors() {
					validator.typ = typ
				}
			}

			for _, inner := range block.Body.Blocks {
				if inner.Type != "validation" {
					continue
				}
				attr, ok := inner.Body.Attributes["condition"]
				if !ok || attr.Expr == nil {
					continue
				}
				msg := "validation condition failed"
				if msgAttr, ok := inner.Body.Attributes["error_message"]; ok && msgAttr.Expr != nil {
					if literal := literalString(msgAttr.Expr); literal != "" {
						msg = literal
					}
				}
				validator.rules = append(validator.rules, moduleValidationRule{
					condition:    attr.Expr,
					errorMessage: msg,
				})
				validator.allowed = appendUniqueStrings(validator.allowed, extractAllowedValues(attr.Expr, name)...)
			}
			out[name] = validator
		}
	}
	return out, nil
}

func (r *validationRegistry) validate(component ComponentKey, variable string, raw any) (validationFailure, bool) {
	if r == nil {
		return validationFailure{code: "internal_error", reason: "validation registry is nil"}, false
	}
	validator, ok := r.variables[moduleVarKey{component: component, variable: variable}]
	if !ok || len(validator.rules) == 0 {
		return validationFailure{}, true
	}

	value, err := ctyValueForType(raw, validator.typ)
	if err != nil {
		return validationFailure{
			code:   "invalid_type",
			reason: fmt.Sprintf("%s=%s: %v", variable, issueValue(raw), err),
		}, false
	}
	if !validator.typ.Equals(cty.DynamicPseudoType) {
		value, err = convert.Convert(value, validator.typ)
		if err != nil {
			return validationFailure{
				code:   "invalid_type",
				reason: fmt.Sprintf("%s=%s: cannot convert to %s: %v", variable, issueValue(raw), validator.typ.FriendlyNameForConstraint(), err),
			}, false
		}
	}

	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"var": cty.ObjectVal(map[string]cty.Value{variable: value}),
		},
		Functions: validationFunctions(),
	}
	for _, rule := range validator.rules {
		result, diags := rule.condition.Value(ctx)
		if diags.HasErrors() {
			return validationFailure{
				code:   "invalid_value",
				reason: fmt.Sprintf("%s=%s: %s", variable, issueValue(raw), diags.Error()),
			}, false
		}
		if !result.IsWhollyKnown() || result.IsNull() {
			return validationFailure{
				code:   "invalid_value",
				reason: fmt.Sprintf("%s=%s: validation condition did not produce a known boolean", variable, issueValue(raw)),
			}, false
		}
		if !result.Type().Equals(cty.Bool) {
			converted, err := convert.Convert(result, cty.Bool)
			if err != nil {
				return validationFailure{
					code:   "invalid_value",
					reason: fmt.Sprintf("%s=%s: validation condition returned %s, not bool", variable, issueValue(raw), result.Type().FriendlyName()),
				}, false
			}
			result = converted
		}
		if !result.True() {
			return validationFailure{
				code:   "invalid_value",
				reason: fmt.Sprintf("%s=%s: %s", variable, issueValue(raw), rule.errorMessage),
			}, false
		}
	}
	return validationFailure{}, true
}

func (r *validationRegistry) allowedValues(component ComponentKey, variable string) []string {
	if r == nil {
		return nil
	}
	validator, ok := r.variables[moduleVarKey{component: component, variable: variable}]
	if !ok {
		return nil
	}
	return cloneStrings(validator.allowed)
}

var validationFunctionsOnce sync.Once
var validationFunctionsMap map[string]function.Function

func validationFunctions() map[string]function.Function {
	validationFunctionsOnce.Do(func() {
		validationFunctionsMap = map[string]function.Function{
			"can":        tryfunc.CanFunc,
			"contains":   stdlib.ContainsFunc,
			"length":     lengthFunc(),
			"lower":      stdlib.LowerFunc,
			"regex":      stdlib.RegexFunc,
			"replace":    stdlib.ReplaceFunc,
			"trimspace":  stdlib.TrimSpaceFunc,
			"upper":      stdlib.UpperFunc,
			"alltrue":    allTrueFunc(),
			"startswith": startsWithFunc(),
		}
	})
	return validationFunctionsMap
}

func allTrueFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{
			Name:             "collection",
			Type:             cty.DynamicPseudoType,
			AllowDynamicType: true,
		}},
		Type: function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			collection := args[0]
			if collection.IsNull() {
				return cty.False, nil
			}
			if !collection.CanIterateElements() {
				return cty.NilVal, fmt.Errorf("alltrue requires an iterable collection")
			}
			it := collection.ElementIterator()
			for it.Next() {
				_, elem := it.Element()
				elem, _ = elem.Unmark()
				if elem.Type().Equals(cty.String) {
					elem = cty.BoolVal(strings.EqualFold(elem.AsString(), "true"))
				}
				if !elem.Type().Equals(cty.Bool) {
					return cty.NilVal, fmt.Errorf("alltrue elements must be bool-compatible")
				}
				if !elem.True() {
					return cty.False, nil
				}
			}
			return cty.True, nil
		},
	})
}

func startsWithFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "str", Type: cty.String},
			{Name: "prefix", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.BoolVal(strings.HasPrefix(args[0].AsString(), args[1].AsString())), nil
		},
	})
}

func lengthFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{
			Name: "value",
			Type: cty.DynamicPseudoType,
		}},
		Type: function.StaticReturnType(cty.Number),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			value := args[0]
			value, _ = value.Unmark()
			if value.Type().Equals(cty.String) {
				return cty.NumberIntVal(int64(len([]rune(value.AsString())))), nil
			}
			if value.CanIterateElements() {
				return value.Length(), nil
			}
			return cty.NilVal, fmt.Errorf("length requires a string or collection")
		},
	})
}

func ctyValueForType(v any, target cty.Type) (cty.Value, error) {
	if v == nil {
		if target.Equals(cty.DynamicPseudoType) {
			return cty.NullVal(cty.DynamicPseudoType), nil
		}
		return cty.NullVal(target), nil
	}

	switch x := v.(type) {
	case string:
		return cty.StringVal(x), nil
	case bool:
		return cty.BoolVal(x), nil
	case int:
		return cty.NumberIntVal(int64(x)), nil
	case int64:
		return cty.NumberIntVal(x), nil
	case float64:
		return cty.NumberFloatVal(x), nil
	case []string:
		if len(x) == 0 {
			return emptyCollectionForType(target), nil
		}
		vals := make([]cty.Value, len(x))
		for i, s := range x {
			vals[i] = cty.StringVal(s)
		}
		return cty.TupleVal(vals), nil
	case []any:
		if len(x) == 0 {
			return emptyCollectionForType(target), nil
		}
		vals := make([]cty.Value, len(x))
		for i, elem := range x {
			val, err := ctyValueForType(elem, cty.DynamicPseudoType)
			if err != nil {
				return cty.NilVal, err
			}
			vals[i] = val
		}
		return cty.TupleVal(vals), nil
	case []int:
		if len(x) == 0 {
			return emptyCollectionForType(target), nil
		}
		vals := make([]cty.Value, len(x))
		for i, n := range x {
			vals[i] = cty.NumberIntVal(int64(n))
		}
		return cty.TupleVal(vals), nil
	default:
		return cty.NilVal, fmt.Errorf("unsupported Go value type %T", v)
	}
}

func emptyCollectionForType(target cty.Type) cty.Value {
	switch {
	case target.IsListType():
		return cty.ListValEmpty(target.ElementType())
	case target.IsSetType():
		return cty.SetValEmpty(target.ElementType())
	case target.IsTupleType():
		return cty.EmptyTupleVal
	default:
		return cty.EmptyTupleVal
	}
}

func extractAllowedValues(expr hcl.Expression, varName string) []string {
	expr = hcl.UnwrapExpression(expr)
	switch e := expr.(type) {
	case *hclsyntax.FunctionCallExpr:
		var out []string
		if e.Name == "contains" && len(e.Args) == 2 && exprReferencesVar(e.Args[1], varName) {
			out = append(out, literalTupleValues(e.Args[0])...)
		}
		for _, arg := range e.Args {
			out = appendUniqueStrings(out, extractAllowedValues(arg, varName)...)
		}
		return out
	case *hclsyntax.BinaryOpExpr:
		return appendUniqueStrings(
			extractAllowedValues(e.LHS, varName),
			extractAllowedValues(e.RHS, varName)...,
		)
	case *hclsyntax.ConditionalExpr:
		out := extractAllowedValues(e.Condition, varName)
		out = appendUniqueStrings(out, extractAllowedValues(e.TrueResult, varName)...)
		out = appendUniqueStrings(out, extractAllowedValues(e.FalseResult, varName)...)
		return out
	case *hclsyntax.ForExpr:
		return extractAllowedValues(e.ValExpr, varName)
	case *hclsyntax.ParenthesesExpr:
		return extractAllowedValues(e.Expression, varName)
	default:
		return nil
	}
}

func exprReferencesVar(expr hcl.Expression, varName string) bool {
	for _, traversal := range expr.Variables() {
		if len(traversal) < 2 || traversal.RootName() != "var" {
			continue
		}
		if attr, ok := traversal[1].(hcl.TraverseAttr); ok && attr.Name == varName {
			return true
		}
	}
	return false
}

func literalTupleValues(expr hcl.Expression) []string {
	expr = hcl.UnwrapExpression(expr)
	tuple, ok := expr.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range tuple.Exprs {
		if v, ok := literalCtyValue(item); ok {
			out = append(out, ctyLiteralString(v))
		}
	}
	return out
}

func literalCtyValue(expr hcl.Expression) (cty.Value, bool) {
	expr = hcl.UnwrapExpression(expr)
	switch e := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		return e.Val, true
	case *hclsyntax.TemplateExpr:
		if len(e.Parts) == 1 {
			if lit, ok := e.Parts[0].(*hclsyntax.LiteralValueExpr); ok {
				return lit.Val, true
			}
		}
	}
	return cty.NilVal, false
}

func ctyLiteralString(v cty.Value) string {
	v, _ = v.Unmark()
	switch {
	case v.Type().Equals(cty.String):
		return v.AsString()
	case v.Type().Equals(cty.Number):
		return v.AsBigFloat().Text('f', -1)
	case v.Type().Equals(cty.Bool):
		if v.True() {
			return "true"
		}
		return "false"
	default:
		return v.GoString()
	}
}

func appendUniqueStrings(dst []string, vals ...string) []string {
	seen := make(map[string]bool, len(dst)+len(vals))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range vals {
		if v == "" || seen[v] {
			continue
		}
		dst = append(dst, v)
		seen[v] = true
	}
	return dst
}

func normalizeStringWith(fn func(string) (string, error)) func(any) (any, error) {
	return func(v any) (any, error) {
		return fn(v.(string))
	}
}

func normalizeStrictInt(field string) func(any) (any, error) {
	return func(v any) (any, error) {
		switch x := v.(type) {
		case int:
			return x, nil
		case string:
			n, err := strconv.Atoi(strings.TrimSpace(x))
			if err != nil {
				return nil, NewValidationError(fmt.Sprintf(
					"%s=%q: expected an integer",
					field, x,
				))
			}
			return n, nil
		default:
			return nil, NewValidationError(fmt.Sprintf("%s=%s: expected an integer", field, issueValue(v)))
		}
	}
}

func normalizeLeadingInt(field string) func(any) (any, error) {
	return func(v any) (any, error) {
		return parseLeadingInt(v.(string), field)
	}
}

func normalizeStorageGB(field string) func(any) (any, error) {
	return func(v any) (any, error) {
		return parseStorageSizeGB(v.(string), field)
	}
}

func normalizeTTLSeconds(field string) func(any) (any, error) {
	return func(v any) (any, error) {
		return parseTTLSeconds(v.(string), field)
	}
}

func normalizeDurationSeconds(field string) func(any) (any, error) {
	return func(v any) (any, error) {
		return parseDurationToSeconds(v.(string), field)
	}
}

func normalizeRetentionHours(field string) func(any) (any, error) {
	return func(v any) (any, error) {
		return parseRetentionHours(v.(string), field)
	}
}

func normalizeEKSControlPlaneVisibility(v any) (any, error) {
	s := strings.ToLower(strings.TrimSpace(v.(string)))
	switch s {
	case "public", "public endpoint", "public control plane":
		return true, nil
	case "private", "private endpoint", "private control plane":
		return false, nil
	default:
		return nil, NewValidationError(fmt.Sprintf(
			"AWSEKS.ControlPlaneVisibility=%q: expected \"Public\" or \"Private\"",
			v.(string),
		))
	}
}

func normalizeECSCapacityProvidersValue(v any) (any, error) {
	providers := v.([]string)
	out := make([]string, len(providers))
	for i, provider := range providers {
		canonical, err := canonicalECSCapacityProvider(provider)
		if err != nil {
			return nil, err
		}
		out[i] = canonical
	}
	return out, nil
}

func normalizeECSCapacityProviderValue(v any) (any, error) {
	return canonicalECSCapacityProvider(v.(string))
}

func canonicalECSCapacityProvider(s string) (string, error) {
	canonical := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
	switch canonical {
	case "FARGATE", "FARGATE_SPOT":
		return canonical, nil
	default:
		return "", NewValidationError(fmt.Sprintf(
			"AWSECS.CapacityProvider=%q: expected \"FARGATE\" or \"FARGATE_SPOT\"",
			s,
		))
	}
}

func normalizeSQSQueueType(v any) (any, error) {
	switch strings.ToLower(strings.TrimSpace(v.(string))) {
	case "standard":
		return "Standard", nil
	case "fifo":
		return "FIFO", nil
	default:
		return nil, NewValidationError(fmt.Sprintf(
			"AWSSQS.Type=%q: expected \"Standard\" or \"FIFO\"",
			v.(string),
		))
	}
}

func normalizeCognitoSignInType(v any) (any, error) {
	s := strings.ToLower(strings.TrimSpace(v.(string)))
	switch s {
	case "email", "username", "both":
		return s, nil
	default:
		return nil, NewValidationError(fmt.Sprintf(
			"AWSCognito.SignInType=%q: expected \"email\", \"username\", or \"both\"",
			v.(string),
		))
	}
}

func normalizeOpenSearchDeploymentType(v any) (any, error) {
	s := strings.ToLower(strings.TrimSpace(v.(string)))
	switch s {
	case "managed", "serverless":
		return s, nil
	default:
		return nil, NewValidationError(fmt.Sprintf(
			"AWSOpenSearch.DeploymentType=%q: expected \"managed\" or \"serverless\"",
			v.(string),
		))
	}
}

func normalizeGCPMemorystoreTier(v any) (any, error) {
	s := strings.ToUpper(strings.TrimSpace(v.(string)))
	switch s {
	case "BASIC", "STANDARD_HA":
		return s, nil
	default:
		return nil, NewValidationError(fmt.Sprintf(
			"GCPMemorystore.Tier=%q: expected \"BASIC\" or \"STANDARD_HA\"",
			v.(string),
		))
	}
}

func normalizeGCPStorageClass(v any) (any, error) {
	s := strings.ToUpper(strings.TrimSpace(v.(string)))
	switch s {
	case "STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE":
		return s, nil
	default:
		return nil, NewValidationError(fmt.Sprintf(
			"GCPGCS.StorageClass=%q: expected one of STANDARD, NEARLINE, COLDLINE, ARCHIVE",
			v.(string),
		))
	}
}
