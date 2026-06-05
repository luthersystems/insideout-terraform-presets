package dependencies

import (
	"reflect"
	"sort"
	"testing"
)

// attrsResolve is a faithful, self-contained re-implementation of
// reliable's Attrs-based resolveImportedDependencies
// (internal/agentapi/import_dependencies.go). It is the oracle the
// discover-only ResolveFromIdentities must match byte-for-byte: same
// FieldRefs registry, same identifier candidate set, same target-type
// filter, same self-ref guard, same sorted-deduped output. Keeping a
// copy here lets the parity test prove equivalence without importing
// reliable. If reliable's resolver changes, this oracle (and the
// invariant) must change in lockstep.
func attrsResolve(rows []fixtureRow) map[string][]string {
	if len(rows) == 0 {
		return nil
	}
	// Build identifier → address index from the SAME candidate set
	// reliable uses: NativeIDs[arn|self_link|full_name|id] + ImportID.
	idIndex := map[string]string{}
	typeByAddr := map[string]string{}
	for _, r := range rows {
		if r.address == "" {
			continue
		}
		typeByAddr[r.address] = r.tfType
		for _, k := range []string{"arn", "self_link", "full_name", "id"} {
			if v := r.nativeIDs[k]; v != "" {
				if _, ok := idIndex[v]; !ok {
					idIndex[v] = r.address
				}
			}
		}
		if r.importID != "" {
			if _, ok := idIndex[r.importID]; !ok {
				idIndex[r.importID] = r.address
			}
		}
	}
	out := map[string][]string{}
	for _, r := range rows {
		if r.address == "" {
			continue
		}
		var deps []string
		for field, targetType := range fieldRefs {
			value, ok := r.attrs[field]
			if !ok || value == "" {
				continue
			}
			matched, ok := idIndex[value]
			if !ok || matched == r.address {
				continue
			}
			if typeByAddr[matched] != targetType {
				continue
			}
			deps = append(deps, matched)
		}
		if len(deps) > 0 {
			sort.Strings(deps)
			out[r.address] = dedupeSorted(deps)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fixtureRow models one discovered resource with BOTH the enriched-Attrs
// view (attrs) and the discover-only view (nativeIDs). For a given FK
// field the two views must agree: attrs[field] is what reliable reads
// from the enriched payload today; nativeIDs[field] is what the
// discoverer lifts into Identity.NativeIDs. The parity test asserts that
// resolving from either view yields the identical edge set.
type fixtureRow struct {
	tfType    string
	address   string
	importID  string
	nativeIDs map[string]string // canonical IDs (arn/id/…) + lifted FK fields
	attrs     map[string]string // enriched cross-ref fields keyed by FieldRefs name
}

func (r fixtureRow) edgeSource() EdgeSource {
	return EdgeSource{
		Address:   r.address,
		Type:      r.tfType,
		NativeIDs: r.nativeIDs,
		ImportID:  r.importID,
	}
}

func sources(rows []fixtureRow) []EdgeSource {
	out := make([]EdgeSource, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.edgeSource())
	}
	return out
}

// fkFixtures covers EVERY free-form FK field in FieldRefs(): the AWS
// fields wired into the Cloud Control extractors (role/role_arn,
// kms_key_arn, kms_key_id, kms_master_key_id, vpc_id, subnet_id) and the
// GCP fields (network, subnetwork, kms_key_name). Each fixture pairs a
// referencing resource (carrying the FK under both attrs[field] and
// nativeIDs[field]) with the target it points at.
func fkFixtures() map[string][]fixtureRow {
	return map[string][]fixtureRow{
		"role_arn → aws_iam_role": {
			{tfType: "aws_iam_role", address: "aws_iam_role.exec", importID: "exec-role",
				nativeIDs: map[string]string{"arn": "arn:aws:iam::1:role/exec-role"}},
			{tfType: "aws_lambda_function", address: "aws_lambda_function.api", importID: "api",
				nativeIDs: map[string]string{"arn": "arn:aws:lambda:us-east-1:1:function:api", "role_arn": "arn:aws:iam::1:role/exec-role"},
				attrs:     map[string]string{"role_arn": "arn:aws:iam::1:role/exec-role"}},
		},
		"role → aws_iam_role (by name/import-id)": {
			{tfType: "aws_iam_role", address: "aws_iam_role.r", importID: "my-role",
				nativeIDs: map[string]string{"arn": "arn:aws:iam::1:role/my-role"}},
			{tfType: "aws_iam_role_policy_attachment", address: "aws_iam_role_policy_attachment.a", importID: "my-role/arn:aws:iam::aws:policy/X",
				nativeIDs: map[string]string{"role": "my-role"},
				attrs:     map[string]string{"role": "my-role"}},
		},
		"kms_key_arn → aws_kms_key (lambda)": {
			{tfType: "aws_kms_key", address: "aws_kms_key.k", importID: "1234abcd-key",
				nativeIDs: map[string]string{"arn": "arn:aws:kms:us-east-1:1:key/1234abcd-key"}},
			{tfType: "aws_lambda_function", address: "aws_lambda_function.enc", importID: "enc",
				nativeIDs: map[string]string{"arn": "arn:aws:lambda:us-east-1:1:function:enc", "kms_key_arn": "arn:aws:kms:us-east-1:1:key/1234abcd-key"},
				attrs:     map[string]string{"kms_key_arn": "arn:aws:kms:us-east-1:1:key/1234abcd-key"}},
		},
		"kms_key_arn → aws_kms_key (dynamodb sse)": {
			{tfType: "aws_kms_key", address: "aws_kms_key.ddb", importID: "ddb-key",
				nativeIDs: map[string]string{"arn": "arn:aws:kms:us-east-1:1:key/ddb-key"}},
			{tfType: "aws_dynamodb_table", address: "aws_dynamodb_table.t", importID: "t",
				nativeIDs: map[string]string{"arn": "arn:aws:dynamodb:us-east-1:1:table/t", "kms_key_arn": "arn:aws:kms:us-east-1:1:key/ddb-key"},
				attrs:     map[string]string{"kms_key_arn": "arn:aws:kms:us-east-1:1:key/ddb-key"}},
		},
		"kms_key_id → aws_kms_key (rds, by arn)": {
			{tfType: "aws_kms_key", address: "aws_kms_key.rds", importID: "rds-key",
				nativeIDs: map[string]string{"arn": "arn:aws:kms:us-east-1:1:key/rds-key"}},
			{tfType: "aws_db_instance", address: "aws_db_instance.db", importID: "db",
				nativeIDs: map[string]string{"arn": "arn:aws:rds:us-east-1:1:db:db", "kms_key_id": "arn:aws:kms:us-east-1:1:key/rds-key"},
				attrs:     map[string]string{"kms_key_id": "arn:aws:kms:us-east-1:1:key/rds-key"}},
		},
		"kms_key_id → aws_kms_key (log group, by key id)": {
			{tfType: "aws_kms_key", address: "aws_kms_key.lg", importID: "lg-key-id",
				nativeIDs: map[string]string{"arn": "arn:aws:kms:us-east-1:1:key/lg-key-id"}},
			{tfType: "aws_cloudwatch_log_group", address: "aws_cloudwatch_log_group.app", importID: "/app",
				nativeIDs: map[string]string{"arn": "arn:aws:logs:us-east-1:1:log-group:/app", "kms_key_id": "lg-key-id"},
				attrs:     map[string]string{"kms_key_id": "lg-key-id"}},
		},
		"kms_master_key_id → aws_kms_key (sqs)": {
			{tfType: "aws_kms_key", address: "aws_kms_key.q", importID: "q-key",
				nativeIDs: map[string]string{"arn": "arn:aws:kms:us-east-1:1:key/q-key"}},
			{tfType: "aws_sqs_queue", address: "aws_sqs_queue.dlq", importID: "https://sqs/dlq",
				nativeIDs: map[string]string{"arn": "arn:aws:sqs:us-east-1:1:dlq", "kms_master_key_id": "q-key"},
				attrs:     map[string]string{"kms_master_key_id": "q-key"}},
		},
		"kms_master_key_id → aws_kms_key (sns)": {
			{tfType: "aws_kms_key", address: "aws_kms_key.t", importID: "topic-key",
				nativeIDs: map[string]string{"arn": "arn:aws:kms:us-east-1:1:key/topic-key"}},
			{tfType: "aws_sns_topic", address: "aws_sns_topic.alerts", importID: "arn:aws:sns:us-east-1:1:alerts",
				nativeIDs: map[string]string{"arn": "arn:aws:sns:us-east-1:1:alerts", "kms_master_key_id": "topic-key"},
				attrs:     map[string]string{"kms_master_key_id": "topic-key"}},
		},
		"vpc_id → aws_vpc": {
			{tfType: "aws_vpc", address: "aws_vpc.main", importID: "vpc-0abc",
				nativeIDs: map[string]string{"id": "vpc-0abc"}},
			{tfType: "aws_subnet", address: "aws_subnet.web", importID: "subnet-1",
				nativeIDs: map[string]string{"id": "subnet-1", "vpc_id": "vpc-0abc"},
				attrs:     map[string]string{"vpc_id": "vpc-0abc"}},
		},
		"subnet_id → aws_subnet": {
			{tfType: "aws_subnet", address: "aws_subnet.web", importID: "subnet-1",
				nativeIDs: map[string]string{"id": "subnet-1"}},
			{tfType: "aws_db_subnet_group", address: "aws_db_subnet_group.g", importID: "g",
				nativeIDs: map[string]string{"id": "g", "subnet_id": "subnet-1"},
				attrs:     map[string]string{"subnet_id": "subnet-1"}},
		},
		"network → google_compute_network": {
			{tfType: "google_compute_network", address: "google_compute_network.vpc", importID: "projects/p/global/networks/vpc",
				nativeIDs: map[string]string{"self_link": "https://www.googleapis.com/compute/v1/projects/p/global/networks/vpc"}},
			{tfType: "google_compute_subnetwork", address: "google_compute_subnetwork.sub", importID: "projects/p/regions/r/subnetworks/sub",
				nativeIDs: map[string]string{"self_link": "https://www.googleapis.com/compute/v1/projects/p/regions/r/subnetworks/sub", "network": "https://www.googleapis.com/compute/v1/projects/p/global/networks/vpc"},
				attrs:     map[string]string{"network": "https://www.googleapis.com/compute/v1/projects/p/global/networks/vpc"}},
		},
		"subnetwork → google_compute_subnetwork": {
			{tfType: "google_compute_subnetwork", address: "google_compute_subnetwork.sub", importID: "projects/p/regions/r/subnetworks/sub",
				nativeIDs: map[string]string{"self_link": "https://www.googleapis.com/compute/v1/projects/p/regions/r/subnetworks/sub"}},
			{tfType: "google_compute_instance", address: "google_compute_instance.vm", importID: "projects/p/zones/z/instances/vm",
				nativeIDs: map[string]string{"self_link": "https://www.googleapis.com/compute/v1/projects/p/zones/z/instances/vm", "subnetwork": "https://www.googleapis.com/compute/v1/projects/p/regions/r/subnetworks/sub"},
				attrs:     map[string]string{"subnetwork": "https://www.googleapis.com/compute/v1/projects/p/regions/r/subnetworks/sub"}},
		},
		"kms_key_name → google_kms_crypto_key": {
			{tfType: "google_kms_crypto_key", address: "google_kms_crypto_key.k", importID: "projects/p/locations/l/keyRings/kr/cryptoKeys/k",
				nativeIDs: map[string]string{"id": "projects/p/locations/l/keyRings/kr/cryptoKeys/k"}},
			{tfType: "google_storage_bucket", address: "google_storage_bucket.b", importID: "b",
				nativeIDs: map[string]string{"self_link": "https://www.googleapis.com/storage/v1/b/b", "kms_key_name": "projects/p/locations/l/keyRings/kr/cryptoKeys/k"},
				attrs:     map[string]string{"kms_key_name": "projects/p/locations/l/keyRings/kr/cryptoKeys/k"}},
		},
	}
}

// TestResolveFromIdentities_ParityWithAttrs is the load-bearing parity
// assertion (presets#733): for EVERY FieldRefs() FK field, the
// discover-only resolver (reading NativeIDs) must produce the exact same
// edge set the Attrs-based oracle produces — proving the picker's
// "auto-included N dependencies" closure does not regress when scan-time
// enrichment is dropped.
func TestResolveFromIdentities_ParityWithAttrs(t *testing.T) {
	t.Parallel()
	for name, rows := range fkFixtures() {
		rows := rows
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wantAttrs := attrsResolve(rows)
			gotNative := ResolveFromIdentities(sources(rows))
			if !reflect.DeepEqual(gotNative, wantAttrs) {
				t.Fatalf("discover-only edges diverge from Attrs edges\n native=%v\n  attrs=%v", gotNative, wantAttrs)
			}
			if len(gotNative) == 0 {
				t.Fatalf("fixture %q produced no edge — fixture is not exercising the FK path", name)
			}
		})
	}
}

