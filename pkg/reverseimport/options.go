// Package reverseimport contains the reusable reverse-import engine used by
// the local CLI and, later, the Mars Go job binary.
package reverseimport

import (
	"context"
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

	deps deps
}

type deps struct {
	runGenconfig func(context.Context, genconfig.Options, []imported.ImportedResource) (*genconfig.Result, error)
	runDriftfix  func(context.Context, driftfix.Options) (*driftfix.Result, error)
	runDepChase  func(context.Context, depchase.Options, []imported.ImportedResource) (*depchase.Result, error)
	tf           terraformRunner
}

func defaultDeps(terraformBinary string) deps {
	return deps{
		runGenconfig: genconfig.Run,
		runDriftfix:  driftfix.Run,
		runDepChase:  depchase.Run,
		tf:           execTerraformRunner{binary: terraformBinary},
	}
}

func (o Options) withDefaults() Options {
	if o.MaxDepChaseIterations <= 0 {
		o.MaxDepChaseIterations = depchase.DefaultMaxIterations
	}
	if o.deps.runGenconfig == nil ||
		o.deps.runDriftfix == nil ||
		o.deps.runDepChase == nil ||
		o.deps.tf == nil {
		o.deps = defaultDeps(o.TerraformBinary)
	}
	return o
}
