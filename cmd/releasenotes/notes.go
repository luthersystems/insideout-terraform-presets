package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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
	// TestsOnly is true when only files under tests/ changed.
	TestsOnly bool
}

// breaking reports a change that can stop a caller's plan: a removed module,
// variable or output, or an input the caller must now set. A changed default
// is listed but not counted, since most default changes are fixes; the reader
// decides (the release skill's version guide).
func (c moduleChange) breaking() bool {
	if c.Status == statusRemoved || len(c.VarsRemoved) > 0 || len(c.OutputsRemoved) > 0 || len(c.VarsNowRequired) > 0 {
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
	vars    map[string]variable
	outputs map[string]bool
}

// parseModule reads the variables and outputs of the .tf files of dir in
// tree. ok is false when dir has no .tf file.
func parseModule(dir string, tree map[string][]byte) (iface, bool, error) {
	m := iface{vars: map[string]variable{}, outputs: map[string]bool{}}
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
				v := variable{Name: b.Labels[0]}
				if a, ok := b.Body.Attributes["default"]; ok {
					v.HasDefault = true
					v.Default = normalize(string(a.Expr.Range().SliceBytes(src)))
				}
				m.vars[v.Name] = v
			case "output":
				m.outputs[b.Labels[0]] = true
			}
		}
	}
	return m, found, nil
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
				case nv.HasDefault && ov.Default != nv.Default:
					from := ov.Default
					if !ov.HasDefault {
						from = "none (required)"
					}
					c.DefaultsChanged = append(c.DefaultsChanged, defaultChange{Name: n, From: from, To: nv.Default})
				}
			}
			for _, n := range sortedKeys(oldM.vars) {
				if _, ok := newM.vars[n]; !ok {
					c.VarsRemoved = append(c.VarsRemoved, n)
				}
			}
			for _, n := range sortedKeys(newM.outputs) {
				if !oldM.outputs[n] {
					c.OutputsAdded = append(c.OutputsAdded, n)
				}
			}
			for _, n := range sortedKeys(oldM.outputs) {
				if !newM.outputs[n] {
					c.OutputsRemoved = append(c.OutputsRemoved, n)
				}
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// changedPackages lists the Go packages under pkg/ whose non-test code
// changed, at the top level (pkg/<name>), sorted.
func changedPackages(changed []string) []string {
	set := map[string]bool{}
	for _, p := range changed {
		parts := strings.Split(p, "/")
		if len(parts) < 3 || parts[0] != "pkg" || path.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			continue
		}
		set["pkg/"+parts[1]] = true
	}
	return sortedKeys(set)
}

var mergeRE = regexp.MustCompile(`^Merge pull request #([0-9]+) from `)

// prLine is one bullet: a merged PR's title (the first body line of its
// merge commit) with its number, or a direct commit's subject.
func prLine(sha, subject, body string) string {
	if m := mergeRE.FindStringSubmatch(subject); m != nil {
		title := subject
		for _, l := range strings.Split(body, "\n") {
			if strings.TrimSpace(l) != "" {
				title = strings.TrimSpace(l)
				break
			}
		}
		return fmt.Sprintf("- %s (#%s)", title, m[1])
	}
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf("- %s (%s)", subject, short)
}

func code(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	return strings.Join(q, ", ")
}

// short keeps a long default (a list or an object) from taking over a
// table row.
func short(s string) string {
	if len(s) > 60 {
		return s[:57] + "..."
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
				vs = append(vs, fmt.Sprintf("`%s` (default `%s`)", v.Name, short(v.Default)))
			} else {
				vs = append(vs, fmt.Sprintf("`%s` (required)", v.Name))
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
			ds = append(ds, fmt.Sprintf("`%s` `%s` to `%s`", d.Name, short(d.From), short(d.To)))
		}
		parts = append(parts, "Defaults changed: "+strings.Join(ds, ", "))
	}
	if len(c.OutputsAdded) > 0 {
		parts = append(parts, "Outputs added: "+code(c.OutputsAdded))
	}
	if len(c.OutputsRemoved) > 0 {
		parts = append(parts, "Outputs removed: "+code(c.OutputsRemoved))
	}
	if len(parts) == 0 {
		if c.TestsOnly {
			return "Tests only"
		}
		return "No variable or output change"
	}
	return strings.Join(parts, ". ")
}

func render(prs []string, changes []moduleChange, pkgs []string, compareURL string) string {
	var b strings.Builder
	b.WriteString("## What's Changed\n\n")
	if len(prs) == 0 {
		b.WriteString("No changes.\n")
	}
	for _, l := range prs {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n## Module Changes\n\n")
	if len(changes) == 0 && len(pkgs) == 0 {
		b.WriteString("No module changed.\n")
	}
	var breaking []string
	for _, c := range changes {
		if c.breaking() {
			breaking = append(breaking, c.Dir)
		}
	}
	if len(breaking) > 0 {
		b.WriteString("**Breaking:** " + code(breaking) + ". A caller may have to change its inputs or outputs.\n\n")
	}
	if len(changes) > 0 {
		b.WriteString("| Module | Change |\n|---|---|\n")
		for _, c := range changes {
			fmt.Fprintf(&b, "| `%s` | %s |\n", c.Dir, c.describe())
		}
		b.WriteString("\n")
	}
	if len(pkgs) > 0 {
		b.WriteString("Go packages changed: " + code(pkgs) + ".\n\n")
	}
	if compareURL != "" {
		b.WriteString("**Full Changelog**: " + compareURL + "\n")
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