// TestResolveFromIdentities_CoversEveryFieldRef pins that the fixture set
// exercises every FieldRefs() field, so a future registry addition that
// lands without a parity fixture fails loudly rather than silently
// skipping the new edge.
func TestResolveFromIdentities_CoversEveryFieldRef(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{}
	for _, rows := range fkFixtures() {
		for _, r := range rows {
			for field := range r.attrs {
				covered[field] = true
			}
		}
	}
	for field := range FieldRefs() {
		if !covered[field] {
			t.Errorf("FieldRefs()[%q] has no parity fixture in fkFixtures()", field)
		}
	}
}

// TestResolveFromIdentities_AllFixturesCombined runs the resolver over the
// union of every fixture at once — the realistic discover-output shape
// where all source + target rows coexist — and asserts the combined edge
// set still matches the Attrs oracle. This catches cross-fixture
// identifier collisions the per-field runs cannot.
func TestResolveFromIdentities_AllFixturesCombined(t *testing.T) {
	t.Parallel()
	var all []fixtureRow
	for _, rows := range fkFixtures() {
		all = append(all, rows...)
	}
	wantAttrs := attrsResolve(all)
	gotNative := ResolveFromIdentities(sources(all))
	if !reflect.DeepEqual(gotNative, wantAttrs) {
		t.Fatalf("combined discover-only edges diverge from Attrs edges\n native=%v\n  attrs=%v", gotNative, wantAttrs)
	}
}

