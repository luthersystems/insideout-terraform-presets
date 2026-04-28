package imported

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAddress_BasicLabel(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Cloud:    "aws",
		Type:     "aws_sqs_queue",
		NameHint: "orders-DLQ",
	}
	got := GenerateAddress(id, neverExists)
	assert.Equal(t, "aws_sqs_queue.orders_dlq", got)
}

func TestGenerateAddress_LeadingDigit(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Cloud:    "aws",
		Type:     "aws_s3_bucket",
		NameHint: "123abc",
	}
	got := GenerateAddress(id, neverExists)
	assert.Equal(t, "aws_s3_bucket.r_123abc", got)
}

func TestGenerateAddress_NameHintBeatsNativeIDs(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Type:     "aws_sqs_queue",
		NameHint: "explicit",
		NativeIDs: map[string]string{
			"name": "should-not-be-used",
			"arn":  "arn:aws:sqs:us-east-1:000000000000:also-ignored",
		},
	}
	assert.Equal(t, "aws_sqs_queue.explicit",
		GenerateAddress(id, neverExists))
}

func TestGenerateAddress_FallsBackToNativeNameThenArn(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Type:      "aws_sqs_queue",
		NativeIDs: map[string]string{"name": "from-native-name"},
	}
	assert.Equal(t, "aws_sqs_queue.from_native_name",
		GenerateAddress(id, neverExists))

	id = ResourceIdentity{
		Type: "aws_sqs_queue",
		NativeIDs: map[string]string{
			"arn": "arn:aws:sqs:us-east-1:000000000000:from-arn",
		},
	}
	assert.Equal(t, "aws_sqs_queue.from_arn",
		GenerateAddress(id, neverExists))
}

func TestGenerateAddress_FallsBackToImportIDFinalSegment(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Type:     "google_compute_instance",
		ImportID: "projects/p/zones/us-central1-a/instances/my-vm",
	}
	assert.Equal(t, "google_compute_instance.my_vm",
		GenerateAddress(id, neverExists))
}

func TestGenerateAddress_FallsBackToTypeStem(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{Type: "aws_sqs_queue"}
	assert.Equal(t, "aws_sqs_queue.sqs_queue",
		GenerateAddress(id, neverExists))

	id = ResourceIdentity{Type: "google_storage_bucket"}
	assert.Equal(t, "google_storage_bucket.storage_bucket",
		GenerateAddress(id, neverExists))
}

func TestGenerateAddress_EmptyTypeReturnsEmpty(t *testing.T) {
	t.Parallel()
	// Type is mandatory. With an empty Type the function must return ""
	// rather than emit a malformed `.label` address.
	for _, in := range []ResourceIdentity{
		{Cloud: "aws"},
		{Cloud: "aws", NameHint: "orders"},
		{Type: "   "}, // whitespace-only also rejected
	} {
		assert.Equalf(t, "", GenerateAddress(in, neverExists),
			"empty/whitespace Type must yield empty address; in=%+v", in)
	}
}

func TestGenerateAddress_AllHintsEmptyFallsBackToTypeStem(t *testing.T) {
	t.Parallel()
	// With Type set but every hint source empty, the type stem provides
	// the label (per pickNameHint precedence step 6).
	id := ResourceIdentity{Type: "aws_sqs_queue"}
	assert.Equal(t, "aws_sqs_queue.sqs_queue",
		GenerateAddress(id, neverExists))
}

func TestGenerateAddress_LengthCap(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Type:     "aws_s3_bucket",
		NameHint: strings.Repeat("a", 200),
	}
	got := GenerateAddress(id, neverExists)
	parts := strings.SplitN(got, ".", 2)
	require.Len(t, parts, 2)
	assert.LessOrEqual(t, len(parts[1]), maxLabelLen-suffixReserve)
}

func TestGenerateAddress_CollisionAppendsHash(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Cloud:    "aws",
		Type:     "aws_sqs_queue",
		NameHint: "orders",
		ImportID: "arn:aws:sqs:us-east-1:000000000000:orders",
	}
	taken := map[string]bool{
		"aws_sqs_queue.orders": true,
	}
	got := GenerateAddress(id, func(addr string) bool { return taken[addr] })

	require.True(t, strings.HasPrefix(got, "aws_sqs_queue.orders_"))
	suffix := strings.TrimPrefix(got, "aws_sqs_queue.orders_")
	assert.Len(t, suffix, hashSuffixLen, "collision suffix must be 8 hex chars: %q", got)
}

