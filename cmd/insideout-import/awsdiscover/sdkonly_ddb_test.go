package awsdiscover

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
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
// Discover path can emit a ServiceWarn. errors.Is on the seed catches
// a regression that wraps the SDK error as a different sentinel.
func TestFetchDDBContributorInsights_PropagatesGenericError(t *testing.T) {
	t.Parallel()
	seedErr := fakeAPIErr("AccessDenied", "no perms")
	fake := &fakeDDBSubresourceClient{
		insightsErrT: map[string]error{"users": seedErr},
	}
	_, _, _, err := fetchDDBContributorInsightsWithClient(context.Background(), fake, "users")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err does not wrap seedErr: got %v", err)
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

// TestDDBContributorInsightsImportID is the #cust3 item-5 regression: the
// Terraform import ID for a table-level contributor-insights binding is
// "<table>//<account>" (empty index_name segment), NOT the bare table
// name. Verified against AWS provider v6 generate-config-out, which
// rejects the bare table name with "unexpected format for ID (...),
// expected table_name/index_name/account_id" — the rejection silently
// dropped io-a0ibmrskxfqc-app as no_generated_config.
func TestDDBContributorInsightsImportID(t *testing.T) {
	t.Parallel()
	t.Run("table-level with account -> compound", func(t *testing.T) {
		t.Parallel()
		props := map[string]any{subresourceAccountIDKey: "031780745048"}
		got := ddbContributorInsightsImportID("io-a0ibmrskxfqc-app", props)
		if got != "io-a0ibmrskxfqc-app//031780745048" {
			t.Errorf("import ID = %q, want io-a0ibmrskxfqc-app//031780745048", got)
		}
	})
	t.Run("no account -> bare table fallback (no regression)", func(t *testing.T) {
		t.Parallel()
		if got := ddbContributorInsightsImportID("users", map[string]any{}); got != "users" {
			t.Errorf("import ID = %q, want bare table fallback users", got)
		}
		if got := ddbContributorInsightsImportID("users", nil); got != "users" {
			t.Errorf("import ID (nil props) = %q, want users", got)
		}
	})
	t.Run("compound parent id strips to table for the prefix", func(t *testing.T) {
		t.Parallel()
		// dep-chase may hand the compound import ID back as parentID; the
		// table must be re-extracted before re-building.
		props := map[string]any{subresourceAccountIDKey: "031780745048"}
		got := ddbContributorInsightsImportID("io-a0ibmrskxfqc-app//031780745048", props)
		if got != "io-a0ibmrskxfqc-app//031780745048" {
			t.Errorf("import ID = %q, want stable io-a0ibmrskxfqc-app//031780745048", got)
		}
	})
}

// TestFetchDDBContributorInsights_StripsCompoundParentID proves the
// DescribeContributorInsights call uses the bare table name even when the
// FetchItem is handed the compound import ID (dep-chase DiscoverByID
// path) — the DDB API takes the table name, not the compound id.
func TestFetchDDBContributorInsights_StripsCompoundParentID(t *testing.T) {
	t.Parallel()
	fake := &fakeDDBSubresourceClient{
		insightsByT: map[string]dynamodb.DescribeContributorInsightsOutput{
			"users": {ContributorInsightsStatus: ddbtypes.ContributorInsightsStatusEnabled},
		},
	}
	exists, _, native, err := fetchDDBContributorInsightsWithClient(context.Background(), fake, "users//031780745048")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true (compound id should resolve table 'users')")
	}
	if native["table_name"] != "users" {
		t.Errorf("NativeIDs[table_name]=%q, want bare table users", native["table_name"])
	}
}

// TestDDBContributorInsightsEnricher_TableFromCompoundImportID proves the
// enricher recovers the bare table name from the compound import ID (it
// no longer trusts ImportID verbatim).
func TestDDBContributorInsightsEnricher_TableFromCompoundImportID(t *testing.T) {
	t.Parallel()
	id := &imported.ResourceIdentity{
		Type:     "aws_dynamodb_contributor_insights",
		ImportID: "io-a0ibmrskxfqc-app//031780745048",
		NameHint: "io-a0ibmrskxfqc-app-contributor-insights",
	}
	table, err := ddbContributorInsightsTableName(id)
	if err != nil {
		t.Fatal(err)
	}
	if table != "io-a0ibmrskxfqc-app" {
		t.Errorf("table = %q, want io-a0ibmrskxfqc-app (compound import ID must be split)", table)
	}
}
