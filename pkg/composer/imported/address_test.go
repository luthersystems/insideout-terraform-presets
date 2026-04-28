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

func TestGenerateAddress_EmptyHints(t *testing.T) {
	t.Parallel()
	// Empty NameHint, NativeIDs, ImportID, AND Type — falls all the way
	// through to the hash-only label form.
	id := ResourceIdentity{Cloud: "aws"}
	got := GenerateAddress(id, neverExists)
	assert.True(t, strings.HasPrefix(got, "."), "no Type means address starts with `.`: %q", got)
	assert.Contains(t, got, ".r_")
	// 8 hex chars after the r_ prefix.
	parts := strings.Split(got, ".r_")
	require.Len(t, parts, 2)
	assert.Len(t, parts[1], 8)
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
