package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func ir(typ, address string, nativeIDs map[string]string) imported.ImportedResource {
	return imported.ImportedResource{Identity: imported.ResourceIdentity{
		Type:      typ,
		Address:   address,
		NameHint:  address,
		NativeIDs: nativeIDs,
	}}
}

func TestPartitionUnimportable(t *testing.T) {
	in := []imported.ImportedResource{
		ir("aws_vpc", "vpc-1", map[string]string{"id": "vpc-1"}),
		ir("aws_kms_alias", "alias/aws/rds", map[string]string{"name": "alias/aws/rds"}),
		ir("aws_kms_alias", "alias/my-app", map[string]string{"name": "alias/my-app"}),
		ir("aws_network_interface", "eni-managed", map[string]string{"id": "eni-managed", "interface_type": "nat_gateway"}),
		ir("aws_network_interface", "eni-plain", map[string]string{"id": "eni-plain", "interface_type": "interface"}),
	}

	keep, dropped := partitionUnimportable(in)

	// Assert input-order preservation on the slice directly (the doc contract),
	// not just set membership — a regression that sorts/reverses either slice
	// must fail here.
	if len(keep) != 3 {
		t.Fatalf("keep: got %d, want 3 (vpc, customer alias, standard eni)", len(keep))
	}
	wantKeep := []string{"vpc-1", "alias/my-app", "eni-plain"}
	for i, want := range wantKeep {
		if keep[i].Identity.Address != want {
			t.Errorf("keep[%d] = %q, want %q (input order must be preserved)", i, keep[i].Identity.Address, want)
		}
	}

	if len(dropped) != 2 {
		t.Fatalf("dropped: got %d, want 2 (managed alias + managed eni)", len(dropped))
	}
	// dropped preserves input order: managed alias precedes managed eni.
	if dropped[0].ID != "alias/aws/rds" || dropped[0].Reason != imported.ReasonAWSManagedKMSAlias {
		t.Errorf("dropped[0] = {ID:%q Reason:%q}, want {alias/aws/rds, %q}", dropped[0].ID, dropped[0].Reason, imported.ReasonAWSManagedKMSAlias)
	}
	if dropped[1].ID != "eni-managed" || dropped[1].Reason != imported.ReasonServiceManagedENI {
		t.Errorf("dropped[1] = {ID:%q Reason:%q}, want {eni-managed, %q}", dropped[1].ID, dropped[1].Reason, imported.ReasonServiceManagedENI)
	}
}

