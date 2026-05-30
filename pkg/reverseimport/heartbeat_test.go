package reverseimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

// safeBuffer is a bytes.Buffer guarded for concurrent Write/String so a
// test can read the progress stream from one goroutine while the heartbeat
// goroutine writes from another, under -race.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForContains blocks until b contains sub or within elapses. Returning
// on the condition (rather than a fixed sleep) keeps the heartbeat tests
// bounded by the tiny test interval, not by a worst-case wait; the timeout
// is only a failure backstop so a regression fails fast instead of hanging.
func waitForContains(b *safeBuffer, sub string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), sub) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return strings.Contains(b.String(), sub)
}

// TestRunPhaseEmitsHeartbeatDuringSlowPhase is the core regression guard
// for issue #702: a phase that runs longer than the heartbeat interval must
// emit a "still running" liveness line so a consumer streaming the job log
// sees forward motion instead of a frozen stream.
func TestRunPhaseEmitsHeartbeatDuringSlowPhase(t *testing.T) {
	var buf safeBuffer
	opts := Options{Stdout: &buf, heartbeatEvery: 2 * time.Millisecond}

	release := make(chan struct{})
	go func() {
		// End the phase as soon as the first heartbeat is observed, so the
		// test is bounded by the 2ms interval; the 2s ceiling is a backstop.
		waitForContains(&buf, "still running", 2*time.Second)
		close(release)
	}()

	called := false
	err := opts.runPhase("terraform init", func() error {
		called = true
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("runPhase returned error: %v", err)
	}
	if !called {
		t.Fatal("phase fn was never called")
	}
	got := buf.String()
	if !strings.Contains(got, "terraform init still running") {
		t.Fatalf("missing heartbeat for slow phase; got:\n%s", got)
	}
	if !strings.Contains(got, "elapsed") {
		t.Fatalf("heartbeat missing elapsed marker; got:\n%s", got)
	}
}

// TestRunPhaseStaysSilentForFastPhase pins the no-noise side: a phase that
// returns before the first tick must emit nothing, so fast phases (validate,
// show on a small plan) don't spam the log with heartbeats.
func TestRunPhaseStaysSilentForFastPhase(t *testing.T) {
	var buf safeBuffer
	opts := Options{Stdout: &buf, heartbeatEvery: time.Hour}
	if err := opts.runPhase("terraform validate", func() error { return nil }); err != nil {
		t.Fatalf("runPhase returned error: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("fast phase emitted unexpected output: %q", got)
	}
}

// TestRunPhasePropagatesError confirms runPhase is a transparent wrapper:
// fn's error is returned unchanged, so wrapping a phase never swallows or
// rewrites its failure.
func TestRunPhasePropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	opts := Options{Stdout: &safeBuffer{}, heartbeatEvery: time.Hour}
	err := opts.runPhase("driftfix", func() error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("runPhase err = %v, want %v", err, wantErr)
	}
}

// TestRunPhaseDisabledWhenIntervalNonPositive pins the passthrough: a
// non-positive interval disables the heartbeat entirely (no goroutine, no
// output), so a caller can opt out cleanly.
func TestRunPhaseDisabledWhenIntervalNonPositive(t *testing.T) {
	var buf safeBuffer
	opts := Options{Stdout: &buf, heartbeatEvery: 0}
	ran := false
	if err := opts.runPhase("terraform init", func() error { ran = true; return nil }); err != nil {
		t.Fatalf("runPhase returned error: %v", err)
	}
	if !ran {
		t.Fatal("phase fn was never called")
	}
	if got := buf.String(); got != "" {
		t.Fatalf("disabled heartbeat emitted output: %q", got)
	}
}

// TestRunPhaseHeartbeatIsPeriodicAndLabeled pins two things the single-tick
// slow-phase test leaves open: the heartbeat fires repeatedly (not just once
// before the ticker is mistakenly stopped), and the line carries the caller's
// label rather than a hardcoded phase name — here "driftfix", distinct from
// the "terraform init" the other tests use.
func TestRunPhaseHeartbeatIsPeriodicAndLabeled(t *testing.T) {
	var buf safeBuffer
	opts := Options{Stdout: &buf, heartbeatEvery: 2 * time.Millisecond}

	release := make(chan struct{})
	go func() {
		// Hold the phase open until at least two heartbeats have fired, so
		// "periodic" is actually proven; 2s is a failure backstop only.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Count(buf.String(), "still running") >= 2 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		close(release)
	}()

	if err := opts.runPhase("driftfix", func() error {
		<-release
		return nil
	}); err != nil {
		t.Fatalf("runPhase returned error: %v", err)
	}
	got := buf.String()
	if n := strings.Count(got, "still running"); n < 2 {
		t.Fatalf("expected >=2 periodic heartbeats, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, "driftfix still running") {
		t.Fatalf("heartbeat did not carry the provided label %q:\n%s", "driftfix", got)
	}
	if strings.Contains(got, "terraform init") {
		t.Fatalf("heartbeat label looks hardcoded, not threaded from the caller:\n%s", got)
	}
}

// TestRunPhasePanicReapsHeartbeatGoroutine guards the panic-cleanup path that
// the production comment promises: runPhase joins the heartbeat goroutine via
// a deferred close(done)+<-stopped, so a panic out of fn re-surfaces AND the
// goroutine is reaped rather than left ticking. The regression this kills is
// moving the cleanup out of the defer — then a panicking fn would leak the
// goroutine, which keeps emitting "still running" lines after the unwind.
func TestRunPhasePanicReapsHeartbeatGoroutine(t *testing.T) {
	var buf safeBuffer
	opts := Options{Stdout: &buf, heartbeatEvery: time.Millisecond}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate out of runPhase")
			}
		}()
		_ = opts.runPhase("terraform init", func() error {
			waitForContains(&buf, "still running", 2*time.Second)
			panic("boom")
		})
	}()

	// runPhase's deferred close(done)+<-stopped runs during the panic unwind
	// and blocks until the goroutine exits, so the goroutine is already
	// joined by the time recover() returns above. A leaked goroutine (the
	// regression) would keep ticking at ~1ms; assert the stream is now
	// quiescent across many would-be ticks.
	afterPanic := buf.String()
	settle := time.Now().Add(60 * time.Millisecond)
	for time.Now().Before(settle) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := buf.String(); got != afterPanic {
		t.Fatalf("heartbeat goroutine kept emitting after panic — not reaped:\nbefore=%q\nafter=%q", afterPanic, got)
	}
}

