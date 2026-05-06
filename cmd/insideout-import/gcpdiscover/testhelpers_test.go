package gcpdiscover

import "context"

// fakeAssetSearcher is the unit-test seam that replaces RealAssetSearcher.
// Tests configure `pages` (the canned response slice) and `err` (forced
// failure). Each SearchAll call appends to `calls` so assertions can pin
// the scope, asset-types, and query the discoverer threaded through.
type fakeAssetSearcher struct {
	results []gcpAssetResult
	err     error

	calls []searchAllCall
}

type searchAllCall struct {
	scope      string
	assetTypes []string
	query      string
}

func (f *fakeAssetSearcher) SearchAll(_ context.Context, scope string, assetTypes []string, query string) ([]gcpAssetResult, error) {
	cp := make([]string, len(assetTypes))
	copy(cp, assetTypes)
	f.calls = append(f.calls, searchAllCall{scope: scope, assetTypes: cp, query: query})
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}
