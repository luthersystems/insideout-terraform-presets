package imported

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unit tests for the pick-time provenance ownership readers (reliable#2230).
// The layer/precedence contract under test is documented once, on
// ProvenanceOwner / the package comment in provenance_owner.go. Motivating
// incident: reliable session sess_v2_tunCkPopdiKK, import irun_jFcsE0sgd8NH
// (staging, 2026-07-12) — a shared-account import selected resources already
// claimed by other import projects and the conflict surfaced only at
// apply-compose; these readers give the picker/reverse-run guards the same
// ownership signal at selection time.

// provCloudFixture parameterizes the per-cloud marker vocabulary once so
// every test below iterates the same table instead of switching on cloud
// inline. GCP label VALUES must be label-legal (lowercase) in fixtures —
// uppercase values cannot exist on a live GCP label, so planting one would
// vacuously pass comparisons production can never see.
type provCloudFixture struct {
	cloud       string
	tfType      string
	projectKey  string
	sessionKey  string
	importedKey string
	// session is a legal live session value for this cloud: AWS tags
	// preserve case; GCP labels are lowercase-only.
	session string
}

var provClouds = []provCloudFixture{
	{
		cloud:       "aws",
		tfType:      "aws_sqs_queue",
		projectKey:  AWSTagKeyImportProject,
		sessionKey:  AWSTagKeyImportSession,
		importedKey: AWSTagKeyImported,
		session:     "sess_v2_tunCkPopdiKK",
	},
	{
		cloud:       "gcp",
		tfType:      "google_storage_bucket",
		projectKey:  GCPLabelKeyImportProject,
		sessionKey:  GCPLabelKeyImportSession,
		importedKey: GCPLabelKeyImported,
		session:     "sess_v2_tunckpopdikk",
	},
}

// ir returns the shared emit-eligible base resource for the fixture's cloud.
func (fx provCloudFixture) ir() ImportedResource {
	return ImportedResource{
		Identity: ResourceIdentity{
			Cloud:    fx.cloud,
			Type:     fx.tfType,
			Address:  fx.tfType + ".r",
			ImportID: "id",
		},
		Tier: TierImportedFlat,
	}
}

// attrName derives the tags/labels attribute via TaggableAttr — the same
// gate production applies — rather than a parallel cloud→name mapping.
func (fx provCloudFixture) attrName(t *testing.T) string {
	t.Helper()
	attr, ok := TaggableAttr(fx.ir())
	require.Truef(t, ok, "fixture type %s must be taggable", fx.tfType)
	return attr
}

// attrsJSON builds a typed-Attrs JSON literal carrying markers under the
// fixture's tags/labels attribute (plus a name so the body stays realistic).
func (fx provCloudFixture) attrsJSON(t *testing.T, markers map[string]string) []byte {
	t.Helper()
	inner := map[string]any{}
	for k, v := range markers {
		inner[k] = map[string]any{"literal": v}
	}
	raw, err := json.Marshal(map[string]any{
		"name":         map[string]any{"literal": "n"},
		fx.attrName(t): inner,
	})
	require.NoError(t, err)
	return raw
}

// attributesBag builds an opaque Attributes bag carrying markers under the
// fixture's tags/labels attribute.
func (fx provCloudFixture) attributesBag(t *testing.T, markers map[string]string) map[string]any {
	t.Helper()
	inner := map[string]any{}
	for k, v := range markers {
		inner[k] = v
	}
	return map[string]any{"name": "n", fx.attrName(t): inner}
}

// fullLiveStamp is a well-formed live claim: project + session + the
// InsideOutImported co-marker, as the stamper's apply leaves them on the
// cloud resource.
func (fx provCloudFixture) fullLiveStamp(project, session string) map[string]string {
	m := map[string]string{
		fx.projectKey:  project,
		fx.importedKey: "true",
	}
	if session != "" {
		m[fx.sessionKey] = session
	}
	return m
}

func TestProvenanceOwner_ClaimPlacements(t *testing.T) {
	t.Parallel()
	const wantProject = "io-owner-42"
	for _, fx := range provClouds {
		fx := fx
		markers := map[string]string{fx.projectKey: wantProject, fx.sessionKey: fx.session}

		t.Run(fx.cloud+"/attrs", func(t *testing.T) {
			t.Parallel()
			ir := fx.ir()
			ir.Attrs = fx.attrsJSON(t, markers)
			project, session, ok := ProvenanceOwner(ir)
			require.True(t, ok)
			assert.Equal(t, wantProject, project)
			assert.Equal(t, fx.session, session)
		})
		t.Run(fx.cloud+"/attributes", func(t *testing.T) {
			t.Parallel()
			ir := fx.ir()
			ir.Attributes = fx.attributesBag(t, markers)
			project, session, ok := ProvenanceOwner(ir)
			require.True(t, ok)
			assert.Equal(t, wantProject, project)
			assert.Equal(t, fx.session, session)
		})
		t.Run(fx.cloud+"/live-full-stamp", func(t *testing.T) {
			t.Parallel()
			ir := fx.ir() // discover-only shape: Attrs and Attributes empty
			ir.Identity.Tags = fx.fullLiveStamp(wantProject, fx.session)
			project, session, ok := ProvenanceOwner(ir)
			require.True(t, ok, "full-stamp discover-only claim must be readable (reliable#2230)")
			assert.Equal(t, wantProject, project)
			assert.Equal(t, fx.session, session)
		})
	}
}

func TestProvenanceOwner_LayerRules(t *testing.T) {
	t.Parallel()
	for _, fx := range provClouds {
		fx := fx
		t.Run(fx.cloud, func(t *testing.T) {
			t.Parallel()

			t.Run("attrs beats attributes", func(t *testing.T) {
				t.Parallel()
				ir := fx.ir()
				ir.Attrs = fx.attrsJSON(t, map[string]string{fx.projectKey: "io-from-attrs", fx.sessionKey: "sess-attrs"})
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.projectKey: "io-from-attributes", fx.sessionKey: "sess-attributes"})
				project, session, ok := ProvenanceOwner(ir)
				require.True(t, ok)
				assert.Equal(t, "io-from-attrs", project, "typed Attrs is plan-authoritative")
				assert.Equal(t, "sess-attrs", session)
			})

			t.Run("attributes read when attrs has no marker", func(t *testing.T) {
				t.Parallel()
				ir := fx.ir()
				ir.Attrs = fx.attrsJSON(t, nil) // tags/labels map present, no project marker
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.projectKey: "io-from-attributes"})
				project, _, ok := ProvenanceOwner(ir)
				require.True(t, ok)
				assert.Equal(t, "io-from-attributes", project)
			})

			t.Run("desired state present gates live leg off", func(t *testing.T) {
				t.Parallel()
				// The resource has been enriched/backfilled (Attrs non-empty)
				// and its desired state carries NO claim — including the case
				// where a prior claim was deliberately removed (claim
				// release). The stale live stamp must NOT shadow that.
				ir := fx.ir()
				ir.Attrs = fx.attrsJSON(t, nil)
				ir.Identity.Tags = fx.fullLiveStamp("io-foreign", fx.session)
				_, _, ok := ProvenanceOwner(ir)
				assert.False(t, ok, "live leg must be consulted only for the literal discover-only shape")
			})

			t.Run("pair is layer-coherent, never stitched", func(t *testing.T) {
				t.Parallel()
				// Attrs carries the project only; Attributes carries a
				// session. The session must NOT be stitched in from the
				// other layer — a stitched pair let a stale same-session
				// marker suppress a real foreign-project conflict (F3).
				ir := fx.ir()
				ir.Attrs = fx.attrsJSON(t, map[string]string{fx.projectKey: "io-from-attrs"})
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.sessionKey: "sess-stitched"})
				project, session, ok := ProvenanceOwner(ir)
				require.True(t, ok)
				assert.Equal(t, "io-from-attrs", project)
				assert.Empty(t, session, "session must come from the same layer as the project marker")
			})
		})
	}
}

