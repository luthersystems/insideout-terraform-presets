package imported_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
)

// existProvider embeds the shared stubProvider and makes EnrichByID
// configurable per identity, so a test can drive ClassifyExistence /
// FilterExisting without a live cloud.
type existProvider struct {
	*stubProvider
	byID func(id *composerimported.ResourceIdentity) (imp.Attrs, error)
}

func (e *existProvider) EnrichByID(_ context.Context, id *composerimported.ResourceIdentity, _ imp.Clients) (imp.Attrs, error) {
	return e.byID(id)
}

var _ imp.Provider = (*existProvider)(nil)

func identFor(addr string) *composerimported.ResourceIdentity {
	return &composerimported.ResourceIdentity{Address: addr, Type: "aws_iam_role"}
}

func mkExistProvider(fn func(id *composerimported.ResourceIdentity) (imp.Attrs, error)) *existProvider {
	return &existProvider{stubProvider: &stubProvider{}, byID: fn}
}

// TestFilterExisting_OnlyDefinitiveNotFoundIsGone is the load-bearing
// contract: a resource is pruned ONLY on a definitive ErrResourceNotFound.
// A live resource, a type with no by-id probe, and a transient/throttle
// error are all KEPT — never a false-drop.
func TestFilterExisting_OnlyDefinitiveNotFoundIsGone(t *testing.T) {
	p := mkExistProvider(func(id *composerimported.ResourceIdentity) (imp.Attrs, error) {
		switch id.Address {
		case "aws_iam_role.live":
			return imp.Attrs("{}"), nil
		case "aws_iam_role.gone":
			// Mirror the provider's not-found wrap (sentinel + underlying).
			return nil, fmt.Errorf("%w: %w", imp.ErrResourceNotFound, errors.New("NoSuchEntity: role not found"))
		case "aws_iam_role.noprobe":
			return nil, fmt.Errorf("%w: aws_iam_role", imp.ErrEnrichByIDNotImplemented)
		default: // "aws_iam_role.throttled"
			return nil, errors.New("ThrottlingException: rate exceeded")
		}
	})

	ids := []*composerimported.ResourceIdentity{
		identFor("aws_iam_role.live"),
		identFor("aws_iam_role.gone"),
		identFor("aws_iam_role.noprobe"),
		identFor("aws_iam_role.throttled"),
		nil, // skipped, must not panic
	}
	gone, err := imp.FilterExisting(context.Background(), p, imp.Clients{}, ids)
	if err != nil {
		t.Fatalf("FilterExisting err: %v", err)
	}
	if len(gone) != 1 {
		addrs := make([]string, len(gone))
		for i, g := range gone {
			addrs[i] = g.Address
		}
		t.Fatalf("expected exactly 1 gone (the definitive not-found); got %d: %v", len(gone), addrs)
	}
	if gone[0].Address != "aws_iam_role.gone" {
		t.Fatalf("wrong resource classified gone: %s", gone[0].Address)
	}
}

func TestClassifyExistence_Verdicts(t *testing.T) {
	cases := []struct {
		name string
		fn   func() (imp.Attrs, error)
		want imp.ExistenceVerdict
	}{
		{"exists", func() (imp.Attrs, error) { return imp.Attrs("{}"), nil }, imp.ExistenceExists},
		{"gone", func() (imp.Attrs, error) { return nil, fmt.Errorf("%w: 404", imp.ErrResourceNotFound) }, imp.ExistenceGone},
		{"no-probe-is-unknown", func() (imp.Attrs, error) { return nil, imp.ErrEnrichByIDNotImplemented }, imp.ExistenceUnknown},
		{"client-unavailable-is-unknown", func() (imp.Attrs, error) { return nil, imp.ErrEnrichClientUnavailable }, imp.ExistenceUnknown},
		{"transient-is-unknown", func() (imp.Attrs, error) { return nil, errors.New("timeout") }, imp.ExistenceUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mkExistProvider(func(*composerimported.ResourceIdentity) (imp.Attrs, error) { return tc.fn() })
			got := imp.ClassifyExistence(context.Background(), p, imp.Clients{}, identFor("aws_iam_role.x"))
			if got != tc.want {
				t.Fatalf("ClassifyExistence = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyExistence_NilInputsAreUnknown(t *testing.T) {
	p := mkExistProvider(func(*composerimported.ResourceIdentity) (imp.Attrs, error) { return imp.Attrs{}, nil })
	if v := imp.ClassifyExistence(context.Background(), nil, imp.Clients{}, identFor("a")); v != imp.ExistenceUnknown {
		t.Fatalf("nil provider must be Unknown, got %v", v)
	}
	if v := imp.ClassifyExistence(context.Background(), p, imp.Clients{}, nil); v != imp.ExistenceUnknown {
		t.Fatalf("nil identity must be Unknown, got %v", v)
	}
}

func TestFilterExisting_NilProviderErrors(t *testing.T) {
	if _, err := imp.FilterExisting(context.Background(), nil, imp.Clients{}, nil); err == nil {
		t.Fatal("nil provider must return an error")
	}
}
