//go:build integration

// Live full-scan smoke test for #616. Walks every CloudControl-backed
// TF type registered in cloudControlTypeConfigs against a real AWS
// account in us-east-1, per-type subtests, and fails any subtest whose
// CC ListResources call rejects with InvalidRequestException (HTTP
// 400) — the class of failure that surfaced as
// AWS::EKS::PodIdentityAssociation missing its ParentLister field.
//
// Run:
//
//	# from a shell where AWS creds are loaded (e.g. aws_jump <acct> <role>):
//	go test -tags=integration ./cmd/insideout-import/awsdiscover/... \
//	    -v -run TestLive616_FullScanNoInvalidRequest -timeout 30m
//
// Forces the CC ListResources fallback on every type by passing
// Project="" and zero TagSelectors — that skips the RGT prefetcher
// (see awsdiscover.go RGT pre-pass; nil cache means
// args.RGTCacheForCFN returns ok=false so per-type discoverers fall
// straight through to ListResources). This is the exact code path the
// live #616 repro exercised.

package awsdiscover

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	cctypes "github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithy "github.com/aws/smithy-go"
)

// live616RequireCCPermission probes cloudformation:ListResources with a
// known top-level (non-parent-scoped) CFN type. If the probe returns
// AccessDeniedException, t.Skip the entire test with a clear message —
// otherwise the per-subtest classifier would silently skip every type
// as an environmental error and the overall test would PASS without
// actually exercising the #616 regression class.
//
// Pick AWS::SQS::Queue: cheap, exists in us-east-1 for any account,
// CloudControl-readable since 2022, no parent-scoping. The call needs
// only cloudformation:ListResources on resource/* — if that fails, no
// other CFN type will work either and the test is meaningless.
func live616RequireCCPermission(t *testing.T, cfg aws.Config) {
	t.Helper()
	client := cloudcontrol.NewFromConfig(cfg)
	probeType := "AWS::SQS::Queue"
	_, err := client.ListResources(context.Background(), &cloudcontrol.ListResourcesInput{
		TypeName: &probeType,
	})
	if err == nil {
		return
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "AccessDeniedException", "AccessDenied", "UnauthorizedOperation":
			t.Skipf("CC ListResources permission probe failed (cloudformation:ListResources for %s): %v — re-auth with a role that grants cloudformation:ListResources on resource/* (and read on the underlying services) before running this test", probeType, err)
		}
	}
	// Any other error (throttle, transient): not a permission problem,
	// proceed and let per-subtest classification handle it.
}

// live616LoadOrSkip mirrors loadOrSkip in
// pkg/observability/discovery/aws/live_integration_test.go (not
// importable across packages — duplicated intentionally, same pattern
// as dynamodb_table_enrich_live_test.go). Defaults region to us-east-1
// because that's the region the original #616 repro hit. Probes STS to
// make a credential-less run a clean Skip rather than a confusing late
// failure inside the actual scan.
func live616LoadOrSkip(t *testing.T) aws.Config {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Skipf("no AWS config: %v", err)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if _, err := sts.NewFromConfig(cfg).GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{}); err != nil {
		t.Skipf("no usable AWS credentials (sts.GetCallerIdentity failed): %v", err)
	}
	return cfg
}

// live616IsEnvSkip mirrors isAWSEnvSkip in
// pkg/observability/discovery/aws/live_probe_255_test.go. Classifies
// environmental errors (region not subscribed, opt-in required, access
// denied, etc.) as Skip rather than Fail so the scan runs cleanly
// against any AWS account.
func live616IsEnvSkip(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "OptInRequired",
			"UnauthorizedOperation",
			"AccessDeniedException",
			"AccessDenied",
			"InvalidClientTokenId",
			"AuthFailure",
			"UnrecognizedClientException",
			"UnsupportedOperation",
			"ServiceUnavailableException",
			"ServiceUnavailable",
			"InvalidAction":
			return true
		}
	}
	s := err.Error()
	for _, sub := range []string{
		"is not subscribed to AWS",
		"opted in",
		"is not authorized to perform",
		"could not be found",
		"DataUnavailable",
		"is not enabled in this region",
		"region is disabled",
	} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestLive616_FullScanNoInvalidRequest exercises every registered
// TF type against the live AWS account in us-east-1 and asserts no
// CC ListResources call returns InvalidRequestException. A 400 here
// strongly indicates the type's CFN handler requires a ResourceModel
// and the production table entry is missing ParentLister — the #616
// regression class.
//
// Per-type subtests are required because DiscoverTypes short-circuits
// on the first error (awsdiscover.go ~line 432). Per-type isolation
// lets a single CI run enumerate every broken type rather than play
// whack-a-mole.
//
// Zero items returned by a type is a PASS — the test is shape-only,
// not existence-dependent.
func TestLive616_FullScanNoInvalidRequest(t *testing.T) {
	cfg := live616LoadOrSkip(t)
	cfg.Region = "us-east-1"

	// Skip the entire test if the caller lacks CC permission — without
	// it every subtest would skip as AccessDeniedException and the
	// overall test would PASS without exercising the regression class.
	live616RequireCCPermission(t, cfg)

	a := NewAWSDiscoverer(cfg)

	identity, err := sts.NewFromConfig(cfg).GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
	if err != nil {
		t.Skipf("STS GetCallerIdentity failed after loadOrSkip probe: %v", err)
	}
	accountID := ""
	if identity.Account != nil {
		accountID = *identity.Account
	}

	for _, tfType := range a.SupportedTypes() {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()

			args := DiscoverArgs{
				Project:   "", // empty → no RGT prefetch → forces CC ListResources fallback
				Regions:   []string{"us-east-1"},
				AccountID: accountID,
			}
			_, err := a.DiscoverTypes(context.Background(), []string{tfType}, args)
			if err == nil {
				return
			}

			// Treat environmental errors as Skip (the test isn't probing
			// account permissions or regional availability — it's pinning
			// the ParentLister wiring).
			if live616IsEnvSkip(err) {
				t.Skipf("environmental skip: %v", err)
			}

			// The #616 failure class: CC ListResources rejected the call
			// with InvalidRequestException because no ResourceModel was
			// supplied. errors.As walks the wrapped error chain (the
			// discoverer wraps with %w at cloudcontrol_discoverer.go ~344).
			var invalid *cctypes.InvalidRequestException
			if errors.As(err, &invalid) {
				t.Fatalf("%s likely needs ParentLister wiring — CC ListResources rejected with InvalidRequestException: %v", tfType, err)
			}

			// Fallback: the typed exception may not survive every wrap
			// path. Match on the smithy error code as a defense-in-depth.
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidRequestException" {
				t.Fatalf("%s likely needs ParentLister wiring — CC ListResources rejected with InvalidRequestException: %v", tfType, err)
			}

			// Any other error: surface but distinguish from the #616 class.
			// We do not Fail here because the live account may have
			// permission/quota edge cases unrelated to the regression
			// we're guarding against; a t.Logf keeps the run visible
			// without false positives.
			t.Logf("%s returned non-#616 error (not failing test): %v", tfType, err)
		})
	}
}