// TestResolveFromIdentities_DropsUnresolvedAndSelfAndWrongType pins the
// best-effort guards that mirror reliable's resolver.
func TestResolveFromIdentities_DropsUnresolvedAndSelfAndWrongType(t *testing.T) {
	t.Parallel()
	rows := []EdgeSource{
		// FK pointing outside the discovered set — dropped.
		{Type: "aws_lambda_function", Address: "aws_lambda_function.dangling",
			NativeIDs: map[string]string{"role_arn": "arn:aws:iam::1:role/not-discovered"}},
		// FK whose value resolves to a sibling of the WRONG type — dropped
		// (an aws_sqs_queue is not an aws_iam_role).
		{Type: "aws_sqs_queue", Address: "aws_sqs_queue.q",
			NativeIDs: map[string]string{"arn": "arn:aws:sqs:us-east-1:1:q"}},
		{Type: "aws_lambda_function", Address: "aws_lambda_function.wrongtype",
			NativeIDs: map[string]string{"role_arn": "arn:aws:sqs:us-east-1:1:q"}},
		// Self-reference — dropped.
		{Type: "aws_iam_role", Address: "aws_iam_role.selfref", ImportID: "self",
			NativeIDs: map[string]string{"arn": "self", "role_arn": "self"}},
	}
	if got := ResolveFromIdentities(rows); got != nil {
		t.Fatalf("expected no edges, got %v", got)
	}
}

// TestResolveFromIdentities_Empty pins the nil-on-empty contract.
func TestResolveFromIdentities_Empty(t *testing.T) {
	t.Parallel()
	if got := ResolveFromIdentities(nil); got != nil {
		t.Fatalf("ResolveFromIdentities(nil) = %v, want nil", got)
	}
	if got := ResolveFromIdentities([]EdgeSource{{Type: "aws_vpc", Address: "aws_vpc.x"}}); got != nil {
		t.Fatalf("ResolveFromIdentities(no-fk) = %v, want nil", got)
	}
}
