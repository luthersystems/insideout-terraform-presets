package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDiscoverRequiredProviders_Basic covers the common shapes a
// required_providers block takes in preset .tf files: version-only entries
// and source/version pairs.
func TestDiscoverRequiredProviders_Basic(t *testing.T) {
	files := map[string][]byte{
		"/versions.tf": []byte(`terraform {
  required_providers {
    opensearch = {
      source  = "opensearch-project/opensearch"
      version = "~> 2.3"
    }
    time = {
      source  = "hashicorp/time"
      version = ">= 0.9"
    }
  }
}
`),
	}
	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	require.Equal(t, RequiredProvider{Source: "opensearch-project/opensearch", Version: "~> 2.3"}, got["opensearch"])
	require.Equal(t, RequiredProvider{Source: "hashicorp/time", Version: ">= 0.9"}, got["time"])
}

// TestDiscoverRequiredProviders_UnionAcrossFiles verifies that
// required_providers declarations split across multiple files in a module
// are all captured.
func TestDiscoverRequiredProviders_UnionAcrossFiles(t *testing.T) {
	files := map[string][]byte{
		"/main.tf": []byte(`terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 6.0" }
  }
}
`),
		"/versions.tf": []byte(`terraform {
  required_providers {
    opensearch = { source = "opensearch-project/opensearch", version = "~> 2.3" }
  }
}
`),
	}
	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	require.Contains(t, got, "aws")
	require.Contains(t, got, "opensearch")
	require.Equal(t, "hashicorp/aws", got["aws"].Source)
}

// TestDiscoverRequiredProviders_IgnoresNonTerraformBlocks confirms that
// resource/module/variable/output blocks don't confuse the walker.
func TestDiscoverRequiredProviders_IgnoresNonTerraformBlocks(t *testing.T) {
	files := map[string][]byte{
		"/main.tf": []byte(`variable "x" { type = string }
resource "null_resource" "r" {}
output "y" { value = "z" }
terraform {
  required_providers {
    opensearch = { source = "opensearch-project/opensearch" }
  }
}
`),
	}
	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "opensearch-project/opensearch", got["opensearch"].Source)
}

// TestDiscoverRequiredProviders_VersionOptional handles the case where only
// `source` is present (valid HCL; Terraform will pick any available
// version).
func TestDiscoverRequiredProviders_VersionOptional(t *testing.T) {
	files := map[string][]byte{
		"/versions.tf": []byte(`terraform {
  required_providers {
    local = { source = "hashicorp/local" }
  }
}
`),
	}
	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	require.Equal(t, RequiredProvider{Source: "hashicorp/local", Version: ""}, got["local"])
}

// TestDiscoverRequiredProviders_NoTerraformBlock returns an empty map
// rather than erroring when a module has no terraform{} block.
func TestDiscoverRequiredProviders_NoTerraformBlock(t *testing.T) {
	files := map[string][]byte{
		"/main.tf": []byte(`resource "null_resource" "r" {}
`),
	}
	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestDiscoverRequiredProviders_SkipsUnparseable mirrors the DiscoverModuleVars
// contract: malformed files are ignored rather than failing the whole walk.
func TestDiscoverRequiredProviders_SkipsUnparseable(t *testing.T) {
	files := map[string][]byte{
		"/broken.tf": []byte(`this is not valid hcl {{{`),
		"/versions.tf": []byte(`terraform {
  required_providers {
    opensearch = { source = "opensearch-project/opensearch", version = "~> 2.3" }
  }
}
`),
	}
	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "opensearch-project/opensearch", got["opensearch"].Source)
}

