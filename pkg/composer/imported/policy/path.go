package policy

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// ErrNoSuchPath is returned when a Layer 2 path cannot be resolved
// against the Layer 1 generated struct or any registered JSON
// projection rule.
var ErrNoSuchPath = errors.New("policy: no such path")

// JSONProjection declares a logical subpath of a JSON-string attribute.
// Some provider fields combine wiring and tuning in one serialized
// JSON value (e.g. aws_sqs_queue.redrive_policy). Layer 2 keys those
// subfields with dot notation; the projection rule names the parent HCL
// attribute that backs them. Phase 2 only declares projections — the
// composer (#148) and validateImportedResources (#149) own the actual
// JSON read-modify-write logic.
type JSONProjection struct {
	// Parent is the Layer 1 attribute name carrying the JSON string
	// (e.g. "redrive_policy").
	Parent string
	// Subpath is the logical key inside the JSON value
	// (e.g. "deadLetterTargetArn"). May contain further dots for
	// nested JSON objects.
	Subpath string
}

// Path returns the dotted Layer 2 path for the projection, in the form
// "<parent>.<subpath>".
func (p JSONProjection) Path() string {
	return p.Parent + "." + p.Subpath
}

var (
	projMu  sync.RWMutex
	projReg = map[string]map[string]JSONProjection{} // tfType -> path -> projection
)

// RegisterJSONProjection records that the given logical path on tfType
// is backed by a JSON-string attribute named p.Parent. Per-type policy
// files call this from init() before declaring policy entries that
// reference the path.
func RegisterJSONProjection(tfType string, p JSONProjection) {
	if p.Parent == "" || p.Subpath == "" {
		panic(fmt.Sprintf("policy: invalid JSONProjection for %q: %+v", tfType, p))
	}
	projMu.Lock()
	defer projMu.Unlock()
	if _, ok := projReg[tfType]; !ok {
		projReg[tfType] = map[string]JSONProjection{}
	}
	if _, dup := projReg[tfType][p.Path()]; dup {
		panic(fmt.Sprintf("policy: duplicate JSONProjection for %q at %q", tfType, p.Path()))
	}
	projReg[tfType][p.Path()] = p
}

// LookupJSONProjection returns the projection registered for path on
// tfType, or false.
func LookupJSONProjection(tfType, path string) (JSONProjection, bool) {
	projMu.RLock()
	defer projMu.RUnlock()
	if m, ok := projReg[tfType]; ok {
		p, ok := m[path]
		return p, ok
	}
	return JSONProjection{}, false
}

// ResolvePath checks whether the dotted Layer 2 path is reachable on
// tfType. Resolution succeeds when:
//
//   - The path matches a chain of `tf:"<segment>"` tags on the Layer 1
//     struct registered for tfType in the generated package, terminating
//     at a leaf attribute (*Value[T] / []*Value[T] / map of strings to
//     *Value[T]); nested ,blocks and singleton ,block fields are
//     descended into with the next segment treated as the block's own
//     attribute name.
//   - The path is registered as a JSONProjection for tfType.
//
// Bracketed map keys (`environment.variables["DATABASE_URL"]`) and
// list indices (`ingress[0].cidr_blocks`) are accepted at the
// corresponding map / slice level; the bracket content is not
// validated — the policy authority is the path shape, not the
// individual key.
//
// Returns nil on success, ErrNoSuchPath wrapped in context on failure.
func ResolvePath(tfType, path string) error {
	if path == "" {
		return fmt.Errorf("%w: %q (empty path)", ErrNoSuchPath, path)
	}
	if _, ok := LookupJSONProjection(tfType, path); ok {
		return nil
	}
	goType, _, ok := generated.Lookup(tfType)
	if !ok {
		return fmt.Errorf("%w: tfType %q not registered in Layer 1", ErrNoSuchPath, tfType)
	}
	segs, err := splitPath(path)
	if err != nil {
		return fmt.Errorf("%w: %q: %v", ErrNoSuchPath, path, err)
	}
	if err := walkSegments(goType, segs); err != nil {
		return fmt.Errorf("%w: %q: %v", ErrNoSuchPath, path, err)
	}
	return nil
}

// pathSegment is one parsed step. name carries the attribute name
// (with brackets stripped); kind says how to descend.
type pathSegment struct {
	name      string
	hasBucket bool // true if this segment carried a ["..."] or [N] suffix
}

