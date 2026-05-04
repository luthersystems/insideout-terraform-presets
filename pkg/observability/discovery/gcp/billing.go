// Cloud Billing inspector.
//
// Mirrors:
//   - inspectGCPBilling         — the InsideOut backend gcp_inspect.go:1490
//   - inspectGCPBillingWithDeps — the InsideOut backend gcp_inspect.go:1509
//
// No labels.project filter applies — billing info and budgets are
// scoped at the API level by project ID and billing-account-name.
//
// Two actions:
//   - get-billing-info: light-touch, project-level lookup. Always works
//     when the caller has roles/billing.viewer on the project.
//   - get-budgets: requires roles/billing.viewer on the BILLING ACCOUNT
//     (not the project), plus the Cloud Billing Budget API enabled.
//     The handler degrades to a "note" map when permissions are
//     missing rather than failing the call — operators see what they
//     CAN see plus a hint about the missing role.

package gcp

import (
	"context"
	"fmt"
	"strings"

	billing "cloud.google.com/go/billing/apiv1"
	"cloud.google.com/go/billing/apiv1/billingpb"
	budgets "cloud.google.com/go/billing/budgets/apiv1"
	"cloud.google.com/go/billing/budgets/apiv1/budgetspb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// gcpBillingAPI is the subset of the Cloud Billing client we need.
// Mirrors the InsideOut backend's GCPBillingAPI (gcp_inspect.go:1464). Kept
// unexported — the billing dispatcher constructs the real client
// directly.
type gcpBillingAPI interface {
	GetProjectBillingInfo(ctx context.Context, req *billingpb.GetProjectBillingInfoRequest, opts ...gax.CallOption) (*billingpb.ProjectBillingInfo, error)
}

// budgetIterator abstracts the budget iterator for testability. Mirrors
// The InsideOut backend's BudgetIterator (gcp_inspect.go:1469).
type budgetIterator interface {
	Next() (*budgetspb.Budget, error)
}

// gcpBudgetAPI is the subset of the Budget client we need. Mirrors
// The InsideOut backend's GCPBudgetAPI (gcp_inspect.go:1474).
type gcpBudgetAPI interface {
	ListBudgets(ctx context.Context, req *budgetspb.ListBudgetsRequest, opts ...gax.CallOption) budgetIterator
}

// realBudgetClientAdapter wraps the real budget client to implement
// gcpBudgetAPI. Mirrors the InsideOut backend's realBudgetClientAdapter
// (gcp_inspect.go:1478).
type realBudgetClientAdapter struct {
	client *budgets.BudgetClient
}

func (a *realBudgetClientAdapter) ListBudgets(ctx context.Context, req *budgetspb.ListBudgetsRequest, _ ...gax.CallOption) budgetIterator {
	return a.client.ListBudgets(ctx, req)
}

func inspectBilling(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	billingClient, err := billing.NewCloudBillingClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = billingClient.Close() }()

	budgetClient, err := budgets.NewBudgetClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = budgetClient.Close() }()

	return inspectBillingWithDeps(ctx, billingClient, &realBudgetClientAdapter{client: budgetClient}, projectID, action, filters)
}

// inspectBillingWithDeps is the testable core — accepts injected
// billing and budget clients. Mirrors the InsideOut backend's inspectGCPBillingWithDeps
// (gcp_inspect.go:1509). Exported via the unit tests.
func inspectBillingWithDeps(ctx context.Context, billingClient gcpBillingAPI, budgetClient gcpBudgetAPI, projectID, action, _ string) (any, error) {
	switch action {
	case "get-billing-info":
		info, err := billingClient.GetProjectBillingInfo(ctx, &billingpb.GetProjectBillingInfoRequest{
			Name: fmt.Sprintf("projects/%s", projectID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get billing info: %v", err)
		}
		return map[string]any{
			"project_id":           info.GetProjectId(),
			"billing_enabled":      info.GetBillingEnabled(),
			"billing_account_name": info.GetBillingAccountName(),
		}, nil

	case "get-budgets":
		// Step 1: Get billing account ID from project billing info
		info, err := billingClient.GetProjectBillingInfo(ctx, &billingpb.GetProjectBillingInfoRequest{
			Name: fmt.Sprintf("projects/%s", projectID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get billing info: %v", err)
		}

		billingAccountName := info.GetBillingAccountName()
		if billingAccountName == "" {
			return map[string]any{
				"note":       "No billing account associated with this project.",
				"project_id": projectID,
			}, nil
		}

		// Step 2: List budgets for the billing account, scoped to this project
		it := budgetClient.ListBudgets(ctx, &budgetspb.ListBudgetsRequest{
			Parent: billingAccountName,
			Scope:  fmt.Sprintf("projects/%s", projectID),
		})

		budgetList := []map[string]any{}
		for {
			b, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				// Permission denied is common — billing.viewer may
				// not be granted on the billing account.
				if strings.Contains(err.Error(), "PermissionDenied") || strings.Contains(err.Error(), "403") {
					return map[string]any{
						"note":            "Insufficient permissions to list budgets. The inspector service account needs roles/billing.viewer on the billing account.",
						"billing_account": billingAccountName,
						"project_id":      projectID,
					}, nil
				}
				return nil, fmt.Errorf("failed to list budgets: %v", err)
			}

			budget := map[string]any{
				"name":         b.GetName(),
				"display_name": b.GetDisplayName(),
			}

			// Budget amount
			if amt := b.GetAmount(); amt != nil {
				if specified := amt.GetSpecifiedAmount(); specified != nil {
					budget["specified_amount"] = fmt.Sprintf("%s %d.%09d",
						specified.GetCurrencyCode(),
						specified.GetUnits(),
						specified.GetNanos())
				}
				if amt.GetLastPeriodAmount() != nil {
					budget["last_period_amount"] = true
				}
			}

			// Threshold rules
			if rules := b.GetThresholdRules(); len(rules) > 0 {
				var thresholds []map[string]any
				for _, rule := range rules {
					thresholds = append(thresholds, map[string]any{
						"threshold_percent": rule.GetThresholdPercent(),
						"spend_basis":       rule.GetSpendBasis().String(),
					})
				}
				budget["threshold_rules"] = thresholds
			}

			budgetList = append(budgetList, budget)
		}

		return map[string]any{
			"billing_account": billingAccountName,
			"project_id":      projectID,
			"budgets":         budgetList,
			"budget_count":    len(budgetList),
		}, nil

	default:
		return nil, unsupportedActionError("Billing", action, observability.GCPServiceActions["billing"])
	}
}
