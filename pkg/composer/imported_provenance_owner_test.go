package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestProvenancePipelineMatrix is the poka-yoke for reliable#2230: it pins,
// shape by shape, how a provenance claim travels the WHOLE compose pipeline
// (DropUnimportable → ValidateProvenanceConflicts → injectProvenance, all
// inside ComposeStackWithIssues) versus what the pick-time decision
// (ProvenanceOwnedByOther) reports for the same resource.
//
// The invariant: anything compose would reject as a provenance conflict OR
// silently drop as foreign-claimed is flagged by ProvenanceOwnedByOther at
// pick time. It is a SUPERSET relation, not a mirror — compose never
// *conflicts* on live-only claims; DropUnimportable (keying on the
// InsideOutImported co-marker via UnimportableReason) silently removes them
// before the validator runs.
//
// Motivating incident: reliable session sess_v2_tunCkPopdiKK, import
// irun_jFcsE0sgd8NH (staging, 2026-07-12) — a shared-account selection
// containing resources claimed by other import projects sailed through
// picker and plan-preview and died at apply-compose with fatal=5
// imported_resource_provenance_conflict.
func TestProvenancePipelineMatrix(t *testing.T) {
	t.Parallel()
	const (
		myProject = "io-mine"
		mySession = "sess_v2_tunCkPopdiKK"
		addr      = "aws_sqs_queue.q"
	)
	foreignStamp := map[string]string{
		"InsideOutImportProject": "io-foreign",
		"InsideOutImportSession": "sess_v2_other",
		"InsideOutImported":      "true",
		"InsideOutImportedAt":    "2026-05-26T21:10:48Z",
	}

	// compose runs one resource through the full stack compose and returns
	// the result plus the issues referencing the resource's address.
	compose := func(t *testing.T, ir imported.ImportedResource) (*ComposeStackResult, []ValidationIssue) {
		t.Helper()
		c := newTestClient()
		res, err := c.ComposeStackWithIssues(ComposeStackOpts{
			Cloud:           "aws",
			SelectedKeys:    []ComponentKey{KeyAWSVPC},
			Comps:           &Components{Cloud: "AWS"},
			Cfg:             &Config{},
			Project:         "demo",
			Region:          "us-east-1",
			ImportProjectID: myProject,
			ImportSessionID: mySession,
			Imported:        []imported.ImportedResource{ir},
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		var addrIssues []ValidationIssue
		for _, iss := range res.Issues {
			if strings.Contains(iss.Field, ir.Identity.Address) {
				addrIssues = append(addrIssues, iss)
			}
		}
		return res, addrIssues
	}

	mkIR := func(tfType, addr string) imported.ImportedResource {
		return imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: tfType,
				Address: addr, ImportID: "import-id",
			},
			Tier: imported.TierImportedFlat,
		}
	}
	baseIR := func() imported.ImportedResource { return mkIR("aws_sqs_queue", addr) }
	attrsWithMarkers := func(markers string) []byte {
		return []byte(`{"name":{"literal":"q"},"tags":{` + markers + `}}`)
	}

	t.Run("a: full foreign live stamp — compose drops silently, pick time flags", func(t *testing.T) {
		t.Parallel()
		// Type choice is LOAD-BEARING — do not "simplify" it: the drop must
		// be provably attributable to DropUnimportable running BEFORE the
		// validators (compose.go: DropUnimportable precedes
		// ValidateImportedResources / ValidateImportedEmitReadiness /
		// ValidateProvenanceConflicts). aws_api_gateway_stage has three
		// provider-Required attrs (rest_api_id, stage_name, deployment_id)
		// and this resource ships EMPTY Attrs, so if anyone reorders
		// compose to validate first, ValidateImportedEmitReadiness emits a
		// spurious imported_resource_missing_required_attr for this address
		// and the zero-issues assertion below goes red. A zero-Required
		// type (aws_sqs_queue) would keep every assertion green across such
		// a reorder and pin nothing.
		ir := mkIR("aws_api_gateway_stage", "aws_api_gateway_stage.s")
		// Discover-only shape: Attrs and Attributes empty.
		ir.Identity.Tags = foreignStamp

		res, addrIssues := compose(t, ir)
		// Dropped BEFORE any validator runs: no conflict issue, no
		// missing-required-attr issue — no trace at all beyond its absence.
		assert.Empty(t, addrIssues, "dropped-before-validation resource must produce no issues")
		assert.NotContains(t, string(res.Files["/imported.tf"]), ir.Identity.Address,
			"full-stamp foreign-claimed resource must not be emitted")

		owner, claimed := ProvenanceOwnedByOther(ir, mySession, myProject)
		assert.True(t, claimed, "pick time must flag what compose silently drops as foreign-claimed")
		assert.Equal(t, "io-foreign", owner)
	})

	t.Run("a2: full foreign live stamp on an UNTAGGABLE type — same verdicts", func(t *testing.T) {
		t.Parallel()
		// aws_iam_role_policy is untaggable (TaggableAttr=false: registered
		// schema without a tags key). DropUnimportable still drops the
		// full-stamp shape — UnimportableReason keys on the co-marker with
		// no taggability gate — so ProvenanceOwnedByOther's ungated live
		// leg must flag it too; gating the live read on TaggableAttr would
		// break the superset invariant exactly here. The type also carries
		// provider-Required attrs (policy, role), so like row (a) a compose
		// reorder would surface spurious missing-required-attr issues and
		// redden the zero-issues assertion.
		ir := mkIR("aws_iam_role_policy", "aws_iam_role_policy.p")
		ir.Identity.Tags = foreignStamp

		res, addrIssues := compose(t, ir)
		assert.Empty(t, addrIssues, "dropped-before-validation resource must produce no issues")
		assert.NotContains(t, string(res.Files["/imported.tf"]), ir.Identity.Address,
			"full-stamp foreign-claimed untaggable resource must not be emitted")
		assert.Equal(t, imported.ReasonInsideOutImported, imported.UnimportableReason(ir))

		owner, claimed := ProvenanceOwnedByOther(ir, mySession, myProject)
		assert.True(t, claimed, "untaggable types must still surface live full-stamp claims at pick time")
		assert.Equal(t, "io-foreign", owner)
	})

	t.Run("b: bare live project marker — not ownership anywhere", func(t *testing.T) {
		t.Parallel()
		// The historical bare account/project marker WITHOUT the
		// InsideOutImported co-marker (see HasInsideOutImportedMarker's
		// contract): compose keeps the resource, raises no conflict, and
		// the injector stamps our fresh markers over it; pick time must
		// not flag it either — conflicting would demand a ForceTakeover
		// from an owner that never owned.
		ir := baseIR()
		ir.Attrs = []byte(`{"name":{"literal":"q"}}`)
		ir.Identity.Tags = map[string]string{"InsideOutImportProject": "io-foreign"}

		res, addrIssues := compose(t, ir)
		assert.Empty(t, issuesWithCode(addrIssues, "imported_resource_provenance_conflict"),
			"bare live marker must not conflict")
		importedTF := string(res.Files["/imported.tf"])
		assert.Contains(t, importedTF, addr, "resource must be emitted")
		assert.True(t, hasAttr(t, importedTF, "InsideOutImportProject", `"`+myProject+`"`),
			"injector must stamp our project over a bare marker:\n%s", importedTF)
		assert.NotContains(t, importedTF, "io-foreign",
			"the bare foreign marker is filtered from <discovered> and replaced by our stamp")

		_, claimed := ProvenanceOwnedByOther(ir, mySession, myProject)
		assert.False(t, claimed, "bare marker is not ownership at pick time either")
	})

	t.Run("c: foreign desired-state claim — compose conflicts, pick time flags", func(t *testing.T) {
		t.Parallel()
		ir := baseIR()
		ir.Attrs = attrsWithMarkers(`"InsideOutImportProject":{"literal":"io-other"},"InsideOutImportSession":{"literal":"sess_v2_other"}`)

		res, addrIssues := compose(t, ir)
		conflicts := issuesWithCode(addrIssues, "imported_resource_provenance_conflict")
		require.Len(t, conflicts, 1, "typed-Attrs foreign claim must conflict at compose time")
		assert.Equal(t, "io-other", conflicts[0].Value)
		// Injector refusal: our project must not overwrite the claim.
		importedTF := string(res.Files["/imported.tf"])
		assert.NotContains(t, importedTF, myProject,
			"injector must not overwrite a conflicting claim without ForceTakeover")

		owner, claimed := ProvenanceOwnedByOther(ir, mySession, myProject)
		assert.True(t, claimed, "pick time must flag every compose-time conflict (superset invariant)")
		assert.Equal(t, "io-other", owner)
	})

	t.Run("d: self shapes — neither conflict nor claim", func(t *testing.T) {
		t.Parallel()
		selfShapes := map[string]imported.ImportedResource{
			"own project in Attrs": func() imported.ImportedResource {
				ir := baseIR()
				ir.Attrs = attrsWithMarkers(`"InsideOutImportProject":{"literal":"` + myProject + `"}`)
				return ir
			}(),
			// The reconcile leg stamps the Oracle deployment UUID as the
			// project string; the session tag is the namespace-stable self
			// identity (reliable#2068).
			"same session, different project string in Attrs": func() imported.ImportedResource {
				ir := baseIR()
				ir.Attrs = attrsWithMarkers(`"InsideOutImportProject":{"literal":"deploy-uuid-reconcile"},"InsideOutImportSession":{"literal":"` + mySession + `"}`)
				return ir
			}(),
		}
		for name, ir := range selfShapes {
			ir := ir
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				_, addrIssues := compose(t, ir)
				assert.Empty(t, issuesWithCode(addrIssues, "imported_resource_provenance_conflict"),
					"self claim must not conflict at compose time")
				_, claimed := ProvenanceOwnedByOther(ir, mySession, myProject)
				assert.False(t, claimed, "self claim must not be flagged at pick time")
			})
		}

		t.Run("own full live stamp", func(t *testing.T) {
			t.Parallel()
			// Discover-only shape carrying OUR full stamp. Pick time
			// correctly reports "not claimed by other"; compose still drops
			// it — through the already-imported channel
			// (ReasonInsideOutImported keys on the co-marker regardless of
			// owner), NOT as a provenance conflict. Pinning that keeps the
			// superset invariant honest: the drop is not a foreign-claim
			// signal, and the picker learns "already managed by InsideOut"
			// from UnimportableReason, not from ProvenanceOwnedByOther.
			ir := baseIR()
			ir.Identity.Tags = map[string]string{
				"InsideOutImportProject": myProject,
				"InsideOutImportSession": mySession,
				"InsideOutImported":      "true",
			}
			res, addrIssues := compose(t, ir)
			assert.Empty(t, addrIssues, "own live stamp drops through the already-imported channel, no issues")
			assert.NotContains(t, string(res.Files["/imported.tf"]), addr)
			assert.Equal(t, imported.ReasonInsideOutImported, imported.UnimportableReason(ir))

			_, claimed := ProvenanceOwnedByOther(ir, mySession, myProject)
			assert.False(t, claimed, "our own stamp is not a foreign claim")
		})
	})
}

