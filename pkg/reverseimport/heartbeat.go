package reverseimport

import (
	"io"
	"sync"
	"time"
)

// defaultHeartbeatInterval is how often a long-running phase emits a
// "still running" liveness line to the progress sink. Chosen at the
// responsive end of issue #702's "~15-30s" window: a 2-5 minute
// terraform-init (provider download + GPG verify) or driftfix phase then
// produces ~8-20 heartbeat lines, so a consumer streaming the job log
// (reliable's onboard import plan-preview) never sees more than ~15s of
// apparent silence and can't mistake the run for stuck/broken.
const defaultHeartbeatInterval = 15 * time.Second

// runPhase executes fn while a background goroutine emits a periodic
// heartbeat line to o.Stdout, so a consumer streaming the job log sees
// forward motion even through phases whose subprocess output is buffered
// or fully silent for minutes (issue #702). terraform's own provider
// download/GPG-verify during init, and driftfix's plan-refresh cycles, are
// the worst offenders: terraform buffers that output and the engine has no
// other line to print until the phase returns.
//
// The first heartbeat fires only after o.heartbeatEvery elapses, so a fast
// phase stays quiet and produces zero extra lines; the ticker stops the
// moment fn returns. fn's own error is returned unchanged — runPhase is a
// transparent wrapper. label names the phase in the heartbeat line and
// should echo the human-readable phase name progressf prints at the phase
// boundary (e.g. "terraform init", "driftfix") — close enough that the
// heartbeat reads as a continuation of the same phase.
//
// o.Stdout is serialized by withDefaults (see syncWriter), so the heartbeat
// goroutine, the per-phase progress lines, and the streamed terraform
// subprocess output never corrupt each other on the shared writer.
func (o Options) runPhase(label string, fn func() error) error {
	if o.heartbeatEvery <= 0 {
		return fn()
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(o.heartbeatEvery)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// Re-check done so a tick that races phase completion
				// doesn't print a spurious heartbeat after the phase has
				// already finished.
				select {
				case <-done:
					return
				default:
				}
				o.progressf("reverse-import: %s still running (%s elapsed)…\n", label, time.Since(start).Round(time.Second))
			}
		}
	}()
	// Stop and join the heartbeat on every exit path — including a panic
	// unwinding out of fn — so the goroutine is reaped rather than left
	// blocked in its select.
	defer func() {
		close(done)
		<-stopped
	}()
	return fn()
}

// syncWriter serializes concurrent writes to an underlying sink. The
// reverse-import engine shares one Options.Stdout across three concurrent
// producers — the heartbeat goroutine, the per-phase progress lines, and
// the streamed terraform subprocess output — and many real sinks (a
// bufio.Writer, a custom log writer) are not safe for concurrent use, so
// the engine guarantees serialization itself rather than assuming the
// caller's writer is goroutine-safe.
//
// Each Write is atomic with respect to every other Write. The engine's own
// progress and heartbeat lines are emitted one whole line per Write, so
// they are never split or interleaved mid-line. The streamed terraform
// output, by contrast, reaches this writer via os/exec's io.Copy in
// ~32KB chunks that are not newline-aligned, so a heartbeat line can land
// between two chunks of a terraform line — cosmetic only (never a race or
// data corruption), and acceptable given heartbeats are seconds apart.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
