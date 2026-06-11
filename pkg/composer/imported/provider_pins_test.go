package imported

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBaseProviderPins_ExactAndMatchMars is the cross-repo drift guard for the
// provider pins (#786). These literals MUST equal the versions baked into the
// luthersystems/mars provider-mirror (mars/Dockerfile AWS_PROVIDER_VERSION /
// GOOGLE_PROVIDER_VERSION); a change on either side without the other re-opens
// the "deploy resolves newest at runtime" hazard (the 6.49→6.50 breakage). The
// assertion reads the literal strings (not the symbol on both sides) so a
// value edit surfaces here as a deliberate diff. Bump these AND the mars bake
// together.
func TestBaseProviderPins_ExactAndMatchMars(t *testing.T) {
	t.Parallel()
	// As of mars v0.123.0: aws 6.46.0, google/google-beta 6.10.0.
	assert.Equal(t, "= 6.46.0", BaseProviderPin("aws", "aws"))
	assert.Equal(t, "= 6.10.0", BaseProviderPin("gcp", "google"))
	assert.Equal(t, "= 6.10.0", BaseProviderPin("gcp", "google-beta"))
}

// TestBaseProviderPins_AreExactNotRanges pins that every base-provider
// constraint is an EXACT `=` pin, never an open range (>=, ~>) — an open
// range would let terraform init resolve a newer provider at runtime and miss
// the mars filesystem-mirror cache.
func TestBaseProviderPins_AreExactNotRanges(t *testing.T) {
	t.Parallel()
	for _, cloud := range []string{"aws", "gcp"} {
		for provider, constraint := range BaseProviderPins(cloud) {
			assert.Truef(t, strings.HasPrefix(constraint, "= "),
				"%s/%s pin %q must be an exact `=` constraint, not an open range", cloud, provider, constraint)
			assert.NotContainsf(t, constraint, ">=", "%s/%s pin must not use >=", cloud, provider)
			assert.NotContainsf(t, constraint, "~>", "%s/%s pin must not use ~>", cloud, provider)
		}
	}
}

// TestBaseProviderPin_UnknownReturnsEmpty pins the documented fallback for an
// unknown cloud/provider pair.
func TestBaseProviderPin_UnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", BaseProviderPin("azure", "azurerm"))
	assert.Equal(t, "", BaseProviderPin("aws", "google"))
	assert.Nil(t, BaseProviderPins("azure"))
}

// TestBaseProviderPins_ReturnsCopy pins that BaseProviderPins hands back a
// copy, so a caller that re-asserts pins after a discovered-provider union
// cannot mutate the source of truth.
func TestBaseProviderPins_ReturnsCopy(t *testing.T) {
	t.Parallel()
	got := BaseProviderPins("aws")
	got["aws"] = "= 9.9.9"
	assert.Equal(t, "= 6.46.0", BaseProviderPin("aws", "aws"),
		"mutating the returned map must not affect the source of truth")
}

// TestAllBaseProviderPins covers the flattened map the pre-init validator
// seeds from — it MUST include every base provider the emitter pins, so a
// preset pinning an incompatible range for any of them (notably google-beta)
// is caught before terraform init.
func TestAllBaseProviderPins(t *testing.T) {
	t.Parallel()
	all := AllBaseProviderPins()
	assert.Equal(t, "= 6.46.0", all["aws"])
	assert.Equal(t, "= 6.10.0", all["google"])
	assert.Equal(t, "= 6.10.0", all["google-beta"],
		"google-beta must be seeded — the emitter pins it, so the validator must too")
	assert.Len(t, all, 3, "exactly the three base providers across aws+gcp")
	// Returned map is a fresh copy; mutating it must not affect the source.
	all["aws"] = "= 9.9.9"
	assert.Equal(t, "= 6.46.0", AllBaseProviderPins()["aws"])
}