// issuesWithCode filters issues to those with the given code.
func issuesWithCode(issues []ValidationIssue, code string) []ValidationIssue {
	var out []ValidationIssue
	for _, iss := range issues {
		if iss.Code == code {
			out = append(out, iss)
		}
	}
	return out
}

// TestProvenanceOwnerReExports pins that the pkg/composer surface delegates
// to the canonical pkg/composer/imported implementations — downstream
// consumers import composer, and a drifted re-export would resurrect the
// two-reader divergence this API exists to end.
func TestProvenanceOwnerReExports(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "q",
			Tags: map[string]string{
				"InsideOutImportProject": "io-other",
				"InsideOutImported":      "true",
			},
		},
		Tier: imported.TierImportedFlat,
	}
	p1, s1, ok1 := ProvenanceOwner(ir)
	p2, s2, ok2 := imported.ProvenanceOwner(ir)
	assert.Equal(t, p2, p1)
	assert.Equal(t, s2, s1)
	assert.Equal(t, ok2, ok1)

	o1, c1 := ProvenanceOwnedByOther(ir, "sess", "io-mine", "deploy-uuid")
	o2, c2 := imported.ProvenanceOwnedByOther(ir, "sess", "io-mine", "deploy-uuid")
	assert.Equal(t, o2, o1)
	assert.Equal(t, c2, c1)
}
