// Batch dispatchers for AWS+GCP. Lifted from
// reliable/internal/agentapi/aws_inspect_batch.go +
// gcp_inspect_batch.go. Each entry point fetches credentials once for
// the batch, then runs up to MaxBatchSubs sub-probes through
// runSubsBounded with the cred-less list-actions / list-metrics paths
// short-circuiting around the cred fetch.
//
// The two methods are deliberately near-mirrors of each other so a
// single design change (e.g. a new cred-less action) lands in both
// clouds in lock-step.
package inspect

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// AWSBatch dispatches multiple AWS inspect probes for one session.
// Fetches credentials once and only if at least one sub needs them.
// Returns results in input order; per-sub errors are encoded in
// SubResult.Error (never as the top-level error).
//
// Wall-clock budget: ctx is wrapped in DefaultBatchWallClock before
// fan-out. Subs that are still running when the deadline fires see
// their per-sub ctx cancel (deadline propagates through
// errgroup.WithContext) and surface as
// {ok:false, error:"timeout: ..."} in the results slice;
// already-completed subs are returned intact.
func (d *Dispatcher) AWSBatch(ctx context.Context, sessionID string, subs []SubRequest) ([]SubResult, error) {
	if len(subs) == 0 {
		return nil, fmt.Errorf("empty_batch")
	}
	if len(subs) > MaxBatchSubs {
		return nil, fmt.Errorf("too_many_subs: got %d, max %d", len(subs), MaxBatchSubs)
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultBatchWallClock)
	defer cancel()

	// Partition: cred-less subs run without touching the cred broker.
	// If every sub is cred-less (e.g., a pure list-actions discovery
	// batch), we skip both ProjectResolver and CredsProvider entirely.
	needsCreds := false
	for _, sub := range subs {
		if !isAWSCredlessSub(sub) {
			needsCreds = true
			break
		}
	}

	// Best-effort credential fetch. Errors here are non-fatal: we
	// attach them to every needs-creds sub as the wrapped error, but
	// cred-less subs still execute.
	var (
		awsCfg  aws.Config
		credErr error
	)
	if needsCreds {
		projectID, _, err := d.Resolver.ResolveSession(ctx, sessionID)
		if err != nil {
			credErr = fmt.Errorf("project_lookup_failed: %v", err)
		} else {
			creds, err := d.Creds.AWS(ctx, projectID)
			if err != nil {
				// %w preserves typed envelopes — see dispatcher.go
				// AWS path for the symmetry rationale.
				credErr = fmt.Errorf("credential_fetch_failed: %w", err)
			} else {
				cfg, err := d.awsConfig(ctx, creds)
				if err != nil {
					credErr = fmt.Errorf("aws_config_failed: %v", err)
				} else {
					awsCfg = cfg
				}
			}
		}
	}

	fn := func(ctx context.Context, idx int, sub SubRequest) SubResult {
		return d.dispatchAWSBatchSub(ctx, idx, sub, sessionID, awsCfg, credErr)
	}
	return runSubsBounded(ctx, subs, DefaultBatchConcurrency, DefaultPerSubTimeout, fn), nil
}

// GCPBatch is the GCP twin of AWSBatch. Adds a cloud-mismatch check
// (must equal "gcp") before any cred fetch, and applies protoNormalize
// to per-sub results inside dispatchGCPBatchSub.
func (d *Dispatcher) GCPBatch(ctx context.Context, sessionID string, subs []SubRequest) ([]SubResult, error) {
	if len(subs) == 0 {
		return nil, fmt.Errorf("empty_batch")
	}
	if len(subs) > MaxBatchSubs {
		return nil, fmt.Errorf("too_many_subs: got %d, max %d", len(subs), MaxBatchSubs)
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultBatchWallClock)
	defer cancel()

	needsCreds := false
	for _, sub := range subs {
		if !isGCPCredlessSub(sub) {
			needsCreds = true
			break
		}
	}

	var (
		creds   *GCPCreds
		credErr error
	)
	if needsCreds {
		projectID, cloud, err := d.Resolver.ResolveSession(ctx, sessionID)
		switch {
		case err != nil:
			credErr = fmt.Errorf("project_lookup_failed: %v", err)
		case !strings.EqualFold(cloud, "gcp"):
			credErr = fmt.Errorf("project_lookup_failed: session %s is not a GCP deployment (cloud=%s)", sessionID, cloud)
		default:
			fetched, err := d.Creds.GCP(ctx, projectID)
			if err != nil {
				// Use %w so callers can errors.As() into
				// *CredentialFetchError on the wrapped per-sub error
				// strings (the SubResult.Error path stores the wrapped
				// string only — but reliable's component-metrics path
				// goes through this same code with a raw err return,
				// not the per-sub channel).
				credErr = fmt.Errorf("credential_fetch_failed: %w", err)
			} else {
				creds = fetched
			}
		}
	}

	fn := func(ctx context.Context, idx int, sub SubRequest) SubResult {
		return d.dispatchGCPBatchSub(ctx, idx, sub, sessionID, creds, credErr)
	}
	return runSubsBounded(ctx, subs, DefaultBatchConcurrency, DefaultPerSubTimeout, fn), nil
}

