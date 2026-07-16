package depchase

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// scopeOf builds an in-scope type set for a SelectionScopePolicy in tests.
func scopeOf(types ...string) AdoptionPolicy {
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	return SelectionScopePolicy{InScopeTypes: set}
}

// TestRun_ClosureContract_OutOfScopeRefIsReferencedNotAdopted is the
// reliable#2231 incident-shape regression (presets#864). A single-category
// "Data storage" selection (an aws_dynamodb_table, plus the customer KMS key it
// encrypts with) references, in its generated HCL, a foreign admin IAM role the
// operator never selected. Historically dep-chase adopted that role transitively
// (the 238-resource blow-up). With the closure contract's SelectionScopePolicy
// scoped to the selection's types, the out-of-scope IAM role must be REPRESENTED
// AS A REFERENCE — the literal stays in the generated config (valid Terraform
// for a concrete ARN) and the role is NOT adopted — while the in-scope KMS key
// is still adopted, carrying pulled_in_by provenance.
func TestRun_ClosureContract_OutOfScopeRefIsReferencedNotAdopted(t *testing.T) {
	t.Parallel()
	const kmsARN = "arn:aws:kms:us-east-1:123:key/abcd-1234-in-scope"
	const adminRoleARN = "arn:aws:iam::123:role/admin-out-of-scope"

	// The selected data store references BOTH an in-scope KMS key and an
	// out-of-scope admin IAM role.
	gen0 := `
resource "aws_dynamodb_table" "orders" {
  name        = "io-orders"
  kms_key_arn = "` + kmsARN + `"
  role_arn    = "` + adminRoleARN + `"
}`
	// After the KMS key is adopted, genconfig re-emits with its resource block so
	// the KMS ARN enters the resolved set. The admin-role literal is retained in
	// the dynamodb block (proving reference-representation) but is terminal so it
	// does not resurface.
	gen1 := gen0 + `
resource "aws_kms_key" "orders_key" {
  arn = "` + kmsARN + `"
}`
	dir := writeGen(t, gen0)

	table := newRes("aws_dynamodb_table.orders", "io-orders",
		"arn:aws:dynamodb:us-east-1:123:table/io-orders", "aws_dynamodb_table")
	kmsKey := newRes("aws_kms_key.orders_key", kmsARN, kmsARN, "aws_kms_key")
	// The admin role is importable and unclaimed — the ONLY thing keeping it out
	// of the managed set is the scope bound.
	adminRole := newRes("aws_iam_role.admin", "admin-out-of-scope", adminRoleARN, "aws_iam_role")

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_kms_key|" + kmsARN:        kmsKey,
		"aws_iam_role|" + adminRoleARN: adminRole,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen1}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer:     disc,
		Pipeline:       p.fns(),
		AdoptionPolicy: scopeOf("aws_dynamodb_table", "aws_kms_key"),
	}, []imported.ImportedResource{table})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The out-of-scope admin role must NOT be adopted; the in-scope KMS key must.
	if len(got.Added) != 1 || got.Added[0].Identity.Type != "aws_kms_key" {
		t.Fatalf("Added=%+v, want exactly one aws_kms_key (admin role must be referenced, not adopted)", got.Added)
	}
	for _, r := range got.Added {
		if r.Identity.Type == "aws_iam_role" {
			t.Fatalf("the out-of-scope admin IAM role was ADOPTED — the closure was not bounded: %+v", r)
		}
	}

	// The adopted KMS key must carry pulled_in_by provenance naming its consumer.
	prov := got.Added[0].PulledInBy
	if prov == nil {
		t.Fatalf("adopted KMS key missing pulled_in_by provenance")
	}
	if prov.Reason != imported.PulledInReasonDependencyChase {
		t.Errorf("provenance Reason=%q, want %q", prov.Reason, imported.PulledInReasonDependencyChase)
	}
	if len(prov.Consumers) != 1 || prov.Consumers[0] != "aws_dynamodb_table.orders" {
		t.Errorf("provenance Consumers=%v, want [aws_dynamodb_table.orders]", prov.Consumers)
	}

	// imported.json is written from res.Resources — the KMS key there must ALSO
	// carry provenance, and the selected table must NOT.
	var sawKeyProv, sawTable bool
	for _, r := range got.Resources {
		switch r.Identity.Address {
		case "aws_kms_key.orders_key":
			sawKeyProv = r.PulledInBy != nil && r.PulledInBy.Reason == imported.PulledInReasonDependencyChase
		case "aws_dynamodb_table.orders":
			sawTable = true
			if r.PulledInBy != nil {
				t.Errorf("selected table carries pulled_in_by=%+v, want nil (operator-selected)", r.PulledInBy)
			}
		case "aws_iam_role.admin":
			t.Errorf("out-of-scope admin role leaked into res.Resources: %+v", r)
		}
	}
	if !sawKeyProv {
		t.Errorf("res.Resources KMS key missing provenance; got %+v", got.Resources)
	}
	if !sawTable {
		t.Errorf("selected table missing from res.Resources: %+v", got.Resources)
	}

	// A machine-readable reference-retained warning must explain the omission.
	var refWarn *Warning
	for i := range got.Warnings {
		if got.Warnings[i].Code == CodeReferenceRetained {
			refWarn = &got.Warnings[i]
		}
	}
	if refWarn == nil {
		t.Fatalf("no %s warning emitted; got %+v", CodeReferenceRetained, got.Warnings)
	}
	if refWarn.Literal != adminRoleARN {
		t.Errorf("reference-retained warning Literal=%q, want %q", refWarn.Literal, adminRoleARN)
	}
	if refWarn.Reason != ReferenceReasonOutOfScope {
		t.Errorf("reference-retained warning Reason=%q, want %q", refWarn.Reason, ReferenceReasonOutOfScope)
	}
	if refWarn.Consumer != "aws_dynamodb_table.orders" {
		t.Errorf("reference-retained warning Consumer=%q, want the referencing table", refWarn.Consumer)
	}

	// No managed→managed edge to the un-adopted role.
	for _, e := range got.Edges {
		if strings.Contains(e.To, "aws_iam_role") {
			t.Errorf("edge to un-adopted role recorded: %+v", e)
		}
	}

	// The literal must be RETAINED in the generated config — the proof that the
	// generated config still validates without adopting the role (a concrete ARN
	// string literal is valid Terraform).
	raw, err := os.ReadFile(filepath.Join(dir, generatedFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), adminRoleARN) {
		t.Errorf("admin-role literal was not retained in generated.tf; reference representation broken:\n%s", raw)
	}
}