// TestUnsupportedResource_ReasonWireFormat pins the cross-repo JSON contract on
// the `reason` field (#709 → reliable#1967): the key is literally "reason", the
// value round-trips, and an empty Reason is omitted (omitempty) so type-level
// unsupported rows don't emit reason:"". This is the headline contract the
// change exists to deliver, so it is pinned at the wire boundary, not just the
// Go field.
func TestUnsupportedResource_ReasonWireFormat(t *testing.T) {
	t.Parallel()

	withReason, err := json.Marshal(UnsupportedResource{
		Type:   "aws_kms_alias",
		ID:     "alias/aws/rds",
		Name:   "alias/aws/rds",
		Reason: imported.ReasonAWSManagedKMSAlias,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(withReason), `"reason":"aws_managed_kms_alias"`) {
		t.Errorf("expected `\"reason\":\"aws_managed_kms_alias\"` in JSON, got %s", withReason)
	}

	// A type-level row (no reason) must omit the key entirely.
	noReason, err := json.Marshal(UnsupportedResource{Type: "aws_lb", ID: "arn:...", Name: "my-lb"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(noReason), "reason") {
		t.Errorf("empty Reason must be omitted (omitempty), got %s", noReason)
	}

	// Round-trip: the code survives decode unchanged.
	var rt UnsupportedResource
	if err := json.Unmarshal(withReason, &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rt.Reason != imported.ReasonAWSManagedKMSAlias {
		t.Errorf("round-trip reason = %q, want %q", rt.Reason, imported.ReasonAWSManagedKMSAlias)
	}
}

func TestPartitionUnimportable_StampsWizardFields(t *testing.T) {
	in := []imported.ImportedResource{{Identity: imported.ResourceIdentity{
		Type:      "aws_kms_alias",
		Address:   "aws_kms_alias.rds",
		NameHint:  "alias/aws/rds",
		ImportID:  "alias/aws/rds",
		Region:    "us-east-1",
		NativeIDs: map[string]string{"name": "alias/aws/rds"},
		Tags:      map[string]string{"Project": "io-abc"},
	}}}

	_, dropped := partitionUnimportable(in)
	if len(dropped) != 1 {
		t.Fatalf("dropped: got %d, want 1", len(dropped))
	}
	d := dropped[0]
	if d.Type != "aws_kms_alias" || d.ID != "alias/aws/rds" || d.Name != "alias/aws/rds" {
		t.Errorf("unexpected wire fields: %+v", d)
	}
	if d.Region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", d.Region)
	}
	// Pin the literal category (not imported.Category("aws_kms_alias"), which
	// would tautologically re-invoke the function under test and pass vacuously
	// if the type were missing from the category map).
	if d.Group != "Security" {
		t.Errorf("group = %q, want Security", d.Group)
	}
	if d.Tags["Project"] != "io-abc" {
		t.Errorf("tags not carried through: %+v", d.Tags)
	}
}

func TestPartitionUnimportable_DropsInsideOutImportedMarker(t *testing.T) {
	in := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{
			Type:     "aws_vpc",
			Address:  "aws_vpc.already_managed",
			NameHint: "already-managed",
			ImportID: "vpc-123",
			Tags: map[string]string{
				"InsideOutImportProject": "123456789012",
				"InsideOutImported":      "true",
			},
		}},
		{Identity: imported.ResourceIdentity{
			Type:     "aws_vpc",
			Address:  "aws_vpc.bare_project_tag",
			NameHint: "bare-project-tag",
			ImportID: "vpc-456",
			Tags: map[string]string{
				"InsideOutImportProject": "123456789012",
			},
		}},
		{Identity: imported.ResourceIdentity{
			Type:     "google_storage_bucket",
			Address:  "google_storage_bucket.already_managed",
			NameHint: "already-managed-gcp",
			ImportID: "gcs-bucket",
			Tags: map[string]string{
				"insideout-imported": "true",
			},
		}},
	}

	keep, dropped := partitionUnimportable(in)
	if len(keep) != 1 {
		t.Fatalf("keep: got %d, want 1 (bare project tag only)", len(keep))
	}
	if keep[0].Identity.Address != "aws_vpc.bare_project_tag" {
		t.Errorf("kept address = %q, want aws_vpc.bare_project_tag", keep[0].Identity.Address)
	}

	if len(dropped) != 2 {
		t.Fatalf("dropped: got %d, want 2 (AWS + GCP imported markers)", len(dropped))
	}
	for i, row := range dropped {
		if row.Reason != imported.ReasonInsideOutImported {
			t.Errorf("dropped[%d].Reason = %q, want %q", i, row.Reason, imported.ReasonInsideOutImported)
		}
	}
	if dropped[0].ID != "vpc-123" || dropped[1].ID != "gcs-bucket" {
		t.Errorf("dropped IDs = [%q, %q], want [vpc-123, gcs-bucket]", dropped[0].ID, dropped[1].ID)
	}
}

func TestPartitionUnimportable_Empty(t *testing.T) {
	keep, dropped := partitionUnimportable(nil)
	if keep == nil || dropped == nil {
		t.Fatalf("keep and dropped must be non-nil; got keep=%v dropped=%v", keep, dropped)
	}
	if len(keep) != 0 || len(dropped) != 0 {
		t.Fatalf("expected empty slices, got keep=%d dropped=%d", len(keep), len(dropped))
	}
}

func TestUnimportableReasonsSummary(t *testing.T) {
	rows := []UnsupportedResource{
		{Reason: imported.ReasonAWSManagedKMSAlias},
		{Reason: imported.ReasonAWSManagedKMSAlias},
		{Reason: imported.ReasonServiceManagedENI},
	}
	got := unimportableReasonsSummary(rows)
	// Sorted by reason code: aws_managed_kms_alias < service_managed_eni.
	want := "2 aws_managed_kms_alias, 1 service_managed_eni"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}