// isAWSCredlessSub reports whether the sub can run without AWS
// credentials. Mirrors Dispatcher.AWS's early-return logic so batch
// and singular agree on what counts.
func isAWSCredlessSub(sub SubRequest) bool {
	action := normalizeAction(sub.Action)
	return action == "list-actions" || action == "list-metrics"
}

// isGCPCredlessSub mirrors isAWSCredlessSub for GCP.
func isGCPCredlessSub(sub SubRequest) bool {
	action := normalizeAction(sub.Action)
	return action == "list-actions" || action == "list-metrics"
}

// dispatchAWSBatchSub is the per-sub worker invoked by runSubsBounded.
// Mirrors Dispatcher.AWS's control flow so singular and batch behave
// identically for a given (service, action) pair.
func (d *Dispatcher) dispatchAWSBatchSub(ctx context.Context, idx int, sub SubRequest, sessionID string, cfg aws.Config, credErr error) SubResult {
	service := normalizeAction(sub.Service)
	action := normalizeAction(sub.Action)
	service = observability.CanonicalAWSService(service)

	r := SubResult{Index: idx, Service: service, Action: action}

	if action == "list-metrics" {
		result, err := d.listAWSMetricsResponse(service)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.Result = result
		r.OK = true
		return r
	}
	if action == "list-actions" {
		result, err := listActionsResponse(service, observability.AWSServiceActions, observability.AWSServiceNames())
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.Result = result
		r.OK = true
		return r
	}

	// From here on we need credentials. Surface the shared error if
	// the batch-level fetch failed.
	if credErr != nil {
		r.Error = credErr.Error()
		return r
	}

	filters := d.injectProjectFilter(sub.Filters, sessionID)
	result, err := d.inspectAWSCore(ctx, cfg, service, action, filters)
	if err != nil {
		d.maybeReportDrift(ctx, sessionID, err)
		r.Error = err.Error()
		return r
	}
	r.Result = result
	r.OK = true
	return r
}

// dispatchGCPBatchSub is the GCP twin. inspectGCPCore applies
// protoNormalize internally, so batch results have the same
// lowerCamelCase / named-enum shape as singular calls.
func (d *Dispatcher) dispatchGCPBatchSub(ctx context.Context, idx int, sub SubRequest, sessionID string, creds *GCPCreds, credErr error) SubResult {
	service := normalizeAction(sub.Service)
	action := normalizeAction(sub.Action)
	service = observability.CanonicalGCPService(service)

	r := SubResult{Index: idx, Service: service, Action: action}

	if action == "list-metrics" {
		result, err := d.listGCPMetricsResponse(service)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.Result = result
		r.OK = true
		return r
	}
	if action == "list-actions" {
		result, err := listActionsResponse(service, observability.GCPServiceActions, observability.GCPServiceNames())
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.Result = result
		r.OK = true
		return r
	}

	if credErr != nil {
		r.Error = credErr.Error()
		return r
	}

	filters := d.injectProjectFilter(sub.Filters, sessionID)
	result, err := d.inspectGCPCore(ctx, creds, service, action, filters)
	if err != nil {
		d.maybeReportDrift(ctx, sessionID, err)
		r.Error = err.Error()
		return r
	}
	r.Result = result
	r.OK = true
	return r
}