// TestRun_ClosureContract_NilPolicyAdoptsEverything guards the default: with no
// AdoptionPolicy the historical adopt-all closure is unchanged — BOTH the
// in-scope KMS key AND the (now un-bounded) admin IAM role are adopted. This is
// the safety pin proving the contract is opt-in and prod behavior is untouched
// unless a caller passes a policy. Provenance is stamped either way.
func TestRun_ClosureContract_NilPolicyAdoptsEverything(t *testing.T) {
	t.Parallel()
	const kmsARN = "arn:aws:kms:us-east-1:123:key/abcd-1234"
	const adminRoleARN = "arn:aws:iam::123:role/admin"

	gen0 := `
resource "aws_dynamodb_table" "orders" {
  name        = "io-orders"
  kms_key_arn = "` + kmsARN + `"
  role_arn    = "` + adminRoleARN + `"
}`
	gen1 := gen0 + `
resource "aws_kms_key" "orders_key" {
  arn = "` + kmsARN + `"
}
resource "aws_iam_role" "admin" {
  arn = "` + adminRoleARN + `"
}`
	dir := writeGen(t, gen0)

	table := newRes("aws_dynamodb_table.orders", "io-orders",
		"arn:aws:dynamodb:us-east-1:123:table/io-orders", "aws_dynamodb_table")
	kmsKey := newRes("aws_kms_key.orders_key", kmsARN, kmsARN, "aws_kms_key")
	adminRole := newRes("aws_iam_role.admin", "admin", adminRoleARN, "aws_iam_role")

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_kms_key|" + kmsARN:        kmsKey,
		"aws_iam_role|" + adminRoleARN: adminRole,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen1}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer: disc,
		Pipeline:   p.fns(),
		// AdoptionPolicy intentionally nil — historical adopt-all.
	}, []imported.ImportedResource{table})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.Added) != 2 {
		t.Fatalf("Added=%d, want 2 (nil policy adopts everything importable)", len(got.Added))
	}
	// No reference-retained warnings under adopt-all.
	for _, w := range got.Warnings {
		if w.Code == CodeReferenceRetained {
			t.Errorf("unexpected reference-retained warning under nil policy: %+v", w)
		}
	}
	// Provenance is stamped regardless of policy — both auto-included resources
	// carry pulled_in_by.
	for _, r := range got.Added {
		if r.PulledInBy == nil || r.PulledInBy.Reason != imported.PulledInReasonDependencyChase {
			t.Errorf("adopted %s missing dependency_chase provenance: %+v", r.Identity.Address, r.PulledInBy)
		}
	}
}