func TestGenerateAddress_DeterministicHashAcrossMapOrder(t *testing.T) {
	t.Parallel()
	mk := func(seed int) ResourceIdentity {
		// Build the ProviderIdentity map in different insertion orders to
		// shake out any reliance on Go map iteration order.
		pi := make(map[string]string, 4)
		keys := []string{"region", "account", "name", "version"}
		vals := []string{"us-east-1", "123", "orders", "v1"}
		// Cycle through a deterministic but different starting offset per
		// seed; Go's map insertion order is randomized, so this is
		// belt-and-braces alongside the sort in identityHash.
		for i := range keys {
			j := (i + seed) % len(keys)
			pi[keys[j]] = vals[j]
		}
		return ResourceIdentity{
			Cloud:            "aws",
			Type:             "aws_sqs_queue",
			AccountID:        "123",
			Region:           "us-east-1",
			ImportID:         "arn:aws:sqs:us-east-1:123:orders",
			ProviderIdentity: pi,
		}
	}

	taken := map[string]bool{"aws_sqs_queue.orders": true}
	exists := func(addr string) bool { return taken[addr] }

	addr0 := GenerateAddress(mk(0), exists)
	for i := 1; i < 100; i++ {
		got := GenerateAddress(mk(i), exists)
		require.Equalf(t, addr0, got,
			"identity hash must be order-independent; iter %d got %q want %q",
			i, got, addr0)
	}
}

func TestGenerateAddress_HashCollisionFallsBackToCounter(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Cloud:    "aws",
		Type:     "aws_sqs_queue",
		NameHint: "orders",
	}
	// First two candidates collide; force the counter path.
	bareAddr := "aws_sqs_queue.orders"
	hashAddr := bareAddr + "_" + identityHash(id)[:hashSuffixLen]
	taken := map[string]bool{
		bareAddr: true,
		hashAddr: true,
	}
	got := GenerateAddress(id, func(addr string) bool { return taken[addr] })
	assert.Equal(t, hashAddr+"_2", got)
}

func TestGenerateAddress_NeverReuseRetired(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Cloud:    "aws",
		Type:     "aws_sqs_queue",
		NameHint: "orders",
	}
	bareAddr := "aws_sqs_queue.orders"
	// The predicate models retired addresses as still "taken" so the
	// generator must skip them.
	retired := map[string]bool{bareAddr: true}
	got := GenerateAddress(id, func(addr string) bool { return retired[addr] })
	assert.NotEqual(t, bareAddr, got)
}

// TestPickNameHint_AdjacentPairPrecedence locks the documented hint-source
// precedence: NameHint > NativeIDs[name] > NativeIDs[arn]-last-segment >
// NativeIDs[self_link]-last-segment > ImportID-last-segment > type stem.
// A regression that swaps any adjacent pair shows up as exactly one failing
// row.
func TestPickNameHint_AdjacentPairPrecedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   ResourceIdentity
		want string // the resulting label portion of the address
	}{
		{
			name: "NameHint_beats_NativeIDsName",
			id: ResourceIdentity{
				Type:      "aws_sqs_queue",
				NameHint:  "from-hint",
				NativeIDs: map[string]string{"name": "from-name"},
			},
			want: "from_hint",
		},
		{
			name: "NativeIDsName_beats_ARN",
			id: ResourceIdentity{
				Type: "aws_sqs_queue",
				NativeIDs: map[string]string{
					"name": "from-name",
					"arn":  "arn:aws:sqs:us-east-1:000000000000:from-arn",
				},
			},
			want: "from_name",
		},
		{
			name: "ARN_beats_SelfLink",
			id: ResourceIdentity{
				Type: "aws_sqs_queue",
				NativeIDs: map[string]string{
					"arn":       "arn:aws:sqs:us-east-1:000000000000:from-arn",
					"self_link": "https://example.com/projects/p/queues/from-self-link",
				},
			},
			want: "from_arn",
		},
		{
			name: "SelfLink_beats_ImportID",
			id: ResourceIdentity{
				Type: "google_compute_instance",
				NativeIDs: map[string]string{
					"self_link": "https://compute.googleapis.com/.../instances/from-self-link",
				},
				ImportID: "projects/p/zones/us-central1-a/instances/from-import",
			},
			want: "from_self_link",
		},
		{
			name: "ImportID_beats_TypeStem",
			id: ResourceIdentity{
				Type:     "aws_sqs_queue",
				ImportID: "arn:aws:sqs:us-east-1:000000000000:from-import",
			},
			want: "from_import",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := GenerateAddress(tc.id, neverExists)
			want := tc.id.Type + "." + tc.want
			assert.Equal(t, want, got)
		})
	}
}

