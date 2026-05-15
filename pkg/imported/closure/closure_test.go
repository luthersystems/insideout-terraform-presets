package closure_test

import (
	"encoding/json"
	"testing"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/closure"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkResource is a small helper that fabricates an ImportedResource with
// the minimum fields needed for closure tests: an Address (for sorting
// + identity), an ARN in NativeIDs (the most common reference shape),
// and an Attrs payload carrying outgoing reference strings.
func mkResource(
	t *testing.T, address, arn string, refs ...string,
) composerimported.ImportedResource {
	t.Helper()
	attrs := map[string]any{}
	for i, ref := range refs {
		// Mix scalar, slice, and nested-map carriers so the test
		// exercises walkStrings's three branches.
		switch i % 3 {
		case 0:
			attrs["role_arn_"+itoa(i)] = ref
		case 1:
			attrs["subnet_ids_"+itoa(i)] = []any{ref}
		case 2:
			attrs["nested_"+itoa(i)] = map[string]any{"target": ref}
		}
	}
	raw, err := json.Marshal(attrs)
	require.NoError(t, err)

	r := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Address: address,
			Type:    "aws_dummy",
		},
		Attrs: raw,
	}
	if arn != "" {
		r.Identity.NativeIDs = map[string]string{"arn": arn}
		r.Identity.ImportID = arn
	}
	return r
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var out []byte
	for i > 0 {
		out = append([]byte{digits[i%10]}, out...)
		i /= 10
	}
	return string(out)
}

func addresses(rs []composerimported.ImportedResource) []string {
	if rs == nil {
		return nil
	}
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Identity.Address
	}
	return out
}

func TestDependencyClosure(t *testing.T) {
	t.Parallel()

	const (
		arnA = "arn:aws:iam::123:role/A"
		arnB = "arn:aws:iam::123:role/B"
		arnC = "arn:aws:iam::123:role/C"
		arnD = "arn:aws:iam::123:role/D"
	)

	type setup struct {
		picked []composerimported.ImportedResource
		all    []composerimported.ImportedResource
	}

	tests := []struct {
		name string
		give setup
		// wantAddresses is the expected sorted list of Identity.Address
		// values in the returned closure.
		wantAddresses []string
	}{
		{
			name:          "empty picked yields empty closure",
			give:          setup{},
			wantAddresses: nil,
		},
		{
			name: "picked with no deps returns just picked, sorted",
			give: setup{
				picked: []composerimported.ImportedResource{
					mkResource(t, "aws_dummy.b", arnB),
					mkResource(t, "aws_dummy.a", arnA),
				},
				all: []composerimported.ImportedResource{
					mkResource(t, "aws_dummy.a", arnA),
					mkResource(t, "aws_dummy.b", arnB),
				},
			},
			wantAddresses: []string{"aws_dummy.a", "aws_dummy.b"},
		},
		{
			name: "single ARN reference pulls in the referenced resource",
			give: func() setup {
				a := mkResource(t, "aws_dummy.a", arnA, arnB)
				b := mkResource(t, "aws_dummy.b", arnB)
				return setup{
					picked: []composerimported.ImportedResource{a},
					all:    []composerimported.ImportedResource{a, b},
				}
			}(),
			wantAddresses: []string{"aws_dummy.a", "aws_dummy.b"},
		},
		{
			name: "transitive chain A -> B -> C is fully closed",
			give: func() setup {
				a := mkResource(t, "aws_dummy.a", arnA, arnB)
				b := mkResource(t, "aws_dummy.b", arnB, arnC)
				c := mkResource(t, "aws_dummy.c", arnC)
				return setup{
					picked: []composerimported.ImportedResource{a},
					all:    []composerimported.ImportedResource{a, b, c},
				}
			}(),
			wantAddresses: []string{"aws_dummy.a", "aws_dummy.b", "aws_dummy.c"},
		},
		{
			name: "cycle A -> B -> A terminates without infinite loop",
			give: func() setup {
				a := mkResource(t, "aws_dummy.a", arnA, arnB)
				b := mkResource(t, "aws_dummy.b", arnB, arnA)
				return setup{
					picked: []composerimported.ImportedResource{a},
					all:    []composerimported.ImportedResource{a, b},
				}
			}(),
			wantAddresses: []string{"aws_dummy.a", "aws_dummy.b"},
		},
		{
			name: "reference to resource not in all is ignored",
			give: func() setup {
				// A references arnB, but B is missing from `all`.
				a := mkResource(t, "aws_dummy.a", arnA, arnB)
				return setup{
					picked: []composerimported.ImportedResource{a},
					all:    []composerimported.ImportedResource{a},
				}
			}(),
			wantAddresses: []string{"aws_dummy.a"},
		},
		{
			name: "multiple picked elements with overlapping deps are deduped",
			give: func() setup {
				a := mkResource(t, "aws_dummy.a", arnA, arnC)
				b := mkResource(t, "aws_dummy.b", arnB, arnC)
				c := mkResource(t, "aws_dummy.c", arnC)
				return setup{
					picked: []composerimported.ImportedResource{a, b},
					all:    []composerimported.ImportedResource{a, b, c},
				}
			}(),
			wantAddresses: []string{"aws_dummy.a", "aws_dummy.b", "aws_dummy.c"},
		},
		{
			name: "picked resource not in all is retained at head of closure",
			give: func() setup {
				// A is picked but only present in `picked`. Its
				// dependency B is in `all`.
				a := mkResource(t, "aws_dummy.a", arnA, arnB)
				b := mkResource(t, "aws_dummy.b", arnB)
				return setup{
					picked: []composerimported.ImportedResource{a},
					all:    []composerimported.ImportedResource{b},
				}
			}(),
			wantAddresses: []string{"aws_dummy.a", "aws_dummy.b"},
		},
		{
			name: "diamond A -> {B,C} -> D is closed without duplicates",
			give: func() setup {
				a := mkResource(t, "aws_dummy.a", arnA, arnB, arnC)
				b := mkResource(t, "aws_dummy.b", arnB, arnD)
				c := mkResource(t, "aws_dummy.c", arnC, arnD)
				d := mkResource(t, "aws_dummy.d", arnD)
				return setup{
					picked: []composerimported.ImportedResource{a},
					all:    []composerimported.ImportedResource{a, b, c, d},
				}
			}(),
			wantAddresses: []string{
				"aws_dummy.a", "aws_dummy.b", "aws_dummy.c", "aws_dummy.d",
			},
		},
		{
			name: "malformed Attrs is tolerated and yields no edges",
			give: func() setup {
				a := composerimported.ImportedResource{
					Identity: composerimported.ResourceIdentity{
						Address: "aws_dummy.a",
						Type:    "aws_dummy",
					},
					Attrs: json.RawMessage(`{not valid json`),
				}
				return setup{
					picked: []composerimported.ImportedResource{a},
					all:    []composerimported.ImportedResource{a},
				}
			}(),
			wantAddresses: []string{"aws_dummy.a"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := closure.DependencyClosure(tc.give.picked, tc.give.all)
			assert.Equal(t, tc.wantAddresses, addresses(got))
		})
	}
}