// splitPath parses a Layer 2 path into segments. Splitting happens on
// '.' outside of brackets; bracket contents are kept on the preceding
// segment.
func splitPath(path string) ([]pathSegment, error) {
	var (
		segs    []pathSegment
		buf     strings.Builder
		inBrack bool
		seg     pathSegment
	)
	flush := func() error {
		if buf.Len() == 0 && seg.name == "" {
			return errors.New("empty segment")
		}
		if seg.name == "" {
			seg.name = buf.String()
			buf.Reset()
		}
		segs = append(segs, seg)
		seg = pathSegment{}
		return nil
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch c {
		case '.':
			if inBrack {
				buf.WriteByte(c)
				continue
			}
			if err := flush(); err != nil {
				return nil, err
			}
		case '[':
			if inBrack {
				return nil, errors.New("nested '[' not supported")
			}
			if seg.name == "" {
				seg.name = buf.String()
				buf.Reset()
			}
			seg.hasBucket = true
			inBrack = true
		case ']':
			if !inBrack {
				return nil, errors.New("unmatched ']'")
			}
			buf.Reset() // discard bracket contents
			inBrack = false
		default:
			buf.WriteByte(c)
		}
	}
	if inBrack {
		return nil, errors.New("unclosed '['")
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return segs, nil
}

// walkSegments descends through goType applying segs in order. Returns
// nil when every segment is consumed at a reachable point in the type.
//
// A bracket suffix (`tags["key"]` / `ingress[0]`) is only legal on a
// map or slice field; using brackets on a scalar leaf is a structural
// mistake and is rejected.
func walkSegments(goType reflect.Type, segs []pathSegment) error {
	cur := goType
	for i, seg := range segs {
		// Unwrap pointer-to-struct.
		for cur.Kind() == reflect.Pointer {
			cur = cur.Elem()
		}
		// Unwrap a single-block slice (`,blocks`) at this level: at the
		// boundary into a slice we treat the next segment as targeting
		// the element type. Detection happens at the field hop below.
		if cur.Kind() != reflect.Struct {
			return fmt.Errorf("segment %d %q: cannot descend into %v", i, seg.name, cur.Kind())
		}
		f, ok := findFieldByTag(cur, seg.name)
		if !ok {
			return fmt.Errorf("segment %d %q: not found on %s", i, seg.name, cur.Name())
		}
		ft := f.Type
		// Slice (`,blocks`): treat the element type as the next struct
		// to descend into. The bracket suffix on the segment is
		// optional; without it, "all elements" is the implicit
		// authority.
		if ft.Kind() == reflect.Slice && ft.Elem().Kind() != reflect.Pointer {
			cur = ft.Elem()
			continue
		}
		// Map of map[string]*Value[T] — leaf-shaped; only valid as the
		// final segment, possibly with a [key].
		if ft.Kind() == reflect.Map {
			if i != len(segs)-1 {
				return fmt.Errorf("segment %d %q: descended past map leaf", i, seg.name)
			}
			return nil
		}
		// Pointer-to-Value[T] leaves.
		if ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct &&
			strings.HasPrefix(ft.Elem().Name(), "Value[") {
			if seg.hasBucket {
				return fmt.Errorf("segment %d %q: bracket suffix not valid on scalar leaf", i, seg.name)
			}
			if i != len(segs)-1 {
				return fmt.Errorf("segment %d %q: descended past *Value leaf", i, seg.name)
			}
			return nil
		}
		// Slice of *Value[T] (list-of-string etc).
		if ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Pointer {
			if i != len(segs)-1 {
				return fmt.Errorf("segment %d %q: descended past list-leaf", i, seg.name)
			}
			return nil
		}
		// Pointer-to-struct (`,block` singleton like timeouts).
		if ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct {
			if seg.hasBucket {
				return fmt.Errorf("segment %d %q: bracket suffix not valid on singleton block", i, seg.name)
			}
			cur = ft.Elem()
			continue
		}
		// Struct directly.
		if ft.Kind() == reflect.Struct {
			if seg.hasBucket {
				return fmt.Errorf("segment %d %q: bracket suffix not valid on inline struct", i, seg.name)
			}
			cur = ft
			continue
		}
		return fmt.Errorf("segment %d %q: unexpected field kind %v", i, seg.name, ft.Kind())
	}
	return nil
}

// findFieldByTag walks goType's fields and returns the one whose
// `tf:"<name>[,...]"` tag matches name.
func findFieldByTag(goType reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < goType.NumField(); i++ {
		f := goType.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("tf")
		if tag == "" {
			continue
		}
		// First comma-separated piece is the attribute name; rest are
		// kind hints (",block" / ",blocks") that don't matter for path
		// resolution because both descend into a struct anyway.
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}
