package generated

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHCLRoundTrip exercises every Phase 1 generated type via a fixture
// under testdata/fixtures/<tfType>.tf. Each fixture must include at least
// one expression-valued field and one explicit null so all Value[T]
// states round-trip.
//
// Round-trip strategy: parse → typed model → emit → parse-again → assert
// the second-pass typed model is deep-equal to the first. Direct byte
// equality is not asserted because hclwrite normalizes alignment / newlines
// in ways that are immaterial to semantic correctness.
func TestHCLRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tfType string
	}{
		{"aws_sqs_queue"},
		{"aws_dynamodb_table"},
		{"aws_cloudwatch_log_group"},
		{"aws_secretsmanager_secret"},
		{"aws_lambda_function"},
		{"google_storage_bucket"},
		{"google_compute_network"},
		{"google_secret_manager_secret"},
		{"google_pubsub_topic"},
		{"google_pubsub_subscription"},
	} {
		t.Run(tc.tfType, func(t *testing.T) {
			t.Parallel()
			fixturePath := filepath.Join("testdata", "fixtures", tc.tfType+".tf")
			src, err := os.ReadFile(fixturePath)
			require.NoErrorf(t, err, "fixture %s missing", fixturePath)

			goType, _, ok := Lookup(tc.tfType)
			require.True(t, ok, "type %q not registered", tc.tfType)

			// First pass: parse fixture into a freshly allocated typed
			// value.
			first := reflect.New(goType)
			require.NoError(t, parseHCL(t, src, first.Interface()))

			// Re-emit.
			emitted, err := MarshalHCL(first.Interface())
			require.NoError(t, err)

			// Second pass: parse the emitted bytes back.
			second := reflect.New(goType)
			require.NoError(t, parseHCL(t, emitted, second.Interface()),
				"re-parse failed; emitted source:\n%s", emitted)

			// Deep equality — same typed model means no semantic loss.
			assert.True(t,
				reflect.DeepEqual(first.Interface(), second.Interface()),
				"round-trip not deep-equal for %s\nemitted:\n%s",
				tc.tfType, string(emitted))
		})
	}
}

func parseHCL(t *testing.T, src []byte, into any) error {
	t.Helper()
	file, diags := hclsyntax.ParseConfig(src, "fixture.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return diags
	}
	body, ok := file.Body.(*hclsyntax.Body)
	require.True(t, ok)
	return UnmarshalHCL(src, body, into)
}
