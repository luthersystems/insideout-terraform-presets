package gcpdiscover

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIAMRoleSuffix pins the role-suffix extraction used in the
// Bundle G1 IAM discoverers' NameHints. A mutation that broke the
// last-slash-then-last-dot logic (e.g. by returning the empty string)
// would silently still yield non-empty terraform addresses because
// the caller appends to a parent name — without these direct
// assertions the rest of the IAM tests pass green.
func TestIAMRoleSuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "predefined role with service.suffix", in: "roles/secretmanager.secretAccessor", want: "secretAccessor"},
		{name: "predefined storage role", in: "roles/storage.objectViewer", want: "objectViewer"},
		{name: "custom org role", in: "organizations/123/roles/foo", want: "foo"},
		{name: "bare role id (no slash, no dot)", in: "owner", want: "owner"},
		{name: "role with whitespace", in: "  roles/run.invoker  ", want: "invoker"},
		{name: "empty input", in: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, iamRoleSuffix(tc.in))
		})
	}
}

// TestIAMMemberSuffix pins the member-suffix extraction used in the
// Bundle G1 _iam_member discoverers' NameHints. The transformation
// is intentionally lossy (the canonical Member lives on
// NativeIDs["member"]) — these cases pin both the dominant shapes
// (typed prefix + email) and the special cases (allUsers,
// allAuthenticatedUsers, domain-shaped) that the function doc-
// comment promises to handle.
func TestIAMMemberSuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "service account email", in: "serviceAccount:foo@bar.iam.gserviceaccount.com", want: "foo"},
		{name: "user email", in: "user:alice@example.com", want: "alice"},
		{name: "group email", in: "group:eng@example.com", want: "eng"},
		{name: "allUsers", in: "allUsers", want: "allUsers"},
		{name: "allAuthenticatedUsers", in: "allAuthenticatedUsers", want: "allAuthenticatedUsers"},
		{name: "domain", in: "domain:example.com", want: "example-com"},
		{name: "member with whitespace", in: "  user:alice@example.com  ", want: "alice"},
		{name: "empty input", in: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, iamMemberSuffix(tc.in))
		})
	}
}