func TestProvenanceOwner_LiveFullStampSemantics(t *testing.T) {
	t.Parallel()
	for _, fx := range provClouds {
		fx := fx
		t.Run(fx.cloud, func(t *testing.T) {
			t.Parallel()

			t.Run("bare project marker is not ownership", func(t *testing.T) {
				t.Parallel()
				// No InsideOutImported co-marker: per the
				// HasInsideOutImportedMarker contract in importability.go,
				// historical resources carry a bare account/project marker
				// without having been imported. Treating it as a claim
				// would demand a force-takeover from an owner that never
				// owned.
				ir := fx.ir()
				ir.Identity.Tags = map[string]string{fx.projectKey: "io-foreign"}
				_, _, ok := ProvenanceOwner(ir)
				assert.False(t, ok, "bare live project marker must not read as a claim")
			})

			t.Run("empty project value is not ownership", func(t *testing.T) {
				t.Parallel()
				// An empty owner is unactionable (ForceTakeover requires a
				// non-empty PreviousOwner) — require substance (F4).
				for _, empty := range []string{"", "   "} {
					ir := fx.ir()
					ir.Identity.Tags = fx.fullLiveStamp(empty, fx.session)
					_, _, ok := ProvenanceOwner(ir)
					assert.False(t, ok, "empty/whitespace live project value must not read as a claim")
				}
			})

			t.Run("normalized key matching and trimmed values", func(t *testing.T) {
				t.Parallel()
				// Key matching shares HasInsideOutImportedMarker's
				// TrimSpace + case-insensitive normalization; values come
				// back trimmed.
				ir := fx.ir()
				ir.Identity.Tags = map[string]string{
					" " + fx.projectKey + " ": "  io-owner-42  ",
					fx.importedKey:            "true",
				}
				project, _, ok := ProvenanceOwner(ir)
				require.True(t, ok)
				assert.Equal(t, "io-owner-42", project)
			})
		})
	}
}

