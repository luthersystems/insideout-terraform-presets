// AWS SageMaker inspector (issue #622).
//
// Provides panel-default discovery for the aws/sagemaker preset (#615,
// composer wiring #618). Three list/describe actions plus the metrics
// passthrough:
//
//   - list-domains — ListDomains; returns []sagemakertypes.DomainDetails.
//     SageMaker domains are the top-level entity that holds user
//     profiles, spaces, and Studio apps; the panel-default surface.
//   - describe-domain — DescribeDomain for a specific domain ID
//     (caller supplies domain_id in the filters JSON). Returns the
//     full *sagemaker.DescribeDomainOutput including security
//     groups / VPC config / DefaultUserSettings used by drift checks.
//   - list-user-profiles — ListUserProfiles; returns
//     []sagemakertypes.UserProfileDetails across every domain visible to
//     the credentials. No required filter; callers post-scope by
//     DomainId in the panel layer if needed.
//   - get-metrics — routed to pkg/observability/metrics; AWS/SageMaker
//     emits CloudWatch metrics that the metrics package owns.
//
// Issue #255 contract: list-domains and list-user-profiles both use
// nilSliceToEmpty so empty AWS responses marshal as `[]` not `null`.
// describe-domain returns a single object (no top-level slice to guard).

package aws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	sagemakertypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

// sageMakerClient is the narrowed SDK surface used by inspectSageMaker.
// Lets tests inject a fake without doing real AWS auth.
type sageMakerClient interface {
	ListDomains(ctx context.Context, params *sagemaker.ListDomainsInput, optFns ...func(*sagemaker.Options)) (*sagemaker.ListDomainsOutput, error)
	DescribeDomain(ctx context.Context, params *sagemaker.DescribeDomainInput, optFns ...func(*sagemaker.Options)) (*sagemaker.DescribeDomainOutput, error)
	ListUserProfiles(ctx context.Context, params *sagemaker.ListUserProfilesInput, optFns ...func(*sagemaker.Options)) (*sagemaker.ListUserProfilesOutput, error)
}

func inspectSageMaker(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	switch action {
	case "list-domains":
		return listSageMakerDomains(ctx, sagemaker.NewFromConfig(cfg))
	case "describe-domain":
		domainID, err := sageMakerFilterDomainID(filters)
		if err != nil {
			return nil, err
		}
		return describeSageMakerDomain(ctx, sagemaker.NewFromConfig(cfg), domainID)
	case "list-user-profiles":
		return listSageMakerUserProfiles(ctx, sagemaker.NewFromConfig(cfg))
	case "get-metrics":
		// SageMaker emits CloudWatch metrics under the AWS/SageMaker
		// namespace; the metrics fetch path owns those. Route through
		// metricsRouted so callers pivot to pkg/observability/metrics.
		return metricsRouted("sagemaker")
	default:
		return nil, unsupportedActionError("sagemaker", action)
	}
}

// listSageMakerDomains runs ListDomains and returns the Domains slice
// with nil normalized to []. Pagination via NextToken; current impl
// returns the first page (default 10 domains per page — sufficient for
// most stacks). Multi-page fan-out is a follow-up if needed.
func listSageMakerDomains(ctx context.Context, client sageMakerClient) ([]sagemakertypes.DomainDetails, error) {
	out, err := client.ListDomains(ctx, &sagemaker.ListDomainsInput{})
	if err != nil {
		return nil, err
	}
	return nilSliceToEmpty(out.Domains), nil
}

// describeSageMakerDomain runs DescribeDomain for the given domain ID
// and returns the full output struct (including security configuration,
// VPC bindings, and DefaultUserSettings).
func describeSageMakerDomain(ctx context.Context, client sageMakerClient, domainID string) (*sagemaker.DescribeDomainOutput, error) {
	out, err := client.DescribeDomain(ctx, &sagemaker.DescribeDomainInput{
		DomainId: aws.String(domainID),
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// listSageMakerUserProfiles runs ListUserProfiles across every domain
// visible to the credentials, returning UserProfiles with nil
// normalized to [].
func listSageMakerUserProfiles(ctx context.Context, client sageMakerClient) ([]sagemakertypes.UserProfileDetails, error) {
	out, err := client.ListUserProfiles(ctx, &sagemaker.ListUserProfilesInput{})
	if err != nil {
		return nil, err
	}
	return nilSliceToEmpty(out.UserProfiles), nil
}

// sageMakerFilterDomainID parses the filters JSON envelope for a
// `domain_id` key. Returns a structured error when missing —
// DescribeDomain is per-ID, so the inspector cannot pick a "default"
// domain.
func sageMakerFilterDomainID(filters string) (string, error) {
	if filters == "" {
		return "", fmt.Errorf("describe-domain requires a domain_id in the filters envelope (e.g. {\"domain_id\":\"d-xxxxxxxxxxxx\"})")
	}
	var fm map[string]string
	if err := json.Unmarshal([]byte(filters), &fm); err != nil {
		return "", fmt.Errorf("describe-domain: invalid filters JSON: %w", err)
	}
	id := fm["domain_id"]
	if id == "" {
		return "", fmt.Errorf("describe-domain requires a domain_id in the filters envelope (e.g. {\"domain_id\":\"d-xxxxxxxxxxxx\"})")
	}
	return id, nil
}