// TestDependencyClosure_MatchByAddress verifies that a reference
// pointing at a resource's Address (not just its ARN/self-link) is
// resolved. This is the path used by Layer-1 typed Attrs that record
// cross-references as Terraform addresses rather than cloud identifiers.
func TestDependencyClosure_MatchByAddress(t *testing.T) {
	t.Parallel()

	b := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Address: "aws_dummy.b",
			Type:    "aws_dummy",
		},
	}
	// A references B by Address.
	aAttrs, err := json.Marshal(map[string]any{"depends_on": "aws_dummy.b"})
	require.NoError(t, err)
	a := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Address: "aws_dummy.a",
			Type:    "aws_dummy",
		},
		Attrs: aAttrs,
	}

	got := closure.DependencyClosure(
		[]composerimported.ImportedResource{a},
		[]composerimported.ImportedResource{a, b},
	)
	assert.Equal(t, []string{"aws_dummy.a", "aws_dummy.b"}, addresses(got))
}

// TestDependencyClosure_MatchBySelfLink verifies the GCP path: a
// reference recorded as a self_link in NativeIDs is resolved.
func TestDependencyClosure_MatchBySelfLink(t *testing.T) {
	t.Parallel()

	const selfLink = "https://www.googleapis.com/compute/v1/projects/p/regions/r/subnetworks/s"

	subnet := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Address: "google_compute_subnetwork.s",
			Type:    "google_compute_subnetwork",
			NativeIDs: map[string]string{
				"self_link": selfLink,
			},
		},
	}
	consumerAttrs, err := json.Marshal(map[string]any{"subnetwork": selfLink})
	require.NoError(t, err)
	consumer := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Address: "google_compute_instance.c",
			Type:    "google_compute_instance",
		},
		Attrs: consumerAttrs,
	}

	got := closure.DependencyClosure(
		[]composerimported.ImportedResource{consumer},
		[]composerimported.ImportedResource{consumer, subnet},
	)
	assert.Equal(t,
		[]string{"google_compute_instance.c", "google_compute_subnetwork.s"},
		addresses(got),
	)
}