func TestProvenanceOwner_GatesAndNoClaim(t *testing.T) {
	t.Parallel()

	t.Run("no markers anywhere", func(t *testing.T) {
		t.Parallel()
		fx := provClouds[0]
		ir := fx.ir()
		ir.Attrs = fx.attrsJSON(t, nil)
		project, session, ok := ProvenanceOwner(ir)
		assert.False(t, ok)
		assert.Empty(t, project)
		assert.Empty(t, session)
	})

	t.Run("untaggable type: live full stamp still reads (ungated live leg)", func(t *testing.T) {
		t.Parallel()
		// google_compute_network has no labels field (weak-lock), but a
		// LIVE full stamp on it must still read as a claim: untaggability
		// is about what OUR emitter may write, live labels exist on the
		// instance regardless, and DropUnimportable drops this shape on
		// the co-marker with no taggability gate — pick time must not
		// report it unclaimed while compose silently drops it (superset
		// invariant).
		ir := ImportedResource{
			Identity: ResourceIdentity{
				Cloud: "gcp", Type: "google_compute_network",
				Address: "google_compute_network.vpc", ImportID: "vpc",
				Tags: map[string]string{
					GCPLabelKeyImportProject: "io-foreign",
					GCPLabelKeyImported:      "true",
				},
			},
			Tier: TierImportedFlat,
		}
		project, _, ok := ProvenanceOwner(ir)
		assert.True(t, ok, "live full stamp on an untaggable type must read as a claim")
		assert.Equal(t, "io-foreign", project)
		assert.Equal(t, ReasonInsideOutImported, UnimportableReason(ir),
			"same shape compose drops through the already-imported channel")
	})

	t.Run("untaggable type: desired-state legs stay gated", func(t *testing.T) {
		t.Parallel()
		// The compose-time validator skips untaggable types, so the
		// desired-state legs mirror it — there is no emitter-writable
		// attribute NAME to read a tags/labels claim under.
		ir := ImportedResource{
			Identity: ResourceIdentity{
				Cloud: "gcp", Type: "google_compute_network",
				Address: "google_compute_network.vpc", ImportID: "vpc",
			},
			Tier: TierImportedFlat,
			Attributes: map[string]any{
				"name":   "vpc",
				"labels": map[string]any{GCPLabelKeyImportProject: "io-foreign"},
			},
		}
		_, _, ok := ProvenanceOwner(ir)
		assert.False(t, ok, "untaggable desired-state claim must not read (validator parity)")
	})

	t.Run("service-managed instance: live full stamp still reads", func(t *testing.T) {
		t.Parallel()
		// Same rationale as the untaggable case: TaggableAttr weak-locks
		// service-managed instances for EMISSION (#785), but a live claim
		// on one is still a claim — UnimportableReason drops the shape
		// (ReasonInsideOutImported wins over ReasonServiceManaged at
		// classification) and pick time must agree it is foreign-claimed.
		fx := provClouds[0]
		ir := fx.ir()
		ir.Identity.ServiceManagedBy = "autoscaling.amazonaws.com"
		ir.Identity.Tags = fx.fullLiveStamp("io-foreign", fx.session)
		project, _, ok := ProvenanceOwner(ir)
		assert.True(t, ok, "live full stamp on a service-managed instance must read as a claim")
		assert.Equal(t, "io-foreign", project)
	})
}

