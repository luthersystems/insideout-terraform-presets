package gcp

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/billing/apiv1/billingpb"
	"cloud.google.com/go/billing/budgets/apiv1/budgetspb"
	gax "github.com/googleapis/gax-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/iterator"
	"google.golang.org/genproto/googleapis/type/money"
)

// fakeBillingClient is a minimal stub of the Cloud Billing client that
// returns a canned ProjectBillingInfo. Mirrors the interface-injection
// pattern the InsideOut backend uses in gcp_inspect.go::inspectGCPBillingWithDeps.
type fakeBillingClient struct {
	info *billingpb.ProjectBillingInfo
	err  error
}

func (f *fakeBillingClient) GetProjectBillingInfo(_ context.Context, _ *billingpb.GetProjectBillingInfoRequest, _ ...gax.CallOption) (*billingpb.ProjectBillingInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.info, nil
}

// fakeBudgetClient returns a canned slice of budgets via a bounded
// iterator that mirrors the real iterator's iterator.Done contract.
type fakeBudgetClient struct {
	budgets []*budgetspb.Budget
	err     error
}

func (f *fakeBudgetClient) ListBudgets(_ context.Context, _ *budgetspb.ListBudgetsRequest, _ ...gax.CallOption) budgetIterator {
	return &fakeBudgetIterator{budgets: f.budgets, err: f.err}
}

type fakeBudgetIterator struct {
	budgets []*budgetspb.Budget
	err     error
	pos     int
}

func (f *fakeBudgetIterator) Next() (*budgetspb.Budget, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.pos >= len(f.budgets) {
		return nil, iterator.Done
	}
	b := f.budgets[f.pos]
	f.pos++
	return b, nil
}

func TestInspectBilling_GetBillingInfo(t *testing.T) {
	t.Parallel()
	bc := &fakeBillingClient{
		info: &billingpb.ProjectBillingInfo{
			ProjectId:          "demo-proj",
			BillingEnabled:     true,
			BillingAccountName: "billingAccounts/0123-4567-89AB",
		},
	}
	got, err := inspectBillingWithDeps(context.Background(), bc, &fakeBudgetClient{}, "demo-proj", "get-billing-info", "")
	require.NoError(t, err)
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "demo-proj", m["project_id"])
	assert.Equal(t, true, m["billing_enabled"])
	assert.Equal(t, "billingAccounts/0123-4567-89AB", m["billing_account_name"])
}

func TestInspectBilling_GetBillingInfo_Error(t *testing.T) {
	t.Parallel()
	bc := &fakeBillingClient{err: errors.New("api boom")}
	_, err := inspectBillingWithDeps(context.Background(), bc, &fakeBudgetClient{}, "demo-proj", "get-billing-info", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get billing info")
}

func TestInspectBilling_GetBudgets_NoBillingAccount(t *testing.T) {
	t.Parallel()
	bc := &fakeBillingClient{
		info: &billingpb.ProjectBillingInfo{
			ProjectId:          "demo-proj",
			BillingAccountName: "",
		},
	}
	got, err := inspectBillingWithDeps(context.Background(), bc, &fakeBudgetClient{}, "demo-proj", "get-budgets", "")
	require.NoError(t, err)
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "demo-proj", m["project_id"])
	assert.Contains(t, m["note"], "No billing account")
}

func TestInspectBilling_GetBudgets_HappyPath(t *testing.T) {
	t.Parallel()
	bc := &fakeBillingClient{
		info: &billingpb.ProjectBillingInfo{
			ProjectId:          "demo-proj",
			BillingAccountName: "billingAccounts/0123-4567-89AB",
		},
	}
	bgc := &fakeBudgetClient{
		budgets: []*budgetspb.Budget{
			{
				Name:        "billingAccounts/0123-4567-89AB/budgets/budget-one",
				DisplayName: "Monthly cap",
				Amount: &budgetspb.BudgetAmount{
					BudgetAmount: &budgetspb.BudgetAmount_SpecifiedAmount{
						SpecifiedAmount: &money.Money{
							CurrencyCode: "USD",
							Units:        100,
							Nanos:        0,
						},
					},
				},
				ThresholdRules: []*budgetspb.ThresholdRule{
					{ThresholdPercent: 0.5, SpendBasis: budgetspb.ThresholdRule_CURRENT_SPEND},
					{ThresholdPercent: 0.9, SpendBasis: budgetspb.ThresholdRule_CURRENT_SPEND},
				},
			},
		},
	}
	got, err := inspectBillingWithDeps(context.Background(), bc, bgc, "demo-proj", "get-budgets", "")
	require.NoError(t, err)
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "billingAccounts/0123-4567-89AB", m["billing_account"])
	assert.Equal(t, 1, m["budget_count"])
	bs, ok := m["budgets"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, bs, 1)
	assert.Equal(t, "Monthly cap", bs[0]["display_name"])
	assert.Equal(t, "USD 100.000000000", bs[0]["specified_amount"])
	thresholds, ok := bs[0]["threshold_rules"].([]map[string]any)
	require.True(t, ok)
	assert.Len(t, thresholds, 2)
	assert.InDelta(t, 0.5, thresholds[0]["threshold_percent"], 0.001)
}

func TestInspectBilling_GetBudgets_PermissionDenied(t *testing.T) {
	t.Parallel()
	bc := &fakeBillingClient{
		info: &billingpb.ProjectBillingInfo{
			ProjectId:          "demo-proj",
			BillingAccountName: "billingAccounts/0123-4567-89AB",
		},
	}
	bgc := &fakeBudgetClient{err: errors.New("rpc error: code = PermissionDenied desc = nope")}
	got, err := inspectBillingWithDeps(context.Background(), bc, bgc, "demo-proj", "get-budgets", "")
	require.NoError(t, err, "PermissionDenied must degrade to a note rather than fail")
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, m["note"], "billing.viewer")
	assert.Equal(t, "billingAccounts/0123-4567-89AB", m["billing_account"])
}

func TestInspectBilling_GetBudgets_GenericError(t *testing.T) {
	t.Parallel()
	bc := &fakeBillingClient{
		info: &billingpb.ProjectBillingInfo{
			ProjectId:          "demo-proj",
			BillingAccountName: "billingAccounts/0123-4567-89AB",
		},
	}
	bgc := &fakeBudgetClient{err: errors.New("rpc error: code = Internal desc = service unavailable")}
	_, err := inspectBillingWithDeps(context.Background(), bc, bgc, "demo-proj", "get-budgets", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list budgets")
}

func TestInspectBilling_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectBillingWithDeps(context.Background(), nil, nil, "demo-proj", "no-such", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Billing action")
	assert.Contains(t, err.Error(), `"no-such"`)
}
