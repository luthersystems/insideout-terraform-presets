package inspect

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMaxBatchSubs pins the wire-coupled batch ceiling. Both the MCP
// client and the reliable batch dispatcher read this constant; a
// careless edit that desync'd it from the dispatcher's reject-threshold
// would silently drop or accept oversized batches.
func TestMaxBatchSubs(t *testing.T) {
	t.Parallel()
	if MaxBatchSubs != 32 {
		t.Errorf("MaxBatchSubs = %d, want 32 (wire-coupled to reliable's batch dispatcher and MCP-server payload check)", MaxBatchSubs)
	}
}

// TestSubRequest_JSONShape pins every json tag and `omitempty` rule on
// SubRequest. Reliable's HTTP handler decodes against this exact shape;
// any drift here breaks the wire contract.
func TestSubRequest_JSONShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   SubRequest
		want string
	}{
		{
			name: "every field populated",
			in:   SubRequest{Service: "lambda", Action: "list-functions", Filters: `{"prefix":"io-"}`},
			want: `{"service":"lambda","action":"list-functions","filters":"{\"prefix\":\"io-\"}"}`,
		},
		{
			// Filters has `omitempty` — empty string must be omitted, not
			// serialized as `"filters":""`. A regression dropping the
			// omitempty would inflate every batch payload by ~14 bytes
			// per sub and could trip strict-mode JSON decoders.
			name: "empty filters omitted",
			in:   SubRequest{Service: "ec2", Action: "list-instances"},
			want: `{"service":"ec2","action":"list-instances"}`,
		},
		{
			// HTML-significant ASCII (`<`, `>`, `&`) must serialize as
			// `<` / `>` / `&` — Go's encoding/json
			// `SetEscapeHTML` defaults to true and reliable's HTTP
			// handler decodes against the escaped form. A regression
			// that flipped to `SetEscapeHTML(false)` (e.g. via a
			// custom encoder) would emit raw `<&>` and break any
			// downstream consumer that round-trips the payload
			// through HTML-bearing transports. Pin the escaped form
			// so the drift surfaces here rather than at the network
			// layer.
			name: "html-significant ascii is escaped",
			in:   SubRequest{Service: "ec2", Action: "list-instances", Filters: `{"q":"a<b&c>d"}`},
			// The `<` / `&` / `>` literals here are six
			// real characters each (a backslash + u + 4 hex digits) —
			// that's what Go's json.Marshal emits when SetEscapeHTML
			// is true (the default). DO NOT collapse to literal `<` /
			// `&` / `>` — that's a different wire format.
			want: "{\"service\":\"ec2\",\"action\":\"list-instances\",\"filters\":\"{\\\"q\\\":\\\"a\\u003cb\\u0026c\\u003ed\\\"}\"}",
		},
		{
			// Non-ASCII codepoints (UTF-8) pass through verbatim — Go
			// does NOT escape them. Pin so a regression that added a
			// blanket `\u`-escaping pass would break non-Latin filter
			// payloads at the wire and surface as a noisy round-trip
			// equality failure.
			name: "unicode passes through verbatim",
			in:   SubRequest{Service: "kms", Action: "list-aliases", Filters: `{"alias":"日本語"}`},
			want: `{"service":"kms","action":"list-aliases","filters":"{\"alias\":\"日本語\"}"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Marshal(%+v)\n  got = %s\n want = %s", tc.in, got, tc.want)
			}
			// Round-trip pin: decoding the marshaled bytes must
			// yield the original struct. NOTE: this catches encoder
			// drift (a regression that produced output the decoder
			// can't parse), but it does NOT catch case-only tag
			// changes — Go's json decoder is case-insensitive by
			// default, so flipping `json:"service"` to
			// `json:"Service"` would still round-trip cleanly. The
			// byte-equal Marshal check above is what guards against
			// that; the round-trip is supplementary protection
			// against decoder-side drift.
			var rt SubRequest
			if err := json.Unmarshal(got, &rt); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if rt != tc.in {
				t.Errorf("round-trip mismatch:\n  got = %+v\n want = %+v", rt, tc.in)
			}
		})
	}
}

// TestSubResult_JSONShape pins the response wire format. The
// `omitempty` and "always emit" choices here are load-bearing:
// observability tooling reads `duration_ms` even on healthy zero-ms
// probes; analytics counts `error` presence to compute success rates.
func TestSubResult_JSONShape(t *testing.T) {
	t.Parallel()
	t.Run("success result with payload", func(t *testing.T) {
		t.Parallel()
		// The dispatcher returns inner results as decoded JSON (map /
		// slice / scalar after `json.Unmarshal` into `any`), so a
		// map is the realistic shape — not a typed Go struct.
		in := SubResult{
			Index:      3,
			Service:    "lambda",
			Action:     "list-functions",
			OK:         true,
			Result:     map[string]any{"functions": []any{"io-handler"}},
			DurationMS: 142,
		}
		got, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal success path: %v", err)
		}
		want := `{"index":3,"service":"lambda","action":"list-functions","ok":true,"result":{"functions":["io-handler"]},"duration_ms":142}`
		if string(got) != want {
			t.Errorf("\n  got = %s\n want = %s", got, want)
		}
		// "error" must NOT appear when empty — a regression that
		// dropped omitempty would emit `"error":""` and break analytics
		// that count error-presence.
		if strings.Contains(string(got), `"error"`) {
			t.Errorf("error field must be omitted when empty; got %s", got)
		}
	})
	t.Run("error result without payload", func(t *testing.T) {
		t.Parallel()
		in := SubResult{
			Index:      0,
			Service:    "ec2",
			Action:     "list-instances",
			OK:         false,
			Error:      "throttling",
			DurationMS: 5,
		}
		got, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal error path: %v", err)
		}
		want := `{"index":0,"service":"ec2","action":"list-instances","ok":false,"error":"throttling","duration_ms":5}`
		if string(got) != want {
			t.Errorf("\n  got = %s\n want = %s", got, want)
		}
		// "result" must NOT appear as `null` — for `Result any`
		// with `omitempty`, only nil (or unset) collapses to
		// omitted. NOTE: an empty composite wrapped in `any` (e.g.
		// `Result: []any{}` or `Result: map[string]any{}`) does
		// NOT collapse — it serializes as `"result":[]` or
		// `"result":{}`. The empty-list-success shape is pinned in
		// the next subtest.
		if strings.Contains(string(got), `"result"`) {
			t.Errorf("result field must be omitted on error path; got %s", got)
		}
	})
	t.Run("success result with empty list payload", func(t *testing.T) {
		t.Parallel()
		// Pins the empty-list success shape — e.g. `list-buckets`
		// returning zero buckets. `Result any` with `omitempty`
		// only omits nil; a non-nil-empty composite must serialize
		// as `[]` or `{}`. This is the load-bearing wire shape for
		// the #255 nil-vs-empty-slice rule (see
		// pkg/observability/discovery/CONTRIBUTING.md): the
		// dispatcher returns a non-nil empty slice so downstream
		// consumers can distinguish "no results" from "field
		// missing".
		in := SubResult{
			Index:      2,
			Service:    "s3",
			Action:     "list-buckets",
			OK:         true,
			Result:     []any{},
			DurationMS: 8,
		}
		got, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		want := `{"index":2,"service":"s3","action":"list-buckets","ok":true,"result":[],"duration_ms":8}`
		if string(got) != want {
			t.Errorf("\n  got = %s\n want = %s", got, want)
		}
	})
	t.Run("zero duration is emitted", func(t *testing.T) {
		t.Parallel()
		// duration_ms is documented as always-emitted (no omitempty).
		// A fast probe that legitimately measures 0ms must still carry
		// the field so observability dashboards can distinguish "0ms
		// fast probe" from "field missing / dispatcher bug." Pin
		// byte-equal — strictly stronger than `Contains` because it
		// also catches an accidental `omitempty` on `OK`, a tag
		// rename anywhere on the success-zero path, or field
		// reordering.
		in := SubResult{Index: 1, Service: "s3", Action: "list-buckets", OK: true, DurationMS: 0}
		got, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		want := `{"index":1,"service":"s3","action":"list-buckets","ok":true,"duration_ms":0}`
		if string(got) != want {
			t.Errorf("\n  got = %s\n want = %s", got, want)
		}
	})
}

// TestBatchRequest_RoundTripJSON pins the batch request envelope. The
// outer shape is what the HTTP handler decodes off the request body.
func TestBatchRequest_RoundTripJSON(t *testing.T) {
	t.Parallel()
	in := BatchRequest{
		SessionID: "sess_abc123",
		Subs: []SubRequest{
			{Service: "lambda", Action: "list-functions"},
			{Service: "ec2", Action: "list-instances", Filters: `{"vpc":"vpc-1"}`},
		},
	}
	first, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("BatchRequest first Marshal: %v", err)
	}
	want := `{"session_id":"sess_abc123","subs":[{"service":"lambda","action":"list-functions"},{"service":"ec2","action":"list-instances","filters":"{\"vpc\":\"vpc-1\"}"}]}`
	if string(first) != want {
		t.Errorf("first marshal:\n  got = %s\n want = %s", first, want)
	}
	// Encode → decode → encode and confirm byte-equal. Catches
	// regressions where the encoder normalizes representation but the
	// decoder doesn't reverse it.
	var rt BatchRequest
	if err := json.Unmarshal(first, &rt); err != nil {
		t.Fatalf("BatchRequest Unmarshal: %v", err)
	}
	second, err := json.Marshal(rt)
	if err != nil {
		t.Fatalf("BatchRequest second Marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("round-trip drift:\n  first  = %s\n second = %s", first, second)
	}
}

// TestBatchResponse_RoundTripJSON pins the batch response envelope.
// MCP-server callers decode against this exact shape; any tag drift
// would silently break their result rendering.
func TestBatchResponse_RoundTripJSON(t *testing.T) {
	t.Parallel()
	in := BatchResponse{
		OK: true,
		Results: []SubResult{
			{Index: 0, Service: "lambda", Action: "list-functions", OK: true, Result: []any{"io-handler"}, DurationMS: 12},
			{Index: 1, Service: "ec2", Action: "list-instances", OK: false, Error: "throttling", DurationMS: 5},
		},
	}
	first, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("BatchResponse first Marshal: %v", err)
	}
	want := `{"ok":true,"results":[{"index":0,"service":"lambda","action":"list-functions","ok":true,"result":["io-handler"],"duration_ms":12},{"index":1,"service":"ec2","action":"list-instances","ok":false,"error":"throttling","duration_ms":5}]}`
	if string(first) != want {
		t.Errorf("first marshal:\n  got = %s\n want = %s", first, want)
	}
	var rt BatchResponse
	if err := json.Unmarshal(first, &rt); err != nil {
		t.Fatalf("BatchResponse Unmarshal: %v", err)
	}
	second, err := json.Marshal(rt)
	if err != nil {
		t.Fatalf("BatchResponse second Marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("round-trip drift:\n  first  = %s\n second = %s", first, second)
	}
}

// TestBatchResponse_OKIsHTTPEnvelopeNotAggregate pins the documented
// semantics of the outer OK field: it is the HTTP-envelope success
// bit, not an aggregate over Results[i].OK. Reliable's existing
// dispatchers (aws_inspect_batch.go:101, gcp_inspect_batch.go:76)
// always set outer OK=true on HTTP 200 even when some sub-probes
// failed. The doc on BatchResponse pins this explicitly; this test
// pins that the struct can in fact represent the
// "outer-OK-true-with-failed-sub" shape — a regression that flipped
// outer OK to an aggregate would not break this test directly (the
// aggregation would happen at the dispatcher), but encoding the
// shape here documents the contract in code so a future dispatcher
// author building against this type sees the canonical example.
func TestBatchResponse_OKIsHTTPEnvelopeNotAggregate(t *testing.T) {
	t.Parallel()
	in := BatchResponse{
		OK: true, // dispatcher ran to completion
		Results: []SubResult{
			{Index: 0, Service: "lambda", Action: "list-functions", OK: true, Result: []any{}, DurationMS: 10},
			{Index: 1, Service: "ec2", Action: "list-instances", OK: false, Error: "throttling", DurationMS: 5},
		},
	}
	got, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// The mixed-success batch must serialize with outer OK=true and
	// per-sub OK reflecting individual outcomes.
	want := `{"ok":true,"results":[{"index":0,"service":"lambda","action":"list-functions","ok":true,"result":[],"duration_ms":10},{"index":1,"service":"ec2","action":"list-instances","ok":false,"error":"throttling","duration_ms":5}]}`
	if string(got) != want {
		t.Errorf("\n  got = %s\n want = %s", got, want)
	}
	// Decode and assert outer OK is independent of inner OKs — pins
	// that the type does not enforce an aggregate constraint at the
	// data-model layer.
	var rt BatchResponse
	if err := json.Unmarshal(got, &rt); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !rt.OK {
		t.Error("outer OK must remain true on decode despite a failed sub-probe")
	}
	if rt.Results[1].OK {
		t.Error("Results[1].OK must remain false on decode")
	}
}

// TestBatchResponse_ResultsShape pins both shapes the encoder can emit
// for the Results field — the safe `[]` (initialized empty slice) and
// the unsafe `null` (nil slice). The MCP-server renderer iterates
// Results unconditionally, so a JSON null would cause a nil-deref or
// silent zero-iteration. The struct field has no `omitempty`, so:
//
//	Results: []SubResult{} → "results":[]    (safe)
//	Results: nil           → "results":null  (unsafe)
//
// The dispatcher's contract is to always initialize Results to a
// non-nil empty slice before responding. This test pins the OUTPUT
// shapes both paths produce so a future dispatcher author who returns
// a nil slice sees the unsafe-sentinel case fail and knows to
// initialize. (Same nil-vs-empty rule the discovery package enforces
// in pkg/observability/discovery/CONTRIBUTING.md per #255.)
func TestBatchResponse_ResultsShape(t *testing.T) {
	t.Parallel()
	t.Run("empty slice serializes as array", func(t *testing.T) {
		t.Parallel()
		in := BatchResponse{OK: true, Results: []SubResult{}}
		got, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		want := `{"ok":true,"results":[]}`
		if string(got) != want {
			t.Errorf("\n  got = %s\n want = %s", got, want)
		}
	})
	t.Run("nil slice serializes as null sentinel", func(t *testing.T) {
		t.Parallel()
		// SENTINEL — pin the unsafe shape so a future dispatcher
		// author (or anyone changing the struct definition) sees the
		// distinction. If we ever add `omitempty` to Results, OR
		// switch to a slice type that defaults non-nil, this test
		// fails and forces a deliberate decision rather than a
		// silent change.
		in := BatchResponse{OK: true, Results: nil}
		got, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		want := `{"ok":true,"results":null}`
		if string(got) != want {
			t.Errorf("\n  got = %s\n want = %s\n  (dispatcher must avoid this shape — initialize Results to []SubResult{} before responding)", got, want)
		}
	})
}