func TestProvenanceOwnedByOther(t *testing.T) {
	t.Parallel()
	const (
		myProject = "io-mine"
		mySession = "sess_v2_tunCkPopdiKK"
	)
	fx := provClouds[0] // AWS; GCP-specific comparison rules are pinned below.

	// deployUUID stands in for the Oracle deployment-project UUID — the
	// second member of a session's own-claim set (reliable#2068's double
	// namespace: the import_apply leg stamps the session-derived
	// "io-<suffix>" name, the mars reconcile leg stamps the UUID).
	const deployUUID = "1c3c8501-45fd-5f46-9a60-5a5d753a3e2b"

	type tc struct {
		name        string
		ir          func(t *testing.T) ImportedResource
		sessionID   string
		ownProjects []string
		wantOwner   string
		wantClaimed bool
	}
	cases := []tc{
		{
			name: "foreign desired-state claim → claimed",
			ir: func(t *testing.T) ImportedResource {
				ir := fx.ir()
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.projectKey: "io-other", fx.sessionKey: "sess_v2_other"})
				return ir
			},
			sessionID: mySession, ownProjects: []string{myProject, deployUUID},
			wantOwner: "io-other", wantClaimed: true,
		},
		{
			name: "foreign live full stamp → claimed",
			ir: func(t *testing.T) ImportedResource {
				ir := fx.ir()
				ir.Identity.Tags = fx.fullLiveStamp("io-other", "sess_v2_other")
				return ir
			},
			sessionID: mySession, ownProjects: []string{myProject, deployUUID},
			wantOwner: "io-other", wantClaimed: true,
		},
		{
			name: "own project → self",
			ir: func(t *testing.T) ImportedResource {
				ir := fx.ir()
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.projectKey: myProject})
				return ir
			},
			sessionID: mySession, ownProjects: []string{myProject, deployUUID},
		},
		{
			name: "own deployment UUID, NO session marker → self via the own-project SET",
			ir: func(t *testing.T) ImportedResource {
				// The reconcile-leg shape that motivated the set-valued
				// API: the stamp carries the deployment UUID and NO
				// session marker, so the session escape cannot recognize
				// it — only membership in ownProjectIDs can. Passing a
				// single "io-<suffix>" id here would mislabel the
				// session's own resource as "claimed by another import
				// project (<own deployment UUID>)".
				ir := fx.ir()
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.projectKey: deployUUID})
				return ir
			},
			sessionID: mySession, ownProjects: []string{myProject, deployUUID},
		},
		{
			name: "own deployment UUID but caller passed only the io-name → claimed (caller must pass the full set)",
			ir: func(t *testing.T) ImportedResource {
				ir := fx.ir()
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.projectKey: deployUUID})
				return ir
			},
			sessionID: mySession, ownProjects: []string{myProject},
			wantOwner: deployUUID, wantClaimed: true,
		},
		{
			name: "foreign project, same session → self (reliable#2068)",
			ir: func(t *testing.T) ImportedResource {
				// A project string outside the caller's set entirely, but
				// the session tag — the namespace-stable identity — matches.
				ir := fx.ir()
				ir.Attributes = fx.attributesBag(t, map[string]string{
					fx.projectKey: "some-legacy-namespace-id",
					fx.sessionKey: mySession,
				})
				return ir
			},
			sessionID: mySession, ownProjects: []string{myProject, deployUUID},
		},
		{
			name: "empty ownProjectIDs → backwards-compat, nothing claimed",
			ir: func(t *testing.T) ImportedResource {
				// Provenance is disabled everywhere in this mode (the
				// validator emits only the skipped-no-project-id advisory
				// and the injector stamps nothing), so pick time must not
				// enforce either (F7).
				ir := fx.ir()
				ir.Identity.Tags = fx.fullLiveStamp("io-other", "sess_v2_other")
				return ir
			},
			sessionID: mySession, ownProjects: nil,
		},
		{
			name: "all-blank ownProjectIDs → backwards-compat too",
			ir: func(t *testing.T) ImportedResource {
				ir := fx.ir()
				ir.Identity.Tags = fx.fullLiveStamp("io-other", "sess_v2_other")
				return ir
			},
			sessionID: mySession, ownProjects: []string{"", "   "},
		},
		{
			name: "empty importSessionID disables the session escape",
			ir: func(t *testing.T) ImportedResource {
				ir := fx.ir()
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.projectKey: "io-other", fx.sessionKey: mySession})
				return ir
			},
			sessionID: "", ownProjects: []string{myProject, deployUUID},
			wantOwner: "io-other", wantClaimed: true,
		},
		{
			name: "resource has no session marker and foreign project → claimed",
			ir: func(t *testing.T) ImportedResource {
				ir := fx.ir()
				ir.Attributes = fx.attributesBag(t, map[string]string{fx.projectKey: "io-other"})
				return ir
			},
			sessionID: mySession, ownProjects: []string{myProject, deployUUID},
			wantOwner: "io-other", wantClaimed: true,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			owner, claimed := ProvenanceOwnedByOther(c.ir(t), c.sessionID, c.ownProjects...)
			assert.Equal(t, c.wantClaimed, claimed)
			assert.Equal(t, c.wantOwner, owner)
		})
	}
}

