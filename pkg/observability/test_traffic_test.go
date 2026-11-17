package observability

import (
	"io/fs"
	"path"
	"testing"
	"testing/fstest"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTestTrafficPublicEndpoints_OnlyKnownKeys ensures every key in
// TestTrafficPublicEndpoints is in composer.AllComponentKeys.
func TestTestTrafficPublicEndpoints_OnlyKnownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range TestTrafficPublicEndpoints {
		assert.True(t, known[k],
			"TestTrafficPublicEndpoints[%s] is not in composer.AllComponentKeys — stale or typo'd key",
			k)
	}
}

// TestTestTrafficPublicEndpoints_OnlyAWSKeys ensures the test-traffic
// allow-list is AWS-only today (we don't have a GCP equivalent, and
// the contract doc in lib/hooks/useTestTraffic.ts assumes AWS shapes).
// New GCP keys would need a parallel allow-list and a GCP-aware
// resolvePublicEndpointURL.
func TestTestTrafficPublicEndpoints_OnlyAWSKeys(t *testing.T) {
	for k := range TestTrafficPublicEndpoints {
		assert.Equal(t, "aws", composer.CloudFor(k),
			"TestTrafficPublicEndpoints[%s] is non-AWS; the resolver only knows AWS output shapes",
			k)
	}
}

// TestTestTrafficPublicEndpoints_HasOutputKey ensures every entry
// declares a non-empty OutputKey — empty would break the URL resolver.
func TestTestTrafficPublicEndpoints_HasOutputKey(t *testing.T) {
	for k, v := range TestTrafficPublicEndpoints {
		assert.NotEmpty(t, v.OutputKey,
			"TestTrafficPublicEndpoints[%s].OutputKey is empty", k)
	}
}

// TestTestTrafficPublicEndpoints_HasMatchingOutput is the drift gate
// between this allow-list and the presets. The URL resolver reads the
// output named by OutputKey from the deployed stack, so if a preset
// renames or drops that output the resolver silently finds nothing.
// For each entry it parses <cloud>/<preset>/outputs.tf from the
// embedded preset FS with hcl/v2 and fails when OutputKey is not a
// declared output block. The directory comes from composer.GetPresetPath,
// the same mapping the composer uses, so the test reads the file the
// composer would ship.
func TestTestTrafficPublicEndpoints_HasMatchingOutput(t *testing.T) {
	for k, v := range TestTrafficPublicEndpoints {
		presetPath := composer.GetPresetPath(composer.CloudFor(k), k, nil)
		outputs, err := declaredOutputs(terraformpresets.FS, path.Join(presetPath, "outputs.tf"))
		require.NoError(t, err, "TestTrafficPublicEndpoints[%s]", k)
		assert.True(t, outputs[v.OutputKey],
			"TestTrafficPublicEndpoints[%s].OutputKey %q is not an output in %s/outputs.tf",
			k, v.OutputKey, presetPath)
	}
}

// TestDeclaredOutputs proves the gate can fail: a name that is not an
// output block (a variable, a resource, or nothing) is not reported.
func TestDeclaredOutputs(t *testing.T) {
	fsys := fstest.MapFS{
		"m/outputs.tf": &fstest.MapFile{Data: []byte(`
output "alb_dns_name" {
  value = aws_lb.this.dns_name
}

variable "api_endpoint" {
  type = string
}
`)},
		"bad/outputs.tf": &fstest.MapFile{Data: []byte(`output "x" {`)},
	}

	got, err := declaredOutputs(fsys, "m/outputs.tf")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"alb_dns_name": true}, got)

	_, err = declaredOutputs(fsys, "missing/outputs.tf")
	require.Error(t, err)

	_, err = declaredOutputs(fsys, "bad/outputs.tf")
	require.Error(t, err)
}

// declaredOutputs returns the names of the output blocks in one .tf file.
// Other block types (variable, resource, locals) are ignored.
func declaredOutputs(fsys fs.FS, name string) (map[string]bool, error) {
	src, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	file, diags := hclparse.NewParser().ParseHCL(src, name)
	if diags.HasErrors() {
		return nil, diags
	}
	content, _, diags := file.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "output", LabelNames: []string{"name"}}},
	})
	if diags.HasErrors() {
		return nil, diags
	}
	out := map[string]bool{}
	for _, b := range content.Blocks {
		out[b.Labels[0]] = true
	}
	return out, nil
}