// TestSelectionScopePolicy_AdoptDependency unit-pins the policy's verdict:
// in-scope types adopt, out-of-scope types reference with the stable reason.
func TestSelectionScopePolicy_AdoptDependency(t *testing.T) {
	t.Parallel()
	pol := SelectionScopePolicy{InScopeTypes: map[string]struct{}{"aws_s3_bucket": {}}}

	inScope := imported.ImportedResource{Identity: imported.ResourceIdentity{Type: "aws_s3_bucket"}}
	if d := pol.AdoptDependency(inScope, nil); !d.Adopt {
		t.Errorf("in-scope type: Adopt=false, want true (%+v)", d)
	}

	outScope := imported.ImportedResource{Identity: imported.ResourceIdentity{Type: "aws_iam_role"}}
	d := pol.AdoptDependency(outScope, nil)
	if d.Adopt {
		t.Errorf("out-of-scope type: Adopt=true, want false")
	}
	if d.Reason != ReferenceReasonOutOfScope {
		t.Errorf("out-of-scope Reason=%q, want %q", d.Reason, ReferenceReasonOutOfScope)
	}
}

// TestNewSelectionScopePolicy_BuildsFromResourceTypes pins the scope-derivation
// helper reverse-import uses: InScopeTypes is exactly the set of Terraform types
// present in the resource set entering dep-chase.
func TestNewSelectionScopePolicy_BuildsFromResourceTypes(t *testing.T) {
	t.Parallel()
	pol := NewSelectionScopePolicy([]imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_s3_bucket"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table"}},
		{Identity: imported.ResourceIdentity{Type: "aws_s3_bucket"}}, // dup collapses
		{Identity: imported.ResourceIdentity{Type: ""}},              // blank skipped
	})
	if len(pol.InScopeTypes) != 2 {
		t.Fatalf("InScopeTypes=%v, want exactly {aws_s3_bucket, aws_dynamodb_table}", pol.InScopeTypes)
	}
	for _, want := range []string{"aws_s3_bucket", "aws_dynamodb_table"} {
		if _, ok := pol.InScopeTypes[want]; !ok {
			t.Errorf("InScopeTypes missing %q", want)
		}
	}
	if _, ok := pol.InScopeTypes[""]; ok {
		t.Errorf("InScopeTypes contains the blank type")
	}
}

