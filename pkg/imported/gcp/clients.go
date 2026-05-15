// Package gcp is the GCP-side pkg/imported.Provider implementation.
// It depends on cmd/insideout-import/gcpdiscover for the live cloud
// dispatch; the top-level pkg/imported package does NOT import this
// package — see pkg/imported package doc for the dependency direction.
package gcp

import (
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
)

// Clients carries the GCP-side SDK client bundle this Provider
// dispatches against. Re-exported as a type alias from
// gcpdiscover.EnrichClients — see pkg/imported/aws.Clients for the
// AWS-side parity rationale.
type Clients = gcpdiscover.EnrichClients
