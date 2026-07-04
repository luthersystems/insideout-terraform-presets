package composer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/normalize"
)

// The repo's #1 recurring meta-bug is "a second emission path bypasses a
// generic fix" — issue #840: the compose path shipped un-normalized
// imported.tf while the reverse-import path normalized it, so a mutually-
// exclusive attribute pair (private_ip_list + private_ips) that reverse-import
// resolved still reached `terraform validate` on the compose path. There are
// two paths that must treat imported resources identically:
//
//   - Compose path: composeStackImpl in compose.go — EmitImportedTF then
//     normalize.NormalizeImportedHCL (AWS only).
//   - Reverse path: pkg/reverseimport/run.go — composer.EmitImportedTF then
//     normalize.NormalizeImportedHCL.
//
// These two tests are the drift guard for that class: a cheap textual pin that
// fails the moment either call site loses its NormalizeImportedHCL invocation,
// and a semantic guard that composes a fixture through both paths and asserts
// the imported.tf treatment invariants match. Builds on PR #842's emit-side
// dropUneditedComputedNestedObjectAttrs guard (which drops an inline
// aws_route_table.route on BOTH paths, since both go through EmitImportedTF).

// repoRootFromTest walks up from this test file's directory to the repo root
// (the dir containing go.mod), so the textual pin can read sibling-package
// source regardless of the process working directory.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) must resolve the test file path")
	dir := filepath.Dir(thisFile)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root (go.mod) above %s", filepath.Dir(thisFile))
	return ""
}

// TestEmitPathParity_TextualPin is the cheap structural half of the guard: it
// asserts that BOTH emit call sites still invoke normalize.NormalizeImportedHCL.
// Triggering incident: issue #840 — the compose path (compose.go) once emitted
// imported.tf WITHOUT the fixups the reverse-import path (run.go) applied, so
// the two paths diverged and a compose-generated stack failed `terraform
// validate` on a mutually-exclusive attribute pair the reverse path scrubbed.
// If either site drops the normalize call, this fails immediately — no compose
// round-trip required.
func TestEmitPathParity_TextualPin(t *testing.T) {
	t.Parallel()
	root := repoRootFromTest(t)

	const needle = "normalize.NormalizeImportedHCL"
	for _, rel := range []string{
		filepath.Join("pkg", "composer", "compose.go"),
		filepath.Join("pkg", "reverseimport", "run.go"),
	} {
		src, err := os.ReadFile(filepath.Join(root, rel))
		require.NoErrorf(t, err, "reading %s", rel)
		assert.Containsf(t, string(src), needle,
			"%s must call %s so the compose and reverse-import emit paths treat "+
				"imported.tf identically (issue #840: compose path shipped "+
				"un-normalized imported.tf while the reverse path normalized)",
			rel, needle)
	}
}

// parityFixtures builds a fresh []imported.ImportedResource for one emit pass.
// EmitImportedTF mutates its input slice (records the provenance decision per
// resource in ir.WeakLocked), so each path must be handed its own slice — this
// is called once per path.
//
// The fixture exercises three distinct treatment classes:
//
//  1. aws_network_interface carrying BOTH private_ips and private_ip_list —
//     the mutually-exclusive pair NormalizeImportedHCL must reconcile (it
//     always drops the *_list alternative form). subnet_id is included so the
//     resource is plannable and survives the compose path's dropUncomposable.
//  2. aws_route_table with an inline `route` — an Optional+Computed nested-
//     object attribute the PR #842 emit guard
//     (dropUneditedComputedNestedObjectAttrs) drops on BOTH paths. vpc_id is
//     included so the resource is plannable.
//  3. a plain aws_s3_bucket — the control that must survive both paths intact.
func parityFixtures() []imported.ImportedResource {
	return []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_network_interface",
				Address:  "aws_network_interface.eni",
				Region:   "us-west-2",
				ImportID: "eni-0123456789abcdef0",
			},
			Tier: imported.TierImportedFlat,
			Attrs: json.RawMessage(`{
				"subnet_id":{"literal":"subnet-0123456789abcdef0"},
				"private_ips":[{"literal":"10.1.134.121"}],
				"private_ip_list":[{"literal":"10.1.134.121"}]
			}`),
		},
		{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_route_table",
				Address:  "aws_route_table.rt",
				Region:   "us-west-2",
				ImportID: "rtb-0123456789abcdef0",
			},
			Tier: imported.TierImportedFlat,
			Attrs: json.RawMessage(`{
				"vpc_id":{"literal":"vpc-0123456789abcdef0"},
				"route":[{"cidr_block":{"literal":"10.0.0.0/16"},"gateway_id":{"literal":"local"}}]
			}`),
		},
		{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_s3_bucket",
				Address:  "aws_s3_bucket.control",
				Region:   "us-west-2",
				ImportID: "paritytest-control",
			},
			Tier: imported.TierImportedFlat,
			Attrs: json.RawMessage(`{
				"bucket":{"literal":"paritytest-control"},
				"region":{"literal":"us-west-2"}
			}`),
		},
	}
}

