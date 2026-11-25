package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// A module is a directory of .tf files that the composer can call: a preset
// (aws/<m>, gcp/<m>) or a shared helper (aws/_shared/<m>, gcp/_shared/<m>,
// _shared/<m>). Its interface is its variables and outputs, which is what a
// caller of the module, and so a release reader, has to know about.
var moduleFileRE = regexp.MustCompile(`^((?:aws|gcp)/(?:_shared/)?[^/]+|_shared/[^/]+)/[^/]+\.tf$`)

// A changed path under a module directory, at any depth (tests/ too).
var moduleDirRE = regexp.MustCompile(`^((?:aws|gcp)/(?:_shared/)?[^/]+|_shared/[^/]+)/`)

type status string

const (
	statusNew     status = "new"
	statusRemoved status = "removed"
	statusChanged status = "changed"
)

type variable struct {
	Name       string
	Default    string
	HasDefault bool
	// value is the evaluated default, when it is a constant, so that a
	// formatting change (a trailing comma, one line or many) is not a new
	// default. cty.NilVal when it does not evaluate without a context.
	value cty.Value
	// Type, Validation and Nullable are source text, whitespace collapsed.
	// Validation is only the condition of each validation block: a
	// reworded error_message accepts and refuses the same inputs.
	Type       string
	Validation string
	Nullable   string
	// typ is the parsed type constraint, so that a reflowed object type is
	// not a type change. cty.NilType when it does not parse, or when it
	// has optional attribute defaults, which only the text compares.
	typ cty.Type
}

type defaultChange struct {
	Name, From, To string
}

type moduleChange struct {
	Dir    string
	Status status
	// VarsAdded without a default are new required inputs: every caller
	// must now set them.
	VarsAdded       []variable
	VarsRemoved     []string
	VarsNowRequired []string
	DefaultsChanged []defaultChange
	OutputsAdded    []string
	OutputsRemoved  []string
	// TypesChanged and NullableOff can stop a caller's plan. Validations
	// and sensitive outputs may or may not: a reader checks them.
	TypesChanged       []string
	NullableOff        []string
	ValidationsChanged []string
	SensitiveChanged   []string
	// TestsOnly is true when only files under tests/ changed.
	TestsOnly bool
}

// breaking reports a change that can stop a caller's plan: a removed module,
// variable or output, an input the caller must now set, a changed variable
// type, or nullable turned off.
// check reports a change that may break a caller and needs a reader to
// decide: a changed default, a changed validation, or an output whose
// sensitive flag changed (a caller that prints it now fails). The release
// skill's version guide calls a breaking default change a major bump.
func (c moduleChange) check() bool {
	return len(c.DefaultsChanged) > 0 || len(c.ValidationsChanged) > 0 || len(c.SensitiveChanged) > 0
}

func (c moduleChange) breaking() bool {
	if c.Status == statusRemoved || len(c.VarsRemoved) > 0 || len(c.OutputsRemoved) > 0 || len(c.VarsNowRequired) > 0 ||
		len(c.TypesChanged) > 0 || len(c.NullableOff) > 0 {
		return true
	}
	if c.Status != statusChanged {
		return false
	}
	for _, v := range c.VarsAdded {
		if !v.HasDefault {
			return true
		}
	}
	return false
}

type iface struct {
	vars map[string]variable
	// outputs maps an output name to its sensitive attribute's text.
	outputs map[string]string
}

// parseModule reads the variables and outputs of the .tf files of dir in
// tree. ok is false when dir has no .tf file.
func parseModule(dir string, tree map[string][]byte) (iface, bool, error) {
	m := iface{vars: map[string]variable{}, outputs: map[string]string{}}
	found := false
	for p, src := range tree {
		if path.Dir(p) != dir || path.Ext(p) != ".tf" {
			continue
		}
		found = true
		f, diags := hclsyntax.ParseConfig(src, p, hcl.InitialPos)
		if diags.HasErrors() {
			return m, true, fmt.Errorf("parse %s: %s", p, diags.Error())
		}
		for _, b := range f.Body.(*hclsyntax.Body).Blocks {
			if len(b.Labels) != 1 {
				continue
			}
			switch b.Type {
			case "variable":
				v := variable{Name: b.Labels[0], value: cty.NilVal, typ: cty.NilType}
				if a, ok := b.Body.Attributes["default"]; ok {
					v.HasDefault = true
					v.Default = normalize(string(a.Expr.Range().SliceBytes(src)))
					if val, d := a.Expr.Value(nil); !d.HasErrors() {
						v.value = val
					}
				}
				v.Type = attrText(b.Body, "type", src)
				if a, ok := b.Body.Attributes["type"]; ok {
					if t, defs, d := typeexpr.TypeConstraintWithDefaults(a.Expr); !d.HasErrors() && defs == nil {
						v.typ = t
					}
				}
				v.Nullable = attrText(b.Body, "nullable", src)
				var vals []string
				for _, vb := range b.Body.Blocks {
					if vb.Type == "validation" {
						vals = append(vals, attrText(vb.Body, "condition", src))
					}
				}
				v.Validation = strings.Join(vals, "\n")
				m.vars[v.Name] = v
			case "output":
				// No sensitive attribute is sensitive = false.
				sens := attrText(b.Body, "sensitive", src)
				if sens == "" {
					sens = "false"
				}
				m.outputs[b.Labels[0]] = sens
			}
		}
	}
	return m, found, nil
}

