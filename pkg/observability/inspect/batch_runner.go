// Bounded fan-out for batch inspect requests. Each cloud-specific
// dispatcher (Dispatcher.AWSBatch, Dispatcher.GCPBatch) fetches
// credentials once and then dispatches up to MaxBatchSubs probes in
// parallel via runSubsBounded, which caps concurrency, applies a
// per-sub timeout, and recovers panics. Per-sub failures are encoded
// inside SubResult — runSubsBounded never returns a Go error so
// partial-success is the normal case.
//
// Lifted from reliable/internal/agentapi/inspect_batch_common.go:11-155
// verbatim. MaxBatchSubs already lives in types.go (PR #277).
package inspect

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// DefaultBatchConcurrency is the parallelism ceiling for per-batch
	// fan-out. Empirically the cred fetch + AWS/GCP SDK HTTP calls
	// saturate well under 8 — higher concurrency mostly stacks latency,
	// lower leaves throughput on the table.
	DefaultBatchConcurrency = 8

	// DefaultPerSubTimeout bounds wall-clock for a single probe. A
	// single slow service (e.g. a laggy CloudFront list-distributions)
	// must not pin the whole batch.
	DefaultPerSubTimeout = 30 * time.Second
)

// DefaultBatchWallClock is the total wall-clock ceiling for one batch
// call. With DefaultPerSubTimeout=30s and DefaultBatchConcurrency=8
// the worst-case serial-tail for MaxBatchSubs=32 is 4×30s = 120s, so
// we clamp at 60s to keep a batch from accidentally outliving the
// caller's HTTP function timeout. Enforced by wrapping the caller ctx
// in Dispatcher.AWSBatch / GCPBatch; remaining subs past the deadline
// return with "timeout" error, already-finished subs are returned
// intact.
//
// Declared as a var (not const) solely so the wall-clock enforcement
// tests can drop the ceiling to milliseconds and avoid a 60-second
// test run. Production code must never mutate it — the `var` form is
// load-bearing only for tests.
var DefaultBatchWallClock = 60 * time.Second

// batchFanOut is the per-sub dispatcher supplied by the cloud-specific
// caller. It's invoked under a per-sub context.WithTimeout inside
// runSubsBounded.
type batchFanOut func(ctx context.Context, idx int, sub SubRequest) SubResult

// runSubsBounded runs each sub with at most `concurrency` goroutines,
// applying perSubTimeout to each call. Results are returned in input
// order. The function never returns an error: panics and timeouts are
// captured into SubResult.Error.
//
// Non-positive concurrency falls back to DefaultBatchConcurrency;
// non-positive perSubTimeout falls back to DefaultPerSubTimeout. This
// keeps callsites tolerant to zero-valued config structs in tests.
func runSubsBounded(ctx context.Context, subs []SubRequest, concurrency int, perSubTimeout time.Duration, fn batchFanOut) []SubResult {
	if concurrency <= 0 {
		concurrency = DefaultBatchConcurrency
	}
	if perSubTimeout <= 0 {
		perSubTimeout = DefaultPerSubTimeout
	}

	results := make([]SubResult, len(subs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for i := range subs {
		sub := subs[i]
		g.Go(func() error {
			subCtx, cancel := context.WithTimeout(gctx, perSubTimeout)
			defer cancel()
			results[i] = runOneSub(subCtx, i, sub, fn)
			return nil
		})
	}

	// Errgroup returns nil here — no goroutine ever returns an error.
	_ = g.Wait()
	return results
}

// runOneSub is the panic-and-timing-safe wrapper around fn. Extracted
// so the defer/recover stack is per-sub and a crash in one sub can't
// take down sibling goroutines.
func runOneSub(ctx context.Context, idx int, sub SubRequest, fn batchFanOut) (r SubResult) {
	start := time.Now()
	defer func() {
		if p := recover(); p != nil {
			r = SubResult{
				Index:   idx,
				Service: sub.Service,
				Action:  sub.Action,
				OK:      false,
				Error:   fmt.Sprintf("panic: %v\n%s", p, debug.Stack()),
			}
		}
		r.DurationMS = time.Since(start).Milliseconds()
		// Ensure index/service/action always populated even if fn
		// returned a zero-value SubResult (defensive; callers should
		// set them).
		if r.Index == 0 && idx != 0 {
			r.Index = idx
		}
		if r.Service == "" {
			r.Service = sub.Service
		}
		if r.Action == "" {
			r.Action = sub.Action
		}
	}()
	r = fn(ctx, idx, sub)
	// Translate a context timeout the fn surfaced as a generic error
	// into an explicit "timeout" marker so callers can distinguish.
	if !r.OK && r.Error != "" && ctx.Err() == context.DeadlineExceeded {
		r.Error = "timeout: " + r.Error
	}
	return r
}