// TestMergeProvenance_UnionsConsumers pins that a target referenced by two
// consumers accumulates both, sorted and de-duplicated.
func TestMergeProvenance_UnionsConsumers(t *testing.T) {
	t.Parallel()
	m := map[string]*imported.PulledInBy{}
	mergeProvenance(m, "aws_kms_key.k", []string{"aws_sqs_queue.b"})
	mergeProvenance(m, "aws_kms_key.k", []string{"aws_sqs_queue.a", "aws_sqs_queue.b"})
	mergeProvenance(m, "", []string{"ignored"}) // blank addr is a no-op

	p := m["aws_kms_key.k"]
	if p == nil {
		t.Fatal("provenance not recorded")
	}
	if p.Reason != imported.PulledInReasonDependencyChase {
		t.Errorf("Reason=%q, want %q", p.Reason, imported.PulledInReasonDependencyChase)
	}
	want := []string{"aws_sqs_queue.a", "aws_sqs_queue.b"}
	if len(p.Consumers) != len(want) || p.Consumers[0] != want[0] || p.Consumers[1] != want[1] {
		t.Errorf("Consumers=%v, want %v (sorted, de-duplicated)", p.Consumers, want)
	}
	if _, ok := m[""]; ok {
		t.Errorf("blank addr recorded provenance")
	}
}

// TestStampProvenance_SkipsOperatorSelectedAddressOnCollision pins the #866
// review finding: when a chased duplicate generates the SAME Terraform address
// as an operator-selected resource (the dedupeByAddress collision class —
// e.g. an IAM role selected by bare ImportID while another resource references
// its full ARN), the address-keyed provByAddr must NOT stamp
// pulled_in_by:dependency_chase onto the operator-selected entry.
// dedupeByAddress keeps that first entry, so a stamp there would ship a
// provenance lie into imported.json. Genuinely-discovered addresses still get
// stamped, and an existing stamp is never overwritten.
func TestStampProvenance_SkipsOperatorSelectedAddressOnCollision(t *testing.T) {
	t.Parallel()
	const collidedAddr = "aws_iam_role.admin"
	const discoveredAddr = "aws_kms_key.k"

	prior := &imported.PulledInBy{Reason: imported.PulledInReasonDependencyChase, Consumers: []string{"aws_sqs_queue.prior"}}
	resources := []imported.ImportedResource{
		// Operator-selected original (pre-chase in-set).
		{Identity: imported.ResourceIdentity{Address: collidedAddr}},
		// Chased duplicate at the SAME address, appended by a later iteration.
		{Identity: imported.ResourceIdentity{Address: collidedAddr}},
		// Genuinely discovered resource at a fresh address.
		{Identity: imported.ResourceIdentity{Address: discoveredAddr}},
		// Already-stamped entry must keep its existing provenance pointer.
		{Identity: imported.ResourceIdentity{Address: discoveredAddr}, PulledInBy: prior},
	}
	provByAddr := map[string]*imported.PulledInBy{
		collidedAddr:   {Reason: imported.PulledInReasonDependencyChase, Consumers: []string{"aws_sqs_queue.c"}},
		discoveredAddr: {Reason: imported.PulledInReasonDependencyChase, Consumers: []string{"aws_sqs_queue.d"}},
	}
	operatorAddrs := map[string]struct{}{collidedAddr: {}}

	stampProvenance(resources, provByAddr, operatorAddrs)

	if resources[0].PulledInBy != nil {
		t.Errorf("operator-selected entry at %s was stamped %+v; must stay nil", collidedAddr, resources[0].PulledInBy)
	}
	if resources[1].PulledInBy != nil {
		t.Errorf("duplicate at the collided operator address was stamped; dedupeByAddress keeps entry 0, and Added carries the chase provenance instead")
	}
	if resources[2].PulledInBy == nil || len(resources[2].PulledInBy.Consumers) != 1 || resources[2].PulledInBy.Consumers[0] != "aws_sqs_queue.d" {
		t.Errorf("discovered entry at %s not stamped correctly: %+v", discoveredAddr, resources[2].PulledInBy)
	}
	if resources[3].PulledInBy != prior {
		t.Errorf("pre-stamped entry's provenance pointer was replaced")
	}
}
