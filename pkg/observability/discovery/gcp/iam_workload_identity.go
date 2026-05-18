// IAM Workload Identity Federation + Service Accounts inspector (#606).
//
// Provides panel-default discovery for the gcp/github_actions WIF
// preset (PR #605). The preset creates:
//
//   - google_iam_workload_identity_pool  (the federation trust boundary)
//   - google_iam_workload_identity_pool_provider (per-IDP config — GitHub,
//     AWS, OIDC, SAML, X509)
//   - google_service_account              (the impersonation target)
//   - google_service_account_iam_binding  (who can impersonate the SA)
//   - google_project_iam_member           (project-level roles on the SA)
//
// `google_service_account` and `google_project_iam_member` are already
// listed in pkg/insideout-import/registry/registry.go::gcpDiscoverTypes
// and discovered via Cloud Asset Inventory (CAI); this inspector adds
// the live-API path for the panel (list-service-accounts via the IAM
// admin API), so the panel can render the impersonation target +
// per-pool config + per-provider attribute_condition without waiting
// for a CAI export round-trip.
//
// The WIF pool + provider + SA IAM binding live on the IAM v1 admin
// API (iam.googleapis.com) — CAI does not index these types. Hence
// they ship via gcpCodegenOnlyTypes for the drift policy (#607) and
// this inspector for the panel discovery (#606).
//
// IAM service: gcp service key "iam".
//
// Actions:
//   - list-workload-identity-pools — projects.locations.workloadIdentity
//     Pools.List against location=global (the only valid location for
//     WIF as of 2025-05). No server-side filter; pools have no labels.
//   - list-workload-identity-pool-providers — providers.List against a
//     specific pool. Caller supplies `pool` in the filters envelope.
//     Returns per-provider attribute_condition + attribute_mapping +
//     oidc/aws/saml/x509 details — the security-load-bearing surface
//     drift policy guards.
//   - list-service-accounts — projects.serviceAccounts.List. Returns
//     []*iam.ServiceAccount for the project. No label filter (SAs
//     don't carry labels at the IAM v1 admin API surface). Caller
//     post-filters by email/account_id if needed.
//   - get-project-iam-policy — projects.GetIamPolicy via the Cloud
//     Resource Manager v1 API. Returns the project IAM policy (bindings
//     of role → members at the project scope). This is the
//     google_project_iam_member surface — operators can spot
//     project-level role drift by diffing against the policy bytes
//     of the preset's google_project_iam_member declarations.
//
// #255 contract: every slice return path is initialized to a non-nil
// composite literal at the construction site (Pattern A from the
// CONTRIBUTING.md cheat-sheet) so an empty result marshals as `[]`,
// never `null`. The project-IAM-policy single-object return is
// non-nil by construction (the API always returns at least an empty
// bindings list).

package gcp

import (
	"context"
	"fmt"

	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// wifLocation is the only valid location string for Workload Identity
// Pools as of 2025-05 — Google's WIF surface is global-only at the API
// path. Kept as a const so callers (and the live probe) don't need to
// pass it through every call.
const wifLocation = "global"

func inspectIAM(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-workload-identity-pools":
		return listWorkloadIdentityPools(ctx, projectID, opts...)

	case "list-workload-identity-pool-providers":
		return listWorkloadIdentityPoolProviders(ctx, projectID, filters, opts...)

	case "list-service-accounts":
		return listServiceAccounts(ctx, projectID, opts...)

	case "get-project-iam-policy":
		return getProjectIAMPolicy(ctx, projectID, opts...)

	default:
		return nil, unsupportedActionError("IAM", action, observability.GCPServiceActions["iam"])
	}
}

// listWorkloadIdentityPools fetches every WIF pool under the project's
// global location. Pools have no user-set labels at the IAM v1 surface
// so no project post-filter applies (every pool in the project is
// surfaced — the panel's stack scoping happens by display_name /
// pool_id convention upstream).
func listWorkloadIdentityPools(ctx context.Context, projectID string, opts ...option.ClientOption) (any, error) {
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, wifLocation)

	// Pattern A: declare with a non-nil composite literal so the
	// empty-result path emits `[]`, not `null` (#255).
	pools := []*iam.WorkloadIdentityPool{}
	err = svc.Projects.Locations.WorkloadIdentityPools.List(parent).Context(ctx).
		Pages(ctx, func(page *iam.ListWorkloadIdentityPoolsResponse) error {
			pools = append(pools, page.WorkloadIdentityPools...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return pools, nil
}

// listWorkloadIdentityPoolProviders fetches every provider under a
// specific pool. The pool is supplied via the filters envelope:
// `{"pool":"<pool_id>"}`. Surfaces a structured error when missing so
// the panel can prompt the caller instead of bubbling a less actionable
// 404 from the SDK.
func listWorkloadIdentityPoolProviders(ctx context.Context, projectID, filters string, opts ...option.ClientOption) (any, error) {
	fm := parseFilterMap(filters)
	pool := fm["pool"]
	if pool == "" {
		return nil, fmt.Errorf("list-workload-identity-pool-providers requires a pool in the filters envelope (e.g. {\"pool\":\"github\"})")
	}
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	parent := fmt.Sprintf("projects/%s/locations/%s/workloadIdentityPools/%s",
		projectID, wifLocation, pool)

	// Pattern A: empty provider list normalizes to `[]`.
	providers := []*iam.WorkloadIdentityPoolProvider{}
	err = svc.Projects.Locations.WorkloadIdentityPools.Providers.List(parent).Context(ctx).
		Pages(ctx, func(page *iam.ListWorkloadIdentityPoolProvidersResponse) error {
			providers = append(providers, page.WorkloadIdentityPoolProviders...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return providers, nil
}

// listServiceAccounts fetches every service account in the project.
// IAM service accounts do not carry user-settable labels at the IAM v1
// admin API surface so no project post-filter applies — every SA in
// the project is surfaced and the caller post-filters by display_name
// / account_id / email convention upstream.
func listServiceAccounts(ctx context.Context, projectID string, opts ...option.ClientOption) (any, error) {
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	parent := fmt.Sprintf("projects/%s", projectID)

	// Pattern A: empty SA list normalizes to `[]`.
	accounts := []*iam.ServiceAccount{}
	err = svc.Projects.ServiceAccounts.List(parent).Context(ctx).
		Pages(ctx, func(page *iam.ListServiceAccountsResponse) error {
			accounts = append(accounts, page.Accounts...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// getProjectIAMPolicy fetches the project-level IAM policy via the
// Cloud Resource Manager v1 API. Returns a *cloudresourcemanager.Policy
// containing the bindings of role → members at the project scope.
//
// The returned shape is wrapped-in-parent (a Policy is an object with
// a `bindings: []` slice + an `etag` string + a `version` int). #255
// pattern C applies: the inner bindings slice is normalized via the
// upstream API contract (the Resource Manager API always returns an
// empty bindings array, never null), but we defensively re-init it
// anyway so a future SDK change doesn't reintroduce the null.
func getProjectIAMPolicy(ctx context.Context, projectID string, opts ...option.ClientOption) (any, error) {
	svc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	policy, err := svc.Projects.GetIamPolicy(projectID, &cloudresourcemanager.GetIamPolicyRequest{}).
		Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	// Pattern C: defensively re-init the inner bindings slice to a
	// non-nil empty slice when the API returns nil (#255). The
	// Resource Manager v1 API always sets Bindings on success, but
	// the typed nil would marshal as `:null` in a future SDK change.
	if policy.Bindings == nil {
		policy.Bindings = []*cloudresourcemanager.Binding{}
	}
	return policy, nil
}
