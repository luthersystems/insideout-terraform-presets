package composer

import terraformpresets "github.com/luthersystems/insideout-terraform-presets"

// testPresetFS is the preset filesystem used by composer tests. The invariant
// "pkg/composer must have zero Luther-org imports" applies to non-test files
// only; this test-only import is permitted and enables composer tests to run
// against the real preset bundle.
var testPresetFS = terraformpresets.FS

// newTestClient returns a Client preconfigured with the embedded preset FS.
// Additional options override defaults in declaration order.
func newTestClient(opts ...Option) *Client {
	return New(append([]Option{WithPresets(testPresetFS)}, opts...)...)
}
