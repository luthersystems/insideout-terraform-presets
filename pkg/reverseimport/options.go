// Package reverseimport contains the reusable reverse-import engine used by
// the local CLI and, later, the Mars Go job binary.
package reverseimport

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/driftfix"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Discoverer is the dependency-resolution surface needed by the dep-chase
// phase. Mars and the local CLI can provide a cloud-backed implementation;
// tests can provide a fake.
type Discoverer interface {
	DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error)
}

// ClosureRequest describes the selected parent resources whose scoped
// children should be discovered before provider readback.
type ClosureRequest struct {
	Cloud           string
	Project         string
	Regions         []string
	AccountID       string
	GCPProjectID    string
	ParentResources []imported.ImportedResource
	ParentTypes     []string
	ChildTypes      []string
}

// ClosureDiscoverer is the optional parent-selection expansion surface.
// The local CLI implements this by calling the same cloud discoverer used for
// top-level discovery; Mars can wrap the same SDK-backed discoverer without
// shelling out to CLI-private code.
type ClosureDiscoverer interface {
	DiscoverClosure(ctx context.Context, req ClosureRequest) ([]imported.ImportedResource, error)
}

// Options configures a reverse-import run.
type Options struct {
	OutputDir string
	Workdir   string

	Cloud          string
	Region         string
	GCPProjectID   string
	AWSEndpointURL string

	ImportProjectID string
	ImportSessionID string
	ImportedAt      time.Time
	DiscoverProject string
	DiscoverRegions []string
	AccountID       string

	TerraformBinary       string
	SkipDriftFix          bool
	SkipDepChase          bool
	MaxDepChaseIterations int
	Discoverer            Discoverer
	ClosureDiscoverer     ClosureDiscoverer

	// Stdout receives continuous human-readable progress as Run works
	// through its phases — provider readback, genconfig, driftfix,
	// dep-chase, and the final terraform init/validate/plan — plus the
	// live stdout/stderr of the terraform subprocesses those phases
	// drive and a periodic per-phase heartbeat during the silent stretches
	// (terraform init provider download/GPG-verify, driftfix plan refresh).
	// The Mars reverse-import job points this at its own stdout so Oracle's
	// follow=1 stream (and the InsideOut import wizard's log console) shows
	// live progress for the whole run rather than just the final plan. Nil
	// defaults to io.Discard in withDefaults, so existing callers and tests
	// stay silent and unaffected.
	Stdout io.Writer

	// heartbeatEvery is the interval between "still running" liveness lines
	// emitted during a long phase (see runPhase). withDefaults sets it to
	// defaultHeartbeatInterval; tests override it to a tiny value to assert
	// heartbeat behavior deterministically. A non-positive value disables
	// the heartbeat entirely (runPhase becomes a transparent passthrough).
	heartbeatEvery time.Duration

	deps deps
}

type deps struct {
	runGenconfig func(context.Context, genconfig.Options, []imported.ImportedResource) (*genconfig.Result, error)
	runDriftfix  func(context.Context, driftfix.Options) (*driftfix.Result, error)
	runDepChase  func(context.Context, depchase.Options, []imported.ImportedResource) (*depchase.Result, error)
	tf           terraformRunner
}

func defaultDeps(terraformBinary string, stdout io.Writer) deps {
	return deps{
		runGenconfig: genconfig.Run,
		runDriftfix:  driftfix.Run,
		runDepChase:  depchase.Run,
		tf:           execTerraformRunner{binary: terraformBinary, stdout: stdout},
	}
}

// progressf writes a human-readable phase-progress line to o.Stdout.
// withDefaults guarantees o.Stdout is non-nil (io.Discard at minimum), so
// callers after withDefaults never need a nil check; the guard here keeps
// a zero-value Options safe too. Best-effort: a write error to the
// progress sink must never affect the run result, so it is ignored.
func (o Options) progressf(format string, args ...any) {
	if o.Stdout == nil {
		return
	}
	fmt.Fprintf(o.Stdout, format, args...)
}

func (o Options) withDefaults() Options {
	if o.MaxDepChaseIterations <= 0 {
		o.MaxDepChaseIterations = depchase.DefaultMaxIterations
	}
	if o.heartbeatEvery <= 0 {
		o.heartbeatEvery = defaultHeartbeatInterval
	}
	// Default the progress sink to io.Discard so progressf and the
	// terraform subprocess streaming are always safe to call without a
	// nil check, and existing callers/tests that pass no writer stay
	// silent.
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	// Serialize the shared progress sink: the heartbeat goroutine, the
	// per-phase progress lines, and the streamed terraform subprocess
	// output all write to o.Stdout concurrently, and the caller's writer is
	// not assumed goroutine-safe. io.Discard needs no guarding and the
	// wrap would only add a pointless mutex, so skip it.
	if o.Stdout != io.Discard {
		o.Stdout = &syncWriter{w: o.Stdout}
	}
	if o.deps.runGenconfig == nil ||
		o.deps.runDriftfix == nil ||
		o.deps.runDepChase == nil ||
		o.deps.tf == nil {
		o.deps = defaultDeps(o.TerraformBinary, o.Stdout)
	}
	return o
}