// TestGenerateAddress_HashCollisionFallsBackToCounterDeep walks the counter
// past the first iteration. Catches off-by-one errors in the counter loop's
// starting value (must be 2, not 1 or 3).
func TestGenerateAddress_HashCollisionFallsBackToCounterDeep(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Cloud:    "aws",
		Type:     "aws_sqs_queue",
		NameHint: "orders",
	}
	bareAddr := "aws_sqs_queue.orders"
	hashAddr := bareAddr + "_" + identityHash(id)[:hashSuffixLen]
	taken := map[string]bool{
		bareAddr:        true,
		hashAddr:        true,
		hashAddr + "_2": true,
		hashAddr + "_3": true,
	}
	got := GenerateAddress(id, func(addr string) bool { return taken[addr] })
	assert.Equal(t, hashAddr+"_4", got)
}

// TestIdentityHash_Golden locks the canonical-render order of identityHash
// against accidental field reorders. The expected hex was computed once
// against the ResourceIdentity below; if anyone reorders the fields in
// identityHash's strings.Builder (or changes a separator), this test breaks
// immediately.
func TestIdentityHash_Golden(t *testing.T) {
	t.Parallel()
	id := ResourceIdentity{
		Cloud:     "aws",
		Type:      "aws_sqs_queue",
		AccountID: "123456789012",
		Region:    "us-east-1",
		ImportID:  "arn:aws:sqs:us-east-1:123456789012:orders-DLQ",
		ProviderIdentity: map[string]string{
			"name":   "orders-DLQ",
			"region": "us-east-1",
		},
	}
	const wantHex = "7e95577c62625f7b2b2c9b8bea1930a115d49453f3496f8b01a68a49babfeec8"
	got := identityHash(id)
	assert.Equal(t, wantHex, got,
		"identityHash must produce the locked-in hex; if this fails after an intentional change to the canonical render, update the golden value")
}

// TestLastSegment_Cases pins the documented separator-cascade behavior of
// lastSegment (`/` then `:` then `,`, applied sequentially to whatever the
// previous step returned). Adding a comma test guards against a future
// refactor that drops one of the separators.
func TestLastSegment_Cases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"only-spaces", "   ", ""},
		{"no-separator", "foo", "foo"},
		{"slash", "a/b/c", "c"},
		{"colon", "a:b:c", "c"},
		{"comma", "a,b,c", "c"},
		{"slash-then-colon", "foo:bar/baz", "baz"},
		{"colon-then-slash-then-comma", "a/b:c,d", "d"},
		{"trailing-separator-ignored", "a/b/", "a/b/"},
		{"arn-style", "arn:aws:sqs:us-east-1:000000000000:orders-DLQ", "orders-DLQ"},
		{"url-style", "https://sqs.us-east-1.amazonaws.com/000000000000/orders-DLQ", "orders-DLQ"},
		{"gcp-self-link-style", "https://compute.googleapis.com/projects/p/zones/z/instances/my-vm", "my-vm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, lastSegment(tc.in))
		})
	}
}

func TestNormalizeLabel_Cases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"only-spaces", "   ", ""},
		{"already-clean", "abc_def", "abc_def"},
		{"uppercase", "ABC", "abc"},
		{"dashes-and-dots", "my.name-thing", "my_name_thing"},
		{"collapse-underscores", "a___b__c", "a_b_c"},
		{"trim-edges", "__abc__", "abc"},
		{"leading-digit", "9lives", "r_9lives"},
		{"all-special", "@@@", ""},
		{"unicode-letters-dropped", "café", "caf"},
		{"only-numeric", "12345", "r_12345"},
		{"only-underscore", "____", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeLabel(tc.in, maxLabelLen-suffixReserve)
			assert.Equal(t, tc.want, got)
		})
	}
}

// neverExists is a predicate that reports every address as available; used
// when we want to assert the unsuffixed form.
func neverExists(string) bool { return false }
