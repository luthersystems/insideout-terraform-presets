package imported_test

import (
	"errors"
	"testing"

	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
	// Side-effect imports populate the Provider registry.
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/imported/aws"
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/imported/gcp"
)

func TestProviderFor_AWS(t *testing.T) {
	t.Parallel()
	p, err := imp.ProviderFor("aws")
	if err != nil {
		t.Fatalf("ProviderFor(aws): %v", err)
	}
	if p == nil {
		t.Fatal("ProviderFor(aws): returned nil Provider with nil error")
	}
	// Smoke: SupportedTypes returns the AWS type list.
	types := p.SupportedTypes()
	if len(types) == 0 {
		t.Fatal("SupportedTypes() returned empty for AWS")
	}
	// Sanity: the list looks like AWS types, not GCP types.
	if got := types[0]; len(got) < 4 || got[:4] != "aws_" {
		t.Errorf("first AWS type %q does not look AWS-shaped", got)
	}
}

func TestProviderFor_GCP(t *testing.T) {
	t.Parallel()
	p, err := imp.ProviderFor("gcp")
	if err != nil {
		t.Fatalf("ProviderFor(gcp): %v", err)
	}
	if p == nil {
		t.Fatal("ProviderFor(gcp): returned nil Provider with nil error")
	}
	types := p.SupportedTypes()
	if len(types) == 0 {
		t.Fatal("SupportedTypes() returned empty for GCP")
	}
	if got := types[0]; len(got) < 7 || got[:7] != "google_" {
		t.Errorf("first GCP type %q does not look GCP-shaped", got)
	}
}

func TestProviderFor_Unknown(t *testing.T) {
	t.Parallel()
	_, err := imp.ProviderFor("azure")
	if err == nil {
		t.Fatal("expected error for unknown cloud")
	}
	if !errors.Is(err, imp.ErrUnknownCloud) {
		t.Errorf("expected ErrUnknownCloud, got %v", err)
	}
}

func TestRegisteredClouds(t *testing.T) {
	t.Parallel()
	clouds := imp.RegisteredClouds()
	if len(clouds) < 2 {
		t.Fatalf("expected at least 2 registered clouds, got %v", clouds)
	}
	want := map[string]bool{"aws": false, "gcp": false}
	for _, c := range clouds {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("expected %q in RegisteredClouds, got %v", k, clouds)
		}
	}
	// Sorted.
	for i := 1; i < len(clouds); i++ {
		if clouds[i-1] > clouds[i] {
			t.Errorf("RegisteredClouds not sorted: %v", clouds)
		}
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	imp.Register("aws", func() imp.Provider { return nil })
}

func TestRegisterPanicsOnEmptyCloud(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on empty cloud")
		}
	}()
	imp.Register("", func() imp.Provider { return nil })
}

func TestRegisterPanicsOnNilCtor(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil ctor")
		}
	}()
	imp.Register("synthetic-cloud", nil)
}
