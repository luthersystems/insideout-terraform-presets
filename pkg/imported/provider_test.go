package imported_test

import (
	"context"
	"testing"

	composer_imported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
	// Side-effect imports populate the Provider registry.
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/imported/aws"
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/imported/gcp"
)

// stubProvider is a minimal Provider used to exercise the interface
// shape and the registry. It carries no cloud-side state and returns
// zero values for every method — the goal is to lock the interface
// shape, not to test live behavior (the per-cloud provider tests
// cover that).
type stubProvider struct {
	name string
}

func (s *stubProvider) SupportedTypes() []string                { return []string{"stub_type"} }
func (s *stubProvider) Capabilities(string) imp.Capabilities    { return imp.Capabilities{} }
func (s *stubProvider) LabelFor(string) (string, string)        { return "", "" }
func (s *stubProvider) PolicyFor(string) (policy.Map, bool)     { return nil, false }
func (s *stubProvider) MetricsBinding(string) (imp.ComponentMetricsBinding, bool) {
	return imp.ComponentMetricsBinding{}, false
}
func (s *stubProvider) StableID(*composer_imported.ResourceIdentity) string         { return "" }
func (s *stubProvider) CanonicalAddress(*composer_imported.ResourceIdentity) string { return "" }
func (s *stubProvider) Discover(context.Context, []string, imp.Clients, imp.DiscoverOpts) ([]composer_imported.ImportedResource, error) {
	return nil, nil
}
func (s *stubProvider) EnrichAttributes(context.Context, []composer_imported.ImportedResource, imp.Clients) error {
	return nil
}
func (s *stubProvider) EnrichByID(context.Context, *composer_imported.ResourceIdentity, imp.Clients) (imp.Attrs, error) {
	return nil, nil
}
func (s *stubProvider) CompareDrift(string, imp.Attrs, imp.Attrs) []imp.FieldMismatch { return nil }
func (s *stubProvider) RileyContext([]composer_imported.ImportedResource) []string    { return nil }

// Compile-time interface satisfaction check.
var _ imp.Provider = (*stubProvider)(nil)

func TestProviderInterfaceShape(t *testing.T) {
	t.Parallel()

	var p imp.Provider = &stubProvider{name: "stub"}

	// Exercise every method to lock the shape.
	if got := p.SupportedTypes(); len(got) != 1 || got[0] != "stub_type" {
		t.Errorf("SupportedTypes: got %v", got)
	}
	_ = p.Capabilities("x")
	_, _ = p.LabelFor("x")
	_, _ = p.PolicyFor("x")
	_, _ = p.MetricsBinding("x")
	_ = p.StableID(nil)
	_ = p.CanonicalAddress(nil)
	if _, err := p.Discover(context.Background(), nil, imp.Clients{}, imp.DiscoverOpts{}); err != nil {
		t.Errorf("Discover stub returned err: %v", err)
	}
	if err := p.EnrichAttributes(context.Background(), nil, imp.Clients{}); err != nil {
		t.Errorf("EnrichAttributes stub returned err: %v", err)
	}
	if _, err := p.EnrichByID(context.Background(), nil, imp.Clients{}); err != nil {
		t.Errorf("EnrichByID stub returned err: %v", err)
	}
	_ = p.CompareDrift("x", nil, nil)
	_ = p.RileyContext(nil)
}

func TestErrUnknownCloud(t *testing.T) {
	t.Parallel()

	if _, err := imp.ProviderFor("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown cloud")
	}
}
