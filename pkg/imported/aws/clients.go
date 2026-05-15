// Package aws is the AWS-side pkg/imported.Provider implementation.
// It depends on cmd/insideout-import/awsdiscover for the live cloud
// dispatch; the top-level pkg/imported package does NOT import this
// package — see pkg/imported package doc for the dependency direction.
package aws

import (
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
)

// Clients carries the AWS-side SDK client bundle this Provider
// dispatches against. Re-exported as a type alias from
// awsdiscover.EnrichClients so callers depending on pkg/imported/aws
// see one canonical name. The aws Provider unwraps a
// pkg/imported.Clients{AWS: <*Clients>} into this concrete shape
// before threading it through to awsdiscover.
//
// Construct one per discover run; the SDK clients are stateless
// wrappers over an aws.Config. A nil field is tolerated: per-type
// enrichers whose required client is nil return
// awsdiscover.ErrEnrichClientUnavailable, which the Provider surfaces
// as imported.ErrEnrichClientUnavailable on the cross-cloud boundary.
type Clients = awsdiscover.EnrichClients
