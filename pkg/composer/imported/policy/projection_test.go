package policy

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Side-effect import: register the Layer 1 generated types so the
	// projection helpers can read FieldSchema.Sensitive defaults.
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// storageBucketAttrs is a JSON-shaped attrs payload mirroring what
// json.Unmarshal of a real terraform state's google_storage_bucket
// instance produces. Singleton and repeated blocks alike are []any
// containing maps, matching tfjson's HCL→JSON convention.
func storageBucketAttrs() map[string]any {
	return map[string]any{
		"name":                        "my-bucket",
		"project":                     "my-project",
		"location":                    "US",
		"storage_class":               "STANDARD",
		"force_destroy":               false,
		"public_access_prevention":    "enforced",
		"uniform_bucket_level_access": true,
		"encryption": []any{
			map[string]any{"default_kms_key_name": "projects/p/locations/us/keyRings/r/cryptoKeys/k"},
		},
		"lifecycle_rule": []any{
			map[string]any{
				"action":    []any{map[string]any{"type": "Delete"}},
				"condition": []any{map[string]any{"age": float64(30)}},
			},
			map[string]any{
				"action":    []any{map[string]any{"type": "SetStorageClass", "storage_class": "NEARLINE"}},
				"condition": []any{map[string]any{"age": float64(60)}},
			},
		},
		"labels": map[string]any{"project": "my-project"},
	}
}

func pathSet(views []FieldView) map[string]FieldView {
	out := make(map[string]FieldView, len(views))
	for _, v := range views {
		out[v.Path] = v
	}
	return out
}

func TestVisibleFieldsFor_StorageBucket_AcceptanceA(t *testing.T) {
	views := VisibleFieldsFor("google_storage_bucket", storageBucketAttrs())
	require.NotEmpty(t, views)

	// Stable Path order.
	paths := make([]string, len(views))
	for i, v := range views {
		paths[i] = v.Path
	}
	sortedCopy := append([]string(nil), paths...)
	sort.Strings(sortedCopy)
	assert.Equal(t, sortedCopy, paths, "VisibleFieldsFor must return rows in alphabetical Path order")

	set := pathSet(views)

	// Curated UI/agent-visible entries are present.
	for _, p := range []string{
		"name", "project", "location", "self_link", "url", "id",
		"storage_class", "force_destroy", "encryption.default_kms_key_name",
		"lifecycle_rule.condition.age", "lifecycle_rule.action.type",
	} {
		assert.Contains(t, set, p, "expected visible path %q", p)
	}

	// Hidden curated entries are excluded.
	for _, p := range []string{"labels", "effective_labels", "terraform_labels", "timeouts"} {
		assert.NotContains(t, set, p, "hidden path %q must not appear in VisibleFieldsFor", p)
	}

	// CurrentValue projection — top-level scalar.
	assert.Equal(t, "STANDARD", set["storage_class"].CurrentValue)
	assert.Equal(t, false, set["force_destroy"].CurrentValue)

	// CurrentValue projection — singleton-block descent flattens through []any.
	assert.Equal(t,
		[]any{"projects/p/locations/us/keyRings/r/cryptoKeys/k"},
		set["encryption.default_kms_key_name"].CurrentValue,
	)

	// CurrentValue projection — multi-element repeated-block fanout.
	assert.Equal(t, []any{float64(30), float64(60)}, set["lifecycle_rule.condition.age"].CurrentValue)
	assert.Equal(t, []any{"Delete", "SetStorageClass"}, set["lifecycle_rule.action.type"].CurrentValue)

	// Annotations preserved.
	storageClass := set["storage_class"]
	assert.Equal(t, RoleTuning, storageClass.Role)
	assert.Equal(t, PillarPerformance, storageClass.Pillar)
	assert.Equal(t, VisibilityRileyVisible, storageClass.Visibility)
	assert.Equal(t, EditChatSafe, storageClass.Edit)
}

func TestEditableFieldsFor_StorageBucket_AcceptanceB(t *testing.T) {
	attrs := storageBucketAttrs()
	visible := pathSet(VisibleFieldsFor("google_storage_bucket", attrs))
	editable := pathSet(EditableFieldsFor("google_storage_bucket", attrs))

	require.NotEmpty(t, editable)

	// Editable ⊆ Visible (cross-helper invariant).
	for p := range editable {
		assert.Contains(t, visible, p, "editable path %q must also be visible", p)
	}

	// EditPolicy values are exactly the three writable ones.
	for _, fv := range editable {
		switch fv.Edit {
		case EditChatSafe, EditRequiresApproval, EditRelationshipOnly:
			// ok
		default:
			t.Errorf("editable row %q has non-editable EditPolicy %q", fv.Path, fv.Edit)
		}
	}

	// EditNever entries excluded.
	for _, p := range []string{"name", "self_link", "url", "id", "project", "location"} {
		assert.NotContains(t, editable, p, "EditNever path %q must not appear in EditableFieldsFor", p)
	}

	// Representative editable entries present.
	for _, p := range []string{
		"storage_class",                   // ChatSafe
		"force_destroy",                   // RequiresApproval
		"encryption.default_kms_key_name", // RelationshipOnly
	} {
		assert.Contains(t, editable, p, "expected editable path %q", p)
	}
}

