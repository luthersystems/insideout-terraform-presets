// Package awsdiscover — AWS-side attribute enrichment (#457).
//
// Mirrors the GCP-side AttributeEnricher / EnrichClients / EnrichAttributes
// surface in cmd/insideout-import/gcpdiscover/enrich.go so the two clouds
// expose a symmetric contract to the discover orchestrator. See that file's
// doc-comment for the rationale (Stage 2b terraform-binary-free path,
// ir.Attrs vs ir.Attributes, decision-#34 clean HCL, ErrEnrichClientUnavailable
// downgrade semantics).
//
// Bundle scope: aws_dynamodb_table lands first because its Layer 1 typed
// struct already exists in pkg/composer/imported/generated. aws_s3_bucket
// follows once presets bundle #461 adds it to WantedAWS — the same
// dispatcher + EnrichClients infrastructure will pick it up with a one-line
// byTypeEnricher registration.
package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// enrichServiceSlug is the progress-event service slug for the SDK
// attribute-enrichment phase. Distinct from per-discoverer slugs so
// progress consumers can attribute events correctly when EnrichAttributes
// runs interleaved with or after DiscoverTypes. Mirrors the GCP-side
// gcpdiscover.enrichServiceSlug ("gcp_sdk_enrich").
const enrichServiceSlug = "aws_sdk_enrich"

// AttributeEnricher is the per-resource-type contract for populating
// `imported.ImportedResource.Attrs` (the typed Layer 1 payload) from a
// cloud-side SDK Describe / Get call. Sibling to Discoverer: Discoverers
// produce Identity-only records via Cloud Control (or the hand-rolled
// per-service path), and AttributeEnrichers later turn each into a fully-
// populated record by issuing per-type AWS SDK calls and writing the
// typed result into ir.Attrs.
//
// Why this exists: Stage 2b's `terraform plan -generate-config-out`
// path needs a `terraform` binary in $PATH and several round-trips per
// resource. UI/SaaS consumers (e.g. luthersystems/reliable's Vercel
// handler in #1346) can't shell out to terraform under their runtime
// constraints; this is the terraform-binary-free path that produces
// decision-#34-clean HCL via composer.EmitImportedTF. Mirrors the
// gcpdiscover.AttributeEnricher contract (presets#403); this is the
// AWS-side parity (presets#457).
//
// Per-type enrichers map their cloud SDK response struct into the
// matching pkg/composer/imported/generated.AWS<Type> typed model and
// JSON-marshal it into ir.Attrs. Per decision #5 computed-only fields
// are not populated; per decision #36 sensitive fields follow the
// downstream redaction policy.
type AttributeEnricher interface {
	// ResourceType returns the Terraform type this enricher covers,
	// e.g. "aws_dynamodb_table". Must match the registered Discoverer
	// of the same type.
	ResourceType() string

	// Enrich fetches live cloud-side state for ir (whose Identity is
	// already populated by the corresponding Discoverer) and writes
	// the typed payload into ir.Attrs. The enricher must not touch
	// ir.Identity. clients carries the SDK clients the enricher
	// needs; a nil required client is reported as
	// ErrEnrichClientUnavailable so callers can distinguish "not
	// configured" from "real API error".
	Enrich(ctx context.Context, ir *imported.ImportedResource, clients EnrichClients) error
}

// EnrichClients bundles the AWS SDK clients per-type enrichers dispatch
// to. Construct once per discover run (the lifecycle is owned by the
// caller — the AWS SDK clients are stateless wrappers over an aws.Config,
// so callers can construct/discard freely).
//
// A nil field is tolerated: enrichers whose required client is nil
// return ErrEnrichClientUnavailable, which EnrichAttributes surfaces as
// a per-resource progress.ServiceWarn rather than failing the whole
// batch.
//
// AccountID parallels gcpdiscover.EnrichClients.ProjectID — it's the
// AWS account ID resolved out-of-band (typically via STS
// GetCallerIdentity at the start of the discover run) and threaded
// through here for enrichers that need to construct ARNs or stamp
// account-scoped fields without an extra STS round-trip per resource.
// Empty is tolerated when no enricher uses it (today: DynamoDB doesn't
// need it because TableArn comes back in DescribeTable directly).
type EnrichClients struct {
	S3        *s3.Client
	DynamoDB  *dynamodb.Client
	AccountID string
}

