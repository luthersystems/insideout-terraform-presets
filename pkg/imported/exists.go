package imported

import (
	"context"
	"errors"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// ExistenceVerdict classifies one resource's continued existence as
// determined by a per-type EnrichByID probe.
type ExistenceVerdict int

const (
	// ExistenceUnknown means the probe could not decide: the type has no
	// by-id enricher (ErrEnrichByIDNotImplemented), the SDK client was
	// unavailable, or the call errored transiently (throttle / auth /
	// network). Callers MUST treat this as "keep" — never a prune signal.
	ExistenceUnknown ExistenceVerdict = iota
	// ExistenceExists means the probe fetched the resource: it is live.
	ExistenceExists
	// ExistenceGone means the probe returned a definitive not-found: the
	// resource has been deleted from the cloud.
	ExistenceGone
)

// ClassifyExistence probes a single resource's existence via the provider's
// per-type EnrichByID and returns a verdict. It NEVER returns ExistenceGone
// for anything but a definitive ErrResourceNotFound — a type without a by-id
// enricher, an unavailable client, and any transient/auth error all return
// ExistenceUnknown, because dropping a live resource (false-drop) is far worse
// than keeping a dead one (which surfaces loudly at terraform apply). p and
// identity must be non-nil.
func ClassifyExistence(ctx context.Context, p Provider, clients Clients, identity *composerimported.ResourceIdentity) ExistenceVerdict {
	if p == nil || identity == nil {
		return ExistenceUnknown
	}
	if _, err := p.EnrichByID(ctx, identity, clients); err != nil {
		if errors.Is(err, ErrResourceNotFound) {
			return ExistenceGone
		}
		return ExistenceUnknown
	}
	return ExistenceExists
}

// FilterExisting probes each identity and returns the subset that is
// DEFINITELY gone (ClassifyExistence == ExistenceGone), preserving input
// order. Resources that still exist, whose type has no by-id enricher, or that
// error transiently are treated as inconclusive and are NOT returned — the
// caller keeps them. This is the cloud-agnostic existence-prune primitive the
// InsideOut backend uses to drop `import{}` blocks for reverse-import
// carry-forward resources that have been deleted from the cloud since they
// were adopted (which would otherwise abort the whole terraform apply with
// "Cannot import non-existent remote object").
//
// The probe is one EnrichByID per identity (a per-type describe/get); a
// not-found short-circuits cheaply, while a live resource pays the enrich
// cost. Callers that need throughput can shard the slice and call
// FilterExisting concurrently — each call is independent and holds no shared
// state. A nil identity in the slice is skipped. err is returned only for a
// caller-fatal condition (nil provider); per-resource probe errors are folded
// into "keep", never a batch failure.
func FilterExisting(ctx context.Context, p Provider, clients Clients, identities []*composerimported.ResourceIdentity) (gone []*composerimported.ResourceIdentity, err error) {
	if p == nil {
		return nil, errors.New("imported: FilterExisting called with nil provider")
	}
	for _, id := range identities {
		if id == nil {
			continue
		}
		if ClassifyExistence(ctx, p, clients, id) == ExistenceGone {
			gone = append(gone, id)
		}
	}
	return gone, nil
}