var (
	rePrivateIPs     = regexp.MustCompile(`(?m)^\s*private_ips\s*=`)
	rePrivateIPList  = regexp.MustCompile(`(?m)^\s*private_ip_list\s*=`)
	reRouteAttr      = regexp.MustCompile(`(?m)^\s*route\s*=`)
	reImportToTarget = regexp.MustCompile(`to\s*=\s*(\S+)`)
)

// importTargets returns the sorted set of `to = <address>` targets from the
// import blocks in an imported.tf body — the parity anchor for "the same
// resources were emitted with the same addresses on both paths".
func importTargets(s string) []string {
	m := reImportToTarget.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	sort.Strings(out)
	return out
}

// eniListForm reports which of the mutually-exclusive ENI attributes survived:
// "private_ips", "private_ip_list", "both" (a bug — the pair must be
// reconciled), or "none".
func eniListForm(s string) string {
	hasPlural := rePrivateIPs.MatchString(s)
	hasList := rePrivateIPList.MatchString(s)
	switch {
	case hasPlural && hasList:
		return "both"
	case hasPlural:
		return "private_ips"
	case hasList:
		return "private_ip_list"
	default:
		return "none"
	}
}

// TestEmitPathParity_ImportedTFTreatment is the semantic half of the guard. It
// runs the identical imported-resource fixture through both emit paths and
// asserts they treat the emitted imported.tf the same way:
//
//   - exactly one of private_ips / private_ip_list survives (the same one) —
//     NormalizeImportedHCL's aws_network_interface fixup, exercised on both
//     paths (issue #708 / #840).
//   - no top-level `route =` attribute — the PR #842 emit guard drops the
//     inline aws_route_table.route on both paths.
//   - the same set of `import {}` block addresses.
//
// Byte-equality is deliberately NOT asserted: the compose path stamps
// provenance tags via nowFn() and may order provenance merges differently, so
// the comparison is on the treatment invariants only.
func TestEmitPathParity_ImportedTFTreatment(t *testing.T) {
	t.Parallel()

	const (
		project   = "paritytest"
		sessionID = "sess_parity"
	)

	// (a) Compose path — the full composeStackImpl pipeline.
	res, err := New().ComposeStackWithIssues(ComposeStackOpts{
		Cloud:           "aws",
		Comps:           &Components{Cloud: "aws"},
		Cfg:             &Config{Region: "us-west-2"},
		Project:         project,
		Region:          "us-west-2",
		Imported:        parityFixtures(),
		ImportProjectID: project,
		ImportSessionID: sessionID,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	composeTF, ok := res.Files["/imported.tf"]
	require.Truef(t, ok, "compose path must emit /imported.tf; files: %v", keysOf(res.Files))
	composeStr := string(composeTF)

	// (b) Reverse-path equivalent — EmitImportedTF then NormalizeImportedHCL,
	// exactly as pkg/reverseimport/run.go does. Fresh fixture slice because
	// EmitImportedTF mutates its input.
	emitted, _ := EmitImportedTF("aws", parityFixtures(), EmitImportedOpts{
		ImportProjectID: project,
		ImportSessionID: sessionID,
	})
	require.NotEmpty(t, emitted, "reverse path must emit imported.tf bytes")
	reverseTF, err := normalize.NormalizeImportedHCL(emitted)
	require.NoError(t, err)
	reverseStr := string(reverseTF)

	// Invariant 1: the ENI mutually-exclusive pair is reconciled to exactly
	// one side, identically on both paths.
	composeForm := eniListForm(composeStr)
	reverseForm := eniListForm(reverseStr)
	assert.Equalf(t, "private_ips", reverseForm,
		"reverse path must keep private_ips and drop private_ip_list:\n%s", reverseStr)
	assert.Equalf(t, composeForm, reverseForm,
		"compose vs reverse diverged on the ENI mutually-exclusive pair "+
			"(compose=%q reverse=%q) — a NormalizeImportedHCL drift between the "+
			"two emit paths (issue #840)\n--- compose ---\n%s\n--- reverse ---\n%s",
		composeForm, reverseForm, composeStr, reverseStr)

	// Invariant 2: the inline aws_route_table.route attribute is dropped on
	// both paths (PR #842 emit guard).
	assert.Falsef(t, reRouteAttr.MatchString(reverseStr),
		"reverse path must drop the inline aws_route_table.route attribute:\n%s", reverseStr)
	assert.Falsef(t, reRouteAttr.MatchString(composeStr),
		"compose path must drop the inline aws_route_table.route attribute:\n%s", composeStr)

	// Invariant 3: the same import-block addresses on both paths.
	composeTargets := importTargets(composeStr)
	reverseTargets := importTargets(reverseStr)
	assert.Equalf(t, []string{
		"aws_network_interface.eni",
		"aws_route_table.rt",
		"aws_s3_bucket.control",
	}, reverseTargets, "reverse path import targets\n%s", reverseStr)
	assert.Equalf(t, composeTargets, reverseTargets,
		"compose vs reverse emitted different import-block addresses "+
			"(compose=%v reverse=%v)", composeTargets, reverseTargets)
}