func TestVisibleFieldsFor_SensitivePasswordExcluded_AcceptanceC(t *testing.T) {
	attrs := map[string]any{
		"name":     "alice",
		"instance": "db-1",
		"password": "super-secret",
	}

	visible := pathSet(VisibleFieldsFor("google_sql_user", attrs))
	assert.NotContains(t, visible, "password",
		"schema-Sensitive password must be excluded from VisibleFieldsFor by default")

	// It surfaces in SystemOwnedFieldsFor with the sensitivity annotation
	// and redacted value.
	system := pathSet(SystemOwnedFieldsFor("google_sql_user", attrs))
	require.Contains(t, system, "password")
	pw := system["password"]
	assert.Equal(t, SensitivitySensitive, pw.Sensitivity,
		"schema-Sensitive field's projection row must carry Sensitivity=Sensitive")
	assert.Nil(t, pw.CurrentValue,
		"sensitive field's CurrentValue must be nil (redaction at the boundary)")
}

func TestResolveAttrPath_NestedBlocks_AcceptanceD(t *testing.T) {
	// Singleton block: encryption → []any of 1, descend to default_kms_key_name.
	attrs := map[string]any{
		"encryption": []any{
			map[string]any{"default_kms_key_name": "k1"},
		},
	}
	assert.Equal(t,
		[]any{"k1"},
		resolveAttrPath(attrs, "encryption.default_kms_key_name"),
	)

	// Repeated block multi-element fanout: lifecycle_rule.action.type.
	attrs2 := map[string]any{
		"lifecycle_rule": []any{
			map[string]any{"action": []any{map[string]any{"type": "Delete"}}},
			map[string]any{"action": []any{map[string]any{"type": "SetStorageClass"}}},
		},
	}
	assert.Equal(t,
		[]any{"Delete", "SetStorageClass"},
		resolveAttrPath(attrs2, "lifecycle_rule.action.type"),
	)

	// Missing top-level key → nil.
	assert.Nil(t, resolveAttrPath(map[string]any{}, "encryption.default_kms_key_name"))

	// Missing nested key → nil for that element; absent overall.
	attrs3 := map[string]any{
		"lifecycle_rule": []any{
			map[string]any{"action": []any{map[string]any{}}}, // no "type" inside
		},
	}
	assert.Nil(t, resolveAttrPath(attrs3, "lifecycle_rule.action.type"))

	// Top-level scalar.
	attrs4 := map[string]any{"storage_class": "STANDARD"}
	assert.Equal(t, "STANDARD", resolveAttrPath(attrs4, "storage_class"))
}

func TestVisibleFieldsFor_UnregisteredType_AcceptanceE(t *testing.T) {
	views := VisibleFieldsFor("not_a_registered_type", map[string]any{"k": "v"})
	assert.Empty(t, views)
	editable := EditableFieldsFor("not_a_registered_type", map[string]any{"k": "v"})
	assert.Empty(t, editable)
	system := SystemOwnedFieldsFor("not_a_registered_type", map[string]any{"k": "v"})
	assert.Empty(t, system)
}

func TestProjection_PartitionInvariant_VisibleUnionSystemOwnedCoversAll(t *testing.T) {
	// For every (tfType, attrs), each curated path appears in at least
	// one of VisibleFieldsFor / SystemOwnedFieldsFor. Run against the
	// 5 currently-enriched GCP types so the invariant is exercised on
	// real curated maps.
	attrs := map[string]any{}
	for _, tfType := range []string{
		"google_storage_bucket",
		"google_pubsub_topic",
		"google_pubsub_subscription",
		"google_secret_manager_secret",
		"google_compute_network",
	} {
		t.Run(tfType, func(t *testing.T) {
			polMap, ok := Lookup(tfType)
			require.True(t, ok, "policy must be registered for %q", tfType)

			visible := pathSet(VisibleFieldsFor(tfType, attrs))
			system := pathSet(SystemOwnedFieldsFor(tfType, attrs))

			for path := range polMap {
				_, inVisible := visible[path]
				_, inSystem := system[path]
				assert.True(t, inVisible || inSystem,
					"curated path %q must appear in Visible ∪ SystemOwned", path)
			}
		})
	}
}

func TestProjection_EditableSubsetOfVisible_OnAllEnrichedTypes(t *testing.T) {
	attrs := map[string]any{}
	for _, tfType := range []string{
		"google_storage_bucket",
		"google_pubsub_topic",
		"google_pubsub_subscription",
		"google_secret_manager_secret",
		"google_compute_network",
	} {
		t.Run(tfType, func(t *testing.T) {
			visible := pathSet(VisibleFieldsFor(tfType, attrs))
			editable := pathSet(EditableFieldsFor(tfType, attrs))
			for path := range editable {
				assert.Contains(t, visible, path,
					"editable path %q must be a subset of visible for %q", path, tfType)
			}
		})
	}
}
