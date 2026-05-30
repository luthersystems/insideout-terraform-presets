package progress

import "testing"

// fakeTypeSink is a minimal TypeProgressEmitter — it satisfies the base
// Emitter contract (no-ops) plus the optional per-type extension.
type fakeTypeSink struct {
	NopEmitter
	got []TypeProgress
}

func (f *fakeTypeSink) TypeDone(p TypeProgress) { f.got = append(f.got, p) }

// TestNopEmitterIsNotTypeProgressEmitter pins the load-bearing design
// property of #699: the per-type extension interface is OPTIONAL, and
// the default (no-sink) Emitter — NopEmitter — must NOT satisfy it. The
// orchestrators type-assert their Emitter to TypeProgressEmitter and
// skip the per-type path when the assertion fails; if NopEmitter ever
// grew a TypeDone method, that path would fire under the default config
// and the "byte-for-byte unchanged" back-compat guarantee would break.
func TestNopEmitterIsNotTypeProgressEmitter(t *testing.T) {
	t.Parallel()
	var e Emitter = NopEmitter{}
	if _, ok := e.(TypeProgressEmitter); ok {
		t.Error("NopEmitter must NOT implement TypeProgressEmitter — the default no-sink path would otherwise emit per-type events")
	}
}

// TestJSONEmitterIsNotTypeProgressEmitter pins that the wire
// (--progress=json / SSE) Emitter does not implement the per-type
// extension either: per-type events are a facade-only concern (#699),
// so the CLI event stream and its golden tests stay untouched. A
// regression that added TypeDone to JSONEmitter would silently inject a
// new event type into the wire stream.
func TestJSONEmitterIsNotTypeProgressEmitter(t *testing.T) {
	t.Parallel()
	var e Emitter = NewJSONEmitter(nil)
	if _, ok := e.(TypeProgressEmitter); ok {
		t.Error("JSONEmitter must NOT implement TypeProgressEmitter — per-type events are facade-only and must not enter the wire stream")
	}
}

// TestTypeProgressEmitter_OptInWorks confirms the positive half: an
// Emitter that DOES implement the extension is reachable via the same
// type assertion the orchestrators use, and TypeDone delivers the event.
func TestTypeProgressEmitter_OptInWorks(t *testing.T) {
	t.Parallel()
	var e Emitter = &fakeTypeSink{}
	tp, ok := e.(TypeProgressEmitter)
	if !ok {
		t.Fatal("fakeTypeSink should satisfy TypeProgressEmitter")
	}
	tp.TypeDone(TypeProgress{Phase: "discover", TFType: "aws_s3_bucket", Found: 3, Total: 7})
	got := e.(*fakeTypeSink).got
	if len(got) != 1 {
		t.Fatalf("TypeDone recorded %d events, want 1", len(got))
	}
	want := TypeProgress{Phase: "discover", TFType: "aws_s3_bucket", Found: 3, Total: 7}
	if got[0] != want {
		t.Errorf("TypeDone event = %+v, want %+v", got[0], want)
	}
}