// TestProvenanceOwnedByOther_LiveValueComparison pins the per-cloud live-leg
// comparison rule derived from how the stamper writes values
// (provenanceKeysFor emits project/session VERBATIM; only the timestamp is
// GCP-charset-normalized):
//
//   - GCP label values are lowercase-only, so the canonical mixed-case
//     session id can only ever exist on a live label lowercased — the
//     comparison folds case, otherwise the reliable#2068 self-claim escape
//     would be silently unreachable on GCP live claims.
//   - AWS tag values preserve case and compare byte-exactly: a live value
//     that differs by case really is a different identifier.
func TestProvenanceOwnedByOther_LiveValueComparison(t *testing.T) {
	t.Parallel()
	const mixedSession = "sess_v2_tunCkPopdiKK"

	t.Run("gcp live folds case", func(t *testing.T) {
		t.Parallel()
		fx := provClouds[1]
		ir := fx.ir()
		ir.Identity.Tags = fx.fullLiveStamp("deploy-uuid-reconcile", "sess_v2_tunckpopdikk")
		owner, claimed := ProvenanceOwnedByOther(ir, mixedSession, "io-mine")
		assert.False(t, claimed, "lowercased live GCP session must match the mixed-case canonical id")
		assert.Empty(t, owner)

		// Project arm folds too: case-variant GCP projects cannot be
		// distinct on a live label.
		ir2 := fx.ir()
		ir2.Identity.Tags = fx.fullLiveStamp("io-mine", "")
		_, claimed = ProvenanceOwnedByOther(ir2, "", "IO-Mine")
		assert.False(t, claimed)
	})

	t.Run("aws live is byte-exact", func(t *testing.T) {
		t.Parallel()
		fx := provClouds[0]
		ir := fx.ir()
		ir.Identity.Tags = fx.fullLiveStamp("io-other", "sess_v2_tunckpopdikk")
		owner, claimed := ProvenanceOwnedByOther(ir, mixedSession, "io-mine")
		assert.True(t, claimed, "AWS live tags preserve case; a case-variant session is a different session")
		assert.Equal(t, "io-other", owner)
	})

	t.Run("comparisons trim caller identifiers", func(t *testing.T) {
		t.Parallel()
		fx := provClouds[0]
		ir := fx.ir()
		ir.Identity.Tags = fx.fullLiveStamp("io-mine", "")
		_, claimed := ProvenanceOwnedByOther(ir, "", "  io-mine  ")
		assert.False(t, claimed)
	})
}
