//go:build integration

// Live full-scan smoke test — GCP analog of the AWS
// TestLive616_FullScanNoInvalidRequest (#616). Walks every TF type
// registered in GCPDiscoverer.byType against a real GCP project,
// per-type subtests, and fails any subtest whose underlying Google
// API call rejects with HTTP 400 (REST: *googleapi.Error{Code:400})
// or gRPC InvalidArgument (codes.InvalidArgument) — the GCP
// equivalent of the AWS InvalidRequestException class that surfaced
// AWS::EKS::PodIdentityAssociation / AWS::ElasticLoadBalancingV2::Listener
// as missing ParentLister.
//
// Run:
//
//	# Either:
//	#   gcloud auth application-default login                  # ADC, or
//	#   export GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa.json # SA key
//	export LIVE_GCP_PROJECT_ID=your-project-id
//	go test -tags=integration ./cmd/insideout-import/gcpdiscover/... \
//	    -v -run TestLive616Analog_FullScanNoInvalidArgument -timeout 30m
//
// Forces the CAI ListResources path on every type by passing
// args.Project="" (no labels-filter clause), and exercises the
// non-CAI per-parent fan-out path for the types that don't live in
// Cloud Asset (sinks, SQL users, identity platform, IAM, project
// services, secret versions, bucket objects, etc.).

package gcpdiscover

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// live616AnalogLoadOrSkip returns the project ID from
// LIVE_GCP_PROJECT_ID or skips the test. ADC (or
// GOOGLE_APPLICATION_CREDENTIALS) is consumed implicitly by the SDK
// clients constructed inside NewRealAssetSearcher /
// NewReal*Lister(ctx) below.
func live616AnalogLoadOrSkip(t *testing.T) string {
	t.Helper()
	projectID := os.Getenv("LIVE_GCP_PROJECT_ID")
	if projectID == "" {
		t.Skip("LIVE_GCP_PROJECT_ID not set; export the project ID to run the GCP analog of the #616 live full-scan")
	}
	return projectID
}

// live616AnalogIsEnvSkip classifies environmental errors (auth,
// permissions, API-not-enabled, region/project misconfigured) as Skip
// rather than Fail — analogous to the AWS test's live616IsEnvSkip.
// Covers both REST (*googleapi.Error) and gRPC (status.FromError)
// error surfaces.
func live616AnalogIsEnvSkip(err error) bool {
	if err == nil {
		return false
	}
	// REST: google.golang.org/api SDKs return *googleapi.Error with
	// .Code set to the HTTP status. 401/403 are env; 404 is "the
	// resource doesn't exist" which we also treat as env (project may
	// legitimately not have the resource yet).
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch gerr.Code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return true
		}
		// 403 sometimes comes with a body that says "API not enabled
		// for project" or "has not been used in project X before or it
		// is disabled" — those are env, not code bugs.
		msg := gerr.Body
		if msg == "" {
			msg = gerr.Message
		}
		if strings.Contains(msg, "not been used in project") ||
			strings.Contains(msg, "is disabled") ||
			strings.Contains(msg, "API not enabled") ||
			strings.Contains(msg, "accessNotConfigured") {
			return true
		}
	}
	// gRPC: cloud.google.com/go SDKs (CAI in particular) return
	// status.Status errors. Unauthenticated / PermissionDenied are
	// env; codes.NotFound (project doesn't exist or no resources of
	// this type) is also env.
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.Unauthenticated, codes.PermissionDenied, codes.NotFound, codes.FailedPrecondition:
			return true
		}
	}
	// String-match fallbacks for errors that don't surface either
	// typed shape cleanly through every layer of wrapping in the
	// discoverer.
	s := err.Error()
	for _, sub := range []string{
		"is not enabled",
		"API not enabled",
		"has not been used in project",
		"PERMISSION_DENIED",
		"UNAUTHENTICATED",
		"could not find default credentials",
		"Reauthentication failed",
		"reauthentication is required",
	} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// live616AnalogIsBadRequest reports whether err is the GCP analog of
// AWS's InvalidRequestException — REST 400 or gRPC InvalidArgument.
// This is the failure shape that indicates a wiring bug in a per-type
// discoverer (e.g. missing parent enumeration, wrong filter dialect,
// malformed API call) rather than an environmental issue.
func live616AnalogIsBadRequest(err error) bool {
	if err == nil {
		return false
	}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) && gerr.Code == http.StatusBadRequest {
		return true
	}
	if s, ok := status.FromError(err); ok && s.Code() == codes.InvalidArgument {
		return true
	}
	return false
}