func attrText(body *hclsyntax.Body, name string, src []byte) string {
	if a, ok := body.Attributes[name]; ok {
		return normalize(string(a.Expr.Range().SliceBytes(src)))
	}
	return ""
}

// sameDefault compares two defaults by value when both are constants, and
// by whitespace-collapsed text when either is not (it refers to a function
// or a variable).
func sameDefault(a, b variable) bool {
	if a.value != cty.NilVal && b.value != cty.NilVal {
		return a.value.RawEquals(b.value)
	}
	return a.Default == b.Default
}

// sameType compares two type constraints by type when both parse, and by
// whitespace-collapsed text when either does not.
func sameType(a, b variable) bool {
	if a.typ != cty.NilType && b.typ != cty.NilType {
		return a.typ.Equals(b.typ)
	}
	return a.Type == b.Type
}

// normalize collapses whitespace, so a terraform fmt change of a default is
// not reported as a new default.
func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// moduleChanges compares each module that has a changed path, between the
// trees of the previous release and of this one.
func moduleChanges(changed []string, oldTree, newTree map[string][]byte) ([]moduleChange, error) {
	dirs := map[string]bool{}
	nonTest := map[string]bool{}
	for _, p := range changed {
		m := moduleDirRE.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		dirs[m[1]] = true
		if !strings.HasPrefix(p, m[1]+"/tests/") {
			nonTest[m[1]] = true
		}
	}
	var out []moduleChange
	for _, dir := range sortedKeys(dirs) {
		oldM, hadOld, err := parseModule(dir, oldTree)
		if err != nil {
			return nil, err
		}
		newM, hasNew, err := parseModule(dir, newTree)
		if err != nil {
			return nil, err
		}
		c := moduleChange{Dir: dir}
		switch {
		case !hadOld && !hasNew:
			continue // not a module, for example a directory of only docs
		case !hadOld:
			c.Status = statusNew
		case !hasNew:
			c.Status = statusRemoved
		default:
			c.Status = statusChanged
			c.TestsOnly = !nonTest[dir]
			for _, n := range sortedKeys(newM.vars) {
				nv := newM.vars[n]
				ov, ok := oldM.vars[n]
				switch {
				case !ok:
					c.VarsAdded = append(c.VarsAdded, nv)
				case ov.HasDefault && !nv.HasDefault:
					c.VarsNowRequired = append(c.VarsNowRequired, n)
				case nv.HasDefault && !sameDefault(ov, nv):
					from := ov.Default
					if !ov.HasDefault {
						from = "none (required)"
					}
					c.DefaultsChanged = append(c.DefaultsChanged, defaultChange{Name: n, From: from, To: nv.Default})
				}
				if !ok {
					continue
				}
				if !sameType(ov, nv) {
					c.TypesChanged = append(c.TypesChanged, n)
				}
				if nv.Nullable == "false" && ov.Nullable != "false" {
					c.NullableOff = append(c.NullableOff, n)
				}
				if ov.Validation != nv.Validation {
					c.ValidationsChanged = append(c.ValidationsChanged, n)
				}
			}
			for _, n := range sortedKeys(oldM.vars) {
				if _, ok := newM.vars[n]; !ok {
					c.VarsRemoved = append(c.VarsRemoved, n)
				}
			}
			for _, n := range sortedKeys(newM.outputs) {
				os, ok := oldM.outputs[n]
				if !ok {
					c.OutputsAdded = append(c.OutputsAdded, n)
				} else if os != newM.outputs[n] {
					c.SensitiveChanged = append(c.SensitiveChanged, n)
				}
			}
			for _, n := range sortedKeys(oldM.outputs) {
				if _, ok := newM.outputs[n]; !ok {
					c.OutputsRemoved = append(c.OutputsRemoved, n)
				}
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// rootPackage names the Go package at the repo root, whose zz_embed.go
// decides which preset files a consumer of this module gets.
const rootPackage = "github.com/luthersystems/insideout-terraform-presets"

// changedPackages lists the Go packages under pkg/ whose non-test code
// changed, at the top level (pkg/<name>), sorted, and the root package.
func changedPackages(changed []string) []string {
	set := map[string]bool{}
	for _, p := range changed {
		parts := strings.Split(p, "/")
		if len(parts) == 1 && path.Ext(p) == ".go" && !strings.HasSuffix(p, "_test.go") {
			set[rootPackage] = true
			continue
		}
		if len(parts) < 3 || parts[0] != "pkg" || path.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			continue
		}
		set["pkg/"+parts[1]] = true
	}
	return sortedKeys(set)
}

func code(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = span(n)
	}
	return strings.Join(q, ", ")
}

// span is an inline code span that holds s even when s has a backtick: the
// fence is one backtick longer than the longest run in s.
func span(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if longest == 0 {
		return "`" + s + "`"
	}
	fence := strings.Repeat("`", longest+1)
	return fence + " " + s + " " + fence
}

// short keeps a long default (a list or an object) from taking over a
// table row. It cuts on a rune boundary, and escapes | after the cut, so
// no | splits the row.
func short(s string) string {
	if len(s) > 60 {
		cut := 57
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return strings.ReplaceAll(s, "|", `\|`)
}

func (c moduleChange) describe() string {
	switch c.Status {
	case statusNew:
		return "New module"
	case statusRemoved:
		return "Removed module"
	}
	var parts []string
	if len(c.VarsAdded) > 0 {
		var vs []string
		for _, v := range c.VarsAdded {
			if v.HasDefault {
				vs = append(vs, fmt.Sprintf("%s (default %s)", span(v.Name), span(short(v.Default))))
			} else {
				vs = append(vs, fmt.Sprintf("%s (required)", span(v.Name)))
			}
		}
		parts = append(parts, "Variables added: "+strings.Join(vs, ", "))
	}
	if len(c.VarsRemoved) > 0 {
		parts = append(parts, "Variables removed: "+code(c.VarsRemoved))
	}
	if len(c.VarsNowRequired) > 0 {
		parts = append(parts, "Now required: "+code(c.VarsNowRequired))
	}
	if len(c.DefaultsChanged) > 0 {
		var ds []string
		for _, d := range c.DefaultsChanged {
			ds = append(ds, fmt.Sprintf("%s %s to %s", span(d.Name), span(short(d.From)), span(short(d.To))))
		}
		parts = append(parts, "Defaults changed: "+strings.Join(ds, ", "))
	}
	if len(c.OutputsAdded) > 0 {
		parts = append(parts, "Outputs added: "+code(c.OutputsAdded))
	}
	if len(c.OutputsRemoved) > 0 {
		parts = append(parts, "Outputs removed: "+code(c.OutputsRemoved))
	}
	if len(c.TypesChanged) > 0 {
		parts = append(parts, "Types changed: "+code(c.TypesChanged))
	}
	if len(c.NullableOff) > 0 {
		parts = append(parts, "Now nullable = false: "+code(c.NullableOff))
	}
	if len(c.ValidationsChanged) > 0 {
		parts = append(parts, "Validations changed: "+code(c.ValidationsChanged))
	}
	if len(c.SensitiveChanged) > 0 {
		parts = append(parts, "Output sensitive flag changed: "+code(c.SensitiveChanged))
	}
	if len(parts) == 0 {
		if c.TestsOnly {
			return "Tests only"
		}
		return "No variable or output change"
	}
	return strings.Join(parts, ". ")
}

const moduleChangesHeading = "## Module Changes\n\n"

// render writes the Module Changes section, which the shared release
// workflow puts after its list of changes. With no module or package
// change it is empty, so the release has no empty section.
func render(changes []moduleChange, pkgs []string) string {
	if len(changes) == 0 && len(pkgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(moduleChangesHeading)
	var breaking, check []string
	for _, c := range changes {
		if c.breaking() {
			breaking = append(breaking, c.Dir)
		} else if c.check() {
			check = append(check, c.Dir)
		}
	}
	if len(breaking) > 0 {
		b.WriteString("**Breaking:** " + code(breaking) + ". A caller may have to change its inputs or outputs.\n\n")
	}
	if len(check) > 0 {
		b.WriteString("**Check:** " + code(check) + ". A changed default, validation or sensitive output may break a caller.\n\n")
	}
	if len(changes) > 0 {
		b.WriteString("| Module | Change |\n|---|---|\n")
		for _, c := range changes {
			fmt.Fprintf(&b, "| `%s` | %s |\n", c.Dir, c.describe())
		}
		b.WriteString("\n")
	}
	if len(pkgs) > 0 {
		b.WriteString("Go packages changed: " + code(pkgs) + ".\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