// ErrEnrichClientUnavailable signals that the SDK client an enricher
// needs is nil on EnrichClients. Distinguishable from a real API error
// so EnrichAttributes can downgrade it to a per-resource warning (the
// type is silently un-enriched in the output) instead of a batch-fatal
// error. Mirrors gcpdiscover.ErrEnrichClientUnavailable.
var ErrEnrichClientUnavailable = errors.New("enrich: required SDK client unavailable on EnrichClients")

// EnrichAttributes populates ir.Attrs in place for every imported
// resource whose Identity.Type has a registered enricher. Resources of
// types without a registered enricher are left untouched; the caller
// can detect this via len(ir.Attrs) == 0 on return.
//
// Errors are accumulated per-resource and surfaced together at the end
// so a single mid-batch failure doesn't lose results from earlier
// successful enrichments — a partial result is more useful than no
// result. ErrEnrichClientUnavailable failures are downgraded to
// progress.ServiceWarn events (and not included in the returned error)
// since they reflect caller-side configuration, not API failures. The
// returned error wraps the joined per-resource errors; callers may
// inspect via errors.Is / errors.As.
//
// emitter receives ItemFound per successfully enriched resource and
// ServiceWarn per ErrEnrichClientUnavailable. The standard
// (ServiceStart, ServiceFinish) pair brackets the whole batch under
// enrichServiceSlug. A nil emitter falls back to progress.NopEmitter.
//
// Mirrors gcpdiscover.GCPDiscoverer.EnrichAttributes — same dispatch
// order (sort by Identity.Type then Identity.Address), same error
// aggregation, same progress semantics. Symmetric APIs across clouds
// keep the consumer-side code (reliable's buildEnrichedAWSImports /
// buildEnrichedGCSImports) a one-liner per cloud.
func (a *AWSDiscoverer) EnrichAttributes(ctx context.Context, irs []imported.ImportedResource, clients EnrichClients, emitter progress.Emitter) error {
	if emitter == nil {
		emitter = progress.NopEmitter{}
	}
	stageStart := time.Now()
	emitter.ServiceStart(enrichServiceSlug, "")
	defer func() { emitter.ServiceFinish(enrichServiceSlug, "", len(irs), time.Since(stageStart)) }()

	// Dispatch in deterministic order so progress events and any
	// emitted warnings are stable across runs. Matches the GCP
	// (type, address) ordering.
	idx := make([]int, 0, len(irs))
	for i := range irs {
		if _, ok := a.byTypeEnricher[irs[i].Identity.Type]; ok {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(x, y int) bool {
		ix, iy := idx[x], idx[y]
		if irs[ix].Identity.Type != irs[iy].Identity.Type {
			return irs[ix].Identity.Type < irs[iy].Identity.Type
		}
		return irs[ix].Identity.Address < irs[iy].Identity.Address
	})

	var errs []error
	for _, i := range idx {
		enr := a.byTypeEnricher[irs[i].Identity.Type]
		err := enr.Enrich(ctx, &irs[i], clients)
		switch {
		case err == nil:
			emitter.ItemFound(enrichServiceSlug, irs[i].Identity.Region, irs[i].Identity.Type, irs[i].Identity.ImportID)
		case errors.Is(err, ErrEnrichClientUnavailable):
			// Client-side configuration failure — surface as a warn
			// but don't accumulate as an error. Mirrors the GCP-side
			// downgrade semantics.
			emitter.ServiceWarn(enrichServiceSlug, "", fmt.Sprintf("%s/%s: %v", irs[i].Identity.Type, irs[i].Identity.Address, err))
		default:
			errs = append(errs, fmt.Errorf("enrich %s/%s: %w", irs[i].Identity.Type, irs[i].Identity.Address, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
