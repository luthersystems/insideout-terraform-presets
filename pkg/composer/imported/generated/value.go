package generated

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Value wraps a single Terraform attribute value with three states that
// must remain distinguishable for zero-loss HCL round-trip:
//
//   - absent — represented by a nil *Value pointer in containing structs
//   - explicit null — Null=true, Literal=nil, Expr=""
//   - literal — Literal points at a value of type T, Expr=""
//   - expression — Expr is a Terraform reference such as
//     `aws_kms_key.main.arn` or `module.kms.arn`, Literal=nil
//
// The composer needs to distinguish "the user wrote `kms_master_key_id =
// aws_kms_key.main.arn`" from "the user wrote `kms_master_key_id =
// "arn:aws:kms:..."` literally" because the former is a wiring edge that
// must survive re-emission while the latter is a pinned literal. Plain Go
// scalars cannot carry that distinction.
//
// Wire format (JSON):
//   - absent → field omitted (nil pointer + omitempty on the containing
//     struct field)
//   - explicit null → {"null": true}
//   - literal → {"literal": <T-encoded>}
//   - expression → {"expr": "..."}
//
// MarshalJSON enforces that exactly one state is set; UnmarshalJSON rejects
// objects with more than one of the three keys present.
type Value[T any] struct {
	Literal *T
	Expr    string
	Null    bool
}

// LiteralOf is a small constructor for the literal state. Pointer-to-literal
// is awkward at call sites (`x := "foo"; v := Value[string]{Literal: &x}`),
// so most callers use this helper.
func LiteralOf[T any](v T) *Value[T] {
	return &Value[T]{Literal: &v}
}

// ExprOf is a small constructor for the expression state.
func ExprOf[T any](expr string) *Value[T] {
	return &Value[T]{Expr: expr}
}

// NullOf returns a Value carrying the explicit-null state.
func NullOf[T any]() *Value[T] {
	return &Value[T]{Null: true}
}

// State reports which of the four states the value is in. A nil receiver
// reports StateAbsent.
func (v *Value[T]) State() ValueState {
	if v == nil {
		return StateAbsent
	}
	switch {
	case v.Null:
		return StateNull
	case v.Expr != "":
		return StateExpr
	case v.Literal != nil:
		return StateLiteral
	default:
		return StateAbsent
	}
}

// ValueState is a small convenience enum describing which of the four
// representable states a Value is in.
type ValueState int

const (
	StateAbsent ValueState = iota
	StateNull
	StateLiteral
	StateExpr
)

func (s ValueState) String() string {
	switch s {
	case StateAbsent:
		return "absent"
	case StateNull:
		return "null"
	case StateLiteral:
		return "literal"
	case StateExpr:
		return "expr"
	}
	return fmt.Sprintf("ValueState(%d)", int(s))
}

// MarshalJSON enforces the single-state invariant. An "absent" value should
// not be marshaled at all (the containing struct uses *Value[T] +
// omitempty); receiving an absent Value here is a programmer error and
// returns an error rather than silently emitting "{}".
func (v Value[T]) MarshalJSON() ([]byte, error) {
	set := 0
	if v.Null {
		set++
	}
	if v.Literal != nil {
		set++
	}
	if v.Expr != "" {
		set++
	}
	if set == 0 {
		return nil, errors.New("Value: refusing to marshal an empty (absent) value; use a nil *Value with omitempty on the parent field")
	}
	if set > 1 {
		return nil, errors.New("Value: exactly one of Null, Literal, Expr must be set")
	}
	switch {
	case v.Null:
		return []byte(`{"null":true}`), nil
	case v.Expr != "":
		return json.Marshal(struct {
			Expr string `json:"expr"`
		}{Expr: v.Expr})
	default: // Literal
		return json.Marshal(struct {
			Literal *T `json:"literal"`
		}{Literal: v.Literal})
	}
}

// UnmarshalJSON rejects multi-state objects. {"null":true,"literal":"x"}
// fails immediately rather than silently picking one — the wire format has
// always been mutually exclusive and a multi-state object indicates either
// upstream corruption or a buggy producer.
func (v *Value[T]) UnmarshalJSON(b []byte) error {
	var raw struct {
		Null    *bool           `json:"null,omitempty"`
		Literal json.RawMessage `json:"literal,omitempty"`
		Expr    *string         `json:"expr,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	set := 0
	if raw.Null != nil {
		set++
	}
	if len(raw.Literal) > 0 {
		set++
	}
	if raw.Expr != nil {
		set++
	}
	if set == 0 {
		return errors.New("Value: at least one of null/literal/expr must be present")
	}
	if set > 1 {
		return errors.New("Value: at most one of null/literal/expr may be present")
	}

	*v = Value[T]{}
	switch {
	case raw.Null != nil:
		if !*raw.Null {
			return errors.New("Value: null must be true if present")
		}
		v.Null = true
	case raw.Expr != nil:
		v.Expr = *raw.Expr
	default:
		var lit T
		if err := json.Unmarshal(raw.Literal, &lit); err != nil {
			return fmt.Errorf("Value: decoding literal: %w", err)
		}
		v.Literal = &lit
	}
	return nil
}
