// ACM (AWS Certificate Manager) inspector (issue #596).
//
// Provides panel-default discovery for the aws/acm preset (#586,
// composer wiring #602). Two actions:
//
//   - list-certificates — ListCertificates; returns the
//     CertificateSummaryList. The SDK exposes a `Includes` filter that
//     can scope by KeyTypes / KeyUsage / ExtendedKeyUsage, but tag-based
//     project scoping is not server-side (callers post-filter via
//     ListTagsForCertificate per-cert in the panel layer when needed).
//   - describe-certificate — DescribeCertificate for a specific ARN
//     (caller supplies certificate_arn in the filters JSON). Returns
//     the full certificate detail including domain_validation_options
//     for DNS validation drift.
//
// Issue #255 contract: list-certificates uses nilSliceToEmpty so an
// empty AWS response marshals as `[]` not `null`. describe-certificate
// returns a single object (no slice nil to worry about) but the
// "RenewalSummary.DomainValidationOptions" sub-slice can still be nil
// post-marshal; downstream consumers tolerate that on a per-cert object
// detail (it's already wrapped in a map, not the top-level slice the
// reliable UI gates on).

package aws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
)

// acmClient is the narrowed SDK surface used by inspectACM. Lets tests
// inject a fake without doing real AWS auth.
type acmClient interface {
	ListCertificates(ctx context.Context, params *acm.ListCertificatesInput, optFns ...func(*acm.Options)) (*acm.ListCertificatesOutput, error)
	DescribeCertificate(ctx context.Context, params *acm.DescribeCertificateInput, optFns ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error)
}

func inspectACM(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	switch action {
	case "list-certificates":
		return listCertificates(ctx, acm.NewFromConfig(cfg))
	case "describe-certificate":
		certArn, err := acmFilterCertificateArn(filters)
		if err != nil {
			return nil, err
		}
		return describeCertificate(ctx, acm.NewFromConfig(cfg), certArn)
	case "get-metrics":
		// ACM emits a small set of CloudWatch metrics (DaysToExpiry per
		// cert) under the AWS/CertificateManager namespace; the metrics
		// fetch path owns those. Route through metricsRouted so callers
		// can pivot to pkg/observability/metrics.
		return metricsRouted("acm")
	default:
		return nil, unsupportedActionError("acm", action)
	}
}

// listCertificates runs ListCertificates and returns the
// CertificateSummaryList with nil normalized to []. The API supports
// pagination via NextToken — current implementation returns the first
// page (default 1000 certs); fan-out is a follow-up if real customers
// hit that ceiling (no observed cases in production presets, which
// typically manage <10 certs per stack).
func listCertificates(ctx context.Context, client acmClient) ([]acmtypes.CertificateSummary, error) {
	out, err := client.ListCertificates(ctx, &acm.ListCertificatesInput{})
	if err != nil {
		return nil, err
	}
	return nilSliceToEmpty(out.CertificateSummaryList), nil
}

// describeCertificate runs DescribeCertificate for the given ARN and
// returns the full CertificateDetail. Used by drift detection to
// compare domain_validation_options against the snapshot.
func describeCertificate(ctx context.Context, client acmClient, arn string) (*acmtypes.CertificateDetail, error) {
	out, err := client.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
		CertificateArn: aws.String(arn),
	})
	if err != nil {
		return nil, err
	}
	return out.Certificate, nil
}

// acmFilterCertificateArn parses the filters JSON envelope for a
// `certificate_arn` key. Returns a structured error (not silent
// fallback) when missing — DescribeCertificate is a per-ARN call, so
// the inspector cannot pick a "default" cert.
func acmFilterCertificateArn(filters string) (string, error) {
	if filters == "" {
		return "", fmt.Errorf("describe-certificate requires a certificate_arn in the filters envelope (e.g. {\"certificate_arn\":\"arn:aws:acm:...\"})")
	}
	var fm map[string]string
	if err := json.Unmarshal([]byte(filters), &fm); err != nil {
		return "", fmt.Errorf("describe-certificate: invalid filters JSON: %w", err)
	}
	arn := fm["certificate_arn"]
	if arn == "" {
		return "", fmt.Errorf("describe-certificate requires a certificate_arn in the filters envelope (e.g. {\"certificate_arn\":\"arn:aws:acm:...\"})")
	}
	return arn, nil
}