// TestSyncWriterSerializesConcurrentWrites proves the engine's shared
// progress sink stays line-atomic under concurrent producers — the
// guarantee that lets the heartbeat goroutine and the streamed terraform
// output share one Options.Stdout without interleaving mid-line.
func TestSyncWriterSerializesConcurrentWrites(t *testing.T) {
	var underlying bytes.Buffer
	sw := &syncWriter{w: &underlying}

	const writers, perWriter = 8, 64
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			line := fmt.Appendf(nil, "line-from-%d\n", id)
			for range perWriter {
				if _, err := sw.Write(line); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	// Safe to read underlying directly now: all writers have joined.
	lines := strings.Split(strings.TrimRight(underlying.String(), "\n"), "\n")
	if len(lines) != writers*perWriter {
		t.Fatalf("got %d lines, want %d", len(lines), writers*perWriter)
	}
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "line-from-") {
			t.Fatalf("interleaved/garbled line: %q", ln)
		}
	}
}

// blockingInitRunner wraps the fake terraform runner but holds Init open
// until release is closed, so a test can keep the init phase running long
// enough to observe the heartbeat the engine emits during a slow init.
type blockingInitRunner struct {
	fakeTerraformRunner
	release <-chan struct{}
}

func (r blockingInitRunner) Init(context.Context, string) error {
	<-r.release
	return nil
}

// TestRunEmitsHeartbeatDuringSlowInit is the end-to-end guard that the
// heartbeat is actually wired into Run's terraform-init phase and reaches
// the Options.Stdout the Mars job supplies — not just exercised in isolation.
func TestRunEmitsHeartbeatDuringSlowInit(t *testing.T) {
	dir := t.TempDir()
	req := job.Request{
		Version: job.Version,
		Resources: []job.ResourceSpec{{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.orders",
				ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders",
				Region:   "us-east-1",
			},
			Tier:   imported.TierImportedFlat,
			Source: imported.SourceImporter,
		}},
	}

	var buf safeBuffer
	release := make(chan struct{})
	go func() {
		waitForContains(&buf, "terraform init still running", 3*time.Second)
		close(release)
	}()

	_, err := Run(context.Background(), req, Options{
		OutputDir:      dir,
		SkipDepChase:   true,
		Stdout:         &buf,
		heartbeatEvery: 2 * time.Millisecond,
		deps: deps{
			runGenconfig: fakeGenconfig,
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           blockingInitRunner{release: release},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "terraform init still running") {
		t.Fatalf("expected terraform-init heartbeat in progress stream; got:\n%s", got)
	}
}