// TestLive616Analog_FullScanNoInvalidArgument exercises every
// registered TF type against the live GCP project and asserts no
// underlying API call returns the GCP analog of an
// InvalidRequestException (HTTP 400 / gRPC InvalidArgument). Strongly
// indicates a per-type discoverer wiring bug — the GCP analog of the
// #616 AWS failure class.
//
// Per-type subtests are required because DiscoverTypes for a single
// CAI bucket is one SearchAllResources call; running each type in
// isolation lets a single CI run enumerate every broken type rather
// than play whack-a-mole.
//
// Zero items returned for a type is a PASS — the test is shape-only,
// not existence-dependent.
func TestLive616Analog_FullScanNoInvalidArgument(t *testing.T) {
	projectID := live616AnalogLoadOrSkip(t)
	ctx := context.Background()

	// Production wiring: same NewRealAssetSearcher +
	// NewReal*Lister(ctx) setup as cmd/insideout-import/discover.go.
	// Lister construction failures are tolerated with t.Logf warnings
	// (mirroring discover.go's WARN pattern) — the corresponding
	// non-CAI types are then exercised against a nil lister, which
	// the per-discoverer code path tolerates.
	searcher, err := NewRealAssetSearcher(ctx)
	if err != nil {
		if live616AnalogIsEnvSkip(err) {
			t.Skipf("CAI searcher construction failed (env): %v — ensure ADC or GOOGLE_APPLICATION_CREDENTIALS is set, and Cloud Asset API is enabled on the ADC quota project", err)
		}
		t.Fatalf("CAI searcher construction failed: %v", err)
	}
	// t.Cleanup (not defer): subtests use t.Parallel(), which queues
	// them to run AFTER the parent body returns. defer would close the
	// searcher before any subtest runs, producing fake "PASS" results
	// from a closed client.
	t.Cleanup(func() { _ = searcher.Close() })

	opts := GCPDiscovererOpts{}
	if l, err := NewRealLoggingSinkLister(ctx); err != nil {
		t.Logf("WARN: logging sink lister unavailable (sinks won't be exercised): %v", err)
	} else {
		opts.SinkLister = l
	}
	if l, err := NewRealSQLUserLister(ctx); err != nil {
		t.Logf("WARN: sqladmin user lister unavailable: %v", err)
	} else {
		opts.SQLUserLister = l
	}
	if l, err := NewRealIdentityPlatformConfigLister(ctx); err != nil {
		t.Logf("WARN: identitytoolkit lister unavailable: %v", err)
	} else {
		opts.IdentityPlatformLister = l
	}
	if l, err := NewRealIAMPolicyLister(ctx); err != nil {
		t.Logf("WARN: IAM policy lister unavailable: %v", err)
	} else {
		opts.IAMPolicyLister = l
	}
	if l, err := NewRealSecretVersionLister(ctx); err != nil {
		t.Logf("WARN: secret version lister unavailable: %v", err)
	} else {
		opts.SecretVersionLister = l
	}
	if l, err := NewRealBucketObjectLister(ctx); err != nil {
		t.Logf("WARN: bucket object lister unavailable: %v", err)
	} else {
		opts.BucketObjectLister = l
	}
	if l, err := NewRealProjectServiceLister(ctx); err != nil {
		t.Logf("WARN: serviceusage lister unavailable: %v", err)
	} else {
		opts.ProjectServiceLister = l
	}
	if l, err := NewRealDefaultSupportedIdpConfigLister(ctx); err != nil {
		t.Logf("WARN: default-IDP-config lister unavailable: %v", err)
	} else {
		opts.DefaultSupportedIdpConfigLister = l
	}
	if l, err := NewRealServiceNetworkingConnectionLister(ctx); err != nil {
		t.Logf("WARN: servicenetworking lister unavailable: %v", err)
	} else {
		opts.ServiceNetworkingConnectionLister = l
	}
	if l, err := NewRealVPCAccessConnectorLister(ctx); err != nil {
		t.Logf("WARN: vpcaccess lister unavailable: %v", err)
	} else {
		opts.VPCAccessConnectorLister = l
	}

	g := NewGCPDiscoverer(searcher, projectID, opts)

	for _, tfType := range g.SupportedTypes() {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()

			args := DiscoverArgs{
				Project: "", // empty → no labels-filter clause; exercises full CAI scope path
			}
			_, err := g.DiscoverTypes(context.Background(), []string{tfType}, args)
			if err == nil {
				return
			}

			if live616AnalogIsEnvSkip(err) {
				t.Skipf("environmental skip: %v", err)
			}

			if live616AnalogIsBadRequest(err) {
				t.Fatalf("%s likely has a per-type discoverer wiring bug — GCP API rejected with HTTP 400 / InvalidArgument: %v", tfType, err)
			}

			// Other errors: log but don't fail — could be transient
			// (quota throttle, network), and we don't want false
			// positives on the #616-class regression we're guarding
			// against. The bad-request classifier above is the gate.
			t.Logf("%s returned non-#616-class error (not failing test): %v", tfType, err)
		})
	}
}