// TestDiscoverRequiredProviders_IgnoresNonTFFiles confirms that non-.tf
// entries (e.g., .tfvars, .md) are skipped.
func TestDiscoverRequiredProviders_IgnoresNonTFFiles(t *testing.T) {
	files := map[string][]byte{
		"/README.md":    []byte(`# not a terraform file`),
		"/config.tfvars": []byte(`foo = "bar"`),
	}
	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestDiscoverRequiredProviders_DeterministicOnConflict locks in the
// "first file in sorted order wins" contract when the same provider
// appears in multiple files with different source/version.
//
// Go's map iteration is randomized per-map per-process, but for small
// maps the iteration often lands close to insertion order — so a
// loop-based retry in a single process gives false confidence. Instead
// we rely on the determinism coming from `sort.Strings(paths)` (a pure
// function) and construct >8 conflicting files to force Go's full
// randomization regime. If the sort is removed, the test becomes flaky
// immediately; if the sort is correct, one call is enough.
func TestDiscoverRequiredProviders_DeterministicOnConflict(t *testing.T) {
	files := map[string][]byte{}
	for _, n := range []string{"00", "11", "22", "33", "44", "55", "66", "77", "88", "99"} {
		files["/"+n+"_versions.tf"] = []byte(`terraform {
  required_providers {
    opensearch = { source = "opensearch-project/opensearch", version = "= ` + n + `.0.0" }
  }
}
`)
	}

	// Two calls — asserting equality catches any residual nondeterminism
	// cheaply without relying on a retry loop for confidence.
	got1, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	got2, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	require.Equal(t, got1, got2, "discovery must be deterministic across calls")

	require.Equal(t, "= 00.0.0", got1["opensearch"].Version,
		"sorted path order must make the lexicographically-first file win (not map iteration order)")
}

// TestDiscoverRequiredProviders_EmbeddedPresetOpenSearch_Skeleton asserts
// what the CURRENT pinned preset's aws/opensearch declares — only
// hashicorp/aws today. This is the "infra skeleton" approach the preset
// took in #69: the AOSS provider plugin isn't a required_provider yet
// because data-access policies and vector indexes are application-layer.
// When the preset grows those (tracked upstream), flip this test over to
// TestDiscoverRequiredProviders_EmbeddedPresetOpenSearch_FullAOSS below.
func TestDiscoverRequiredProviders_EmbeddedPresetOpenSearch_Skeleton(t *testing.T) {
	c := newTestClient()
	files, err := c.GetPresetFiles("aws/opensearch")
	require.NoError(t, err)
	require.NotEmpty(t, files, "aws/opensearch preset must be readable")

	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)

	require.Contains(t, got, "aws", "opensearch preset must declare the aws provider")
	require.Equal(t, "hashicorp/aws", got["aws"].Source)

	// Verify discovery produces well-formed entries across the board.
	for name, rp := range got {
		require.NotEmpty(t, name, "discovered provider names must be non-empty")
		require.NotEmpty(t, rp.Source, "discovered providers must have a source")
	}
}

// TestDiscoverRequiredProviders_EmbeddedPresetOpenSearch_FullAOSS is a
// forward-looking test that will assert the post-AOSS-full-implementation
// shape of the opensearch preset. Currently skipped because the preset
// took the narrower infra-skeleton approach in #69 (collection + IAM
// role only; data-access policies + vector index are application-layer).
//
// When the preset grows data-access-policy or vector-index provisioning
// that require the opensearch-project/opensearch and hashicorp/time
// providers at module scope, delete the t.Skip and this becomes the real
// regression test. A skipped test is the right tripwire here — it forces
// a decision at review time rather than silently drifting past.
//
// Tracked against: insideout-terraform-presets future PR to extend #69.
func TestDiscoverRequiredProviders_EmbeddedPresetOpenSearch_FullAOSS(t *testing.T) {
	t.Skip("pending preset extension beyond #69: awaiting opensearch-project/opensearch + hashicorp/time required_providers")

	c := newTestClient()
	files, err := c.GetPresetFiles("aws/opensearch")
	require.NoError(t, err)

	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)

	require.Contains(t, got, "opensearch",
		"AOSS full-stack preset must declare opensearch-project/opensearch")
	require.Equal(t, "opensearch-project/opensearch", got["opensearch"].Source)

	require.Contains(t, got, "time",
		"AOSS full-stack preset must declare hashicorp/time for index-readiness waits")
	require.Equal(t, "hashicorp/time", got["time"].Source)
}

// TestDiscoverRequiredProviders_EmbeddedPresetOpenSearch loads the actual
// aws/opensearch preset from the embedded FS and verifies the discovery
// picks up whatever required_providers the current preset declares. Lower
// bar than the skeleton / full-AOSS tests above — pure smoke coverage.
func TestDiscoverRequiredProviders_EmbeddedPresetOpenSearch(t *testing.T) {
	c := newTestClient()
	files, err := c.GetPresetFiles("aws/opensearch")
	require.NoError(t, err)
	require.NotEmpty(t, files, "aws/opensearch preset must be readable")

	got, err := DiscoverRequiredProviders(files)
	require.NoError(t, err)
	// Don't require a specific set of entries here — the preset shape
	// changes with upstream releases. The point is that discovery is
	// non-destructive on a real preset.
	for name, rp := range got {
		require.NotEmpty(t, name, "discovered provider names must be non-empty")
		require.NotEmpty(t, rp.Source, "discovered providers must have a source")
	}
}
