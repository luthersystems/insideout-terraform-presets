package awsdiscover

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDDBSubresourceClient is the in-test fake for the DDB sub-resource
// client interface. Each table can be seeded with either a DescribeContributorInsights
// response or an error so per-table test cases can exercise success,
// not-configured, race, and propagation paths independently.
type fakeDDBSubresourceClient struct {
	tables       []string
	listErr      error
	insightsByT  map[string]dynamodb.DescribeContributorInsightsOutput
	insightsErrT map[string]error
}

func (f *fakeDDBSubresourceClient) ListTables(_ context.Context, _ *dynamodb.ListTablesInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &dynamodb.ListTablesOutput{TableNames: f.tables}, nil
}

func (f *fakeDDBSubresourceClient) DescribeContributorInsights(_ context.Context, in *dynamodb.DescribeContributorInsightsInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeContributorInsightsOutput, error) {
	t := aws.ToString(in.TableName)
	if err, ok := f.insightsErrT[t]; ok {
		return nil, err
	}
	if out, ok := f.insightsByT[t]; ok {
		return &out, nil
	}
	return &dynamodb.DescribeContributorInsightsOutput{}, nil
}

// TestListDDBTables_HappyPath pins the parent-enumeration contract:
// ListTables returns the table names in the order the SDK reported.
func TestListDDBTables_HappyPath(t *testing.T) {
	t.Parallel()
	fake := &fakeDDBSubresourceClient{tables: []string{"users", "orders", "products"}}
	got, err := listDDBTablesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"orders", "products", "users"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d]=%q, want %q", i, got[i], w)
		}
	}
}

// TestListDDBTables_PropagatesError pins that an SDK error wraps and
// propagates so the discoverer's per-region abort path can identify it.
func TestListDDBTables_PropagatesError(t *testing.T) {
	t.Parallel()
	seedErr := errors.New("list-tables-seed")
	fake := &fakeDDBSubresourceClient{listErr: seedErr}
	_, err := listDDBTablesWithClient(context.Background(), fake)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err=%v, want errors.Is(err, seedErr)", err)
	}
}

// TestListDDBTables_EmptyAccountReturnsNonNilSlice pins the #255
// contract: zero tables surface as `[]`, not nil, so downstream JSON
// marshaling produces "[]" not "null".
func TestListDDBTables_EmptyAccountReturnsNonNilSlice(t *testing.T) {
	t.Parallel()
	fake := &fakeDDBSubresourceClient{tables: nil}
	got, err := listDDBTablesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("got=nil, want non-nil empty slice (#255 JSON-shape contract)")
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

// TestFetchDDBContributorInsights_EnabledEmitsExistsTrue pins that
// ContributorInsightsStatus=ENABLED yields exists=true plus the
// table_name NativeID.
func TestFetchDDBContributorInsights_EnabledEmitsExistsTrue(t *testing.T) {
	t.Parallel()
	fake := &fakeDDBSubresourceClient{
		insightsByT: map[string]dynamodb.DescribeContributorInsightsOutput{
			"users": {ContributorInsightsStatus: ddbtypes.ContributorInsightsStatusEnabled},
		},
	}
	exists, _, native, err := fetchDDBContributorInsightsWithClient(context.Background(), fake, "users")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true (Status=ENABLED)")
	}
	if native["table_name"] != "users" {
		t.Errorf("NativeIDs[table_name]=%q, want users", native["table_name"])
	}
}

// TestFetchDDBContributorInsights_EnablingEmitsExistsTrue pins that
// ENABLING (in-progress enable) still produces the TF resource — the
// TF resource exists across the lifecycle from ENABLING through
// ENABLED.
func TestFetchDDBContributorInsights_EnablingEmitsExistsTrue(t *testing.T) {
	t.Parallel()
	fake := &fakeDDBSubresourceClient{
		insightsByT: map[string]dynamodb.DescribeContributorInsightsOutput{
			"users": {ContributorInsightsStatus: ddbtypes.ContributorInsightsStatusEnabling},
		},
	}
	exists, _, _, err := fetchDDBContributorInsightsWithClient(context.Background(), fake, "users")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true (Status=ENABLING)")
	}
}

// TestFetchDDBContributorInsights_DisabledEmitsExistsFalse pins that
// DISABLED status maps to exists=false (TF resource not present, even
// though the table itself exists).
func TestFetchDDBContributorInsights_DisabledEmitsExistsFalse(t *testing.T) {
	t.Parallel()
	for _, status := range []ddbtypes.ContributorInsightsStatus{
		ddbtypes.ContributorInsightsStatusDisabled,
		ddbtypes.ContributorInsightsStatusDisabling,
		ddbtypes.ContributorInsightsStatusFailed,
	} {
		fake := &fakeDDBSubresourceClient{
			insightsByT: map[string]dynamodb.DescribeContributorInsightsOutput{
				"users": {ContributorInsightsStatus: status},
			},
		}
		exists, _, _, err := fetchDDBContributorInsightsWithClient(context.Background(), fake, "users")
		if err != nil {
			t.Errorf("%s: err=%v, want nil", status, err)
		}
		if exists {
			t.Errorf("%s: exists=true, want false (TF resource models active enablement only)", status)
		}
	}
}

// TestFetchDDBContributorInsights_NotFoundSwallowed pins the race
// case: ListTables emitted a table that vanished before
// DescribeContributorInsights ran. ResourceNotFoundException must
// surface as exists=false rather than warn-spam.
func TestFetchDDBContributorInsights_NotFoundSwallowed(t *testing.T) {
	t.Parallel()
	fake := &fakeDDBSubresourceClient{
		insightsErrT: map[string]error{
			"missing": fakeAPIErr("ResourceNotFoundException", "table disappeared"),
		},
	}
	exists, _, _, err := fetchDDBContributorInsightsWithClient(context.Background(), fake, "missing")
	if err != nil {
		t.Fatalf("err=%v, want nil (ResourceNotFoundException swallowed)", err)
	}
	if exists {
		t.Error("exists=true, want false")
	}
}

// TestFetchDDBContributorInsights_PropagatesGenericError pins that
// errors other than ResourceNotFoundException propagate so the bulk
// Discover path can emit a ServiceWarn.
func TestFetchDDBContributorInsights_PropagatesGenericError(t *testing.T) {
	t.Parallel()
	fake := &fakeDDBSubresourceClient{
		insightsErrT: map[string]error{
			"users": fakeAPIErr("AccessDenied", "no perms"),
		},
	}
	_, _, _, err := fetchDDBContributorInsightsWithClient(context.Background(), fake, "users")
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestNewDDBSubresourceClient_ProductionFactoryReturnsRealClient pins
// the production factory's contract: a real *dynamodb.Client (not nil),
// constructed from the supplied aws.Config.
func TestNewDDBSubresourceClient_ProductionFactoryReturnsRealClient(t *testing.T) {
	t.Parallel()
	c := newDDBSubresourceClient(aws.Config{Region: "us-east-1"}, "us-east-1")
	if c == nil {
		t.Fatal("newDDBSubresourceClient returned nil")
	}
}
