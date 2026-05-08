package awsdiscover

import (
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func TestMakeImportedResource_PopulatesRequiredFields(t *testing.T) {
	t.Parallel()
	book := addressBook{}
	wantTags := map[string]string{"Project": "io-foo", "env": "prod"}
	got := makeImportedResource(book, "aws_sqs_queue", "io-foo-q", "https://sqs.us-east-1.amazonaws.com/123/io-foo-q", "us-east-1", "123",
		map[string]string{"url": "https://sqs.us-east-1.amazonaws.com/123/io-foo-q"},
		wantTags,
	)

	// Tags carrier (#291) — pin both presence and shape. A regression
	// that drops the field would surface here before any downstream
	// consumer (selector-filter, summary builder) silently breaks.
	if got.Identity.Tags == nil {
		t.Error("Tags must be populated when discoverer fetched a tag map")
	}
	if got.Identity.Tags["Project"] != "io-foo" || got.Identity.Tags["env"] != "prod" {
		t.Errorf("Tags=%v, want %v", got.Identity.Tags, wantTags)
	}

	if got.Identity.Cloud != "aws" {
		t.Errorf("Cloud=%q, want aws", got.Identity.Cloud)
	}
	if got.Identity.Type != "aws_sqs_queue" {
		t.Errorf("Type=%q, want aws_sqs_queue", got.Identity.Type)
	}
	if got.Identity.Address == "" {
		t.Error("Address must be populated by GenerateAddress")
	}
	if got.Identity.ImportID == "" {
		t.Error("ImportID must be populated")
	}
	if got.Identity.NameHint != "io-foo-q" {
		t.Errorf("NameHint=%q, want io-foo-q", got.Identity.NameHint)
	}
	if got.Identity.AccountID != "123" {
		t.Errorf("AccountID=%q, want 123", got.Identity.AccountID)
	}
	if got.Identity.Region != "us-east-1" {
		t.Errorf("Region=%q, want us-east-1", got.Identity.Region)
	}
	if got.Identity.ProviderSource != awsProviderSource {
		t.Errorf("ProviderSource=%q, want %q", got.Identity.ProviderSource, awsProviderSource)
	}
	// The composer's emitted HCL references `provider = aws.imported`
	// for every imported AWS resource (per Phase 2 design decision: see
	// pkg/composer/imported_emit.go::providerAliasFor). A mutation that
	// drops or renames this constant breaks every downstream stack.
	if got.Identity.ProviderConfig != "aws.imported" {
		t.Errorf("ProviderConfig=%q, want aws.imported", got.Identity.ProviderConfig)
	}
	if got.Identity.NativeIDs["name"] != "io-foo-q" {
		t.Errorf("NativeIDs[name]=%q, want io-foo-q", got.Identity.NativeIDs["name"])
	}
	if got.Identity.NativeIDs["url"] == "" {
		t.Error("NativeIDs[url] should be populated by extra map")
	}
	if got.Tier != imported.TierImportedFlat {
		t.Errorf("Tier=%q, want TierImportedFlat", got.Tier)
	}
	if got.Source != imported.SourceImporter {
		t.Errorf("Source=%q, want SourceImporter", got.Source)
	}
}

func TestMakeImportedResource_ResolvesAddressCollisionsWithinBatch(t *testing.T) {
	t.Parallel()
	book := addressBook{}
	a := makeImportedResource(book, "aws_sqs_queue", "io-q", "https://example/io-q-1", "us-east-1", "123", nil, nil)
	b := makeImportedResource(book, "aws_sqs_queue", "io-q", "https://example/io-q-2", "us-east-1", "123", nil, nil)

	// Identical NameHint but different ImportID → identityHash differs →
	// GenerateAddress's `_<8hex>` collision suffix should produce
	// distinct addresses.
	if a.Identity.Address == b.Identity.Address {
		t.Errorf("expected distinct addresses for distinct identities; got %q twice", a.Identity.Address)
	}
	// Pin the suffix shape: collision-resolved address == base + "_" + 8 hex.
	// A mutation that shrinks the hash to 4 hex (or shifts to a different
	// separator) survives the cardinality test alone.
	if !strings.HasPrefix(b.Identity.Address, a.Identity.Address+"_") {
		t.Errorf("collision-resolved address %q must start with the original %q + '_'", b.Identity.Address, a.Identity.Address)
	}
	suffix := b.Identity.Address[len(a.Identity.Address)+1:]
	if len(suffix) != 8 {
		t.Errorf("collision suffix = %q (len=%d), want 8 hex chars", suffix, len(suffix))
	}
	for _, r := range suffix {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("collision suffix %q contains non-hex %q", suffix, r)
			break
		}
	}
}

func TestMergeNativeIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		hint string
		in   map[string]string
		want map[string]string
	}{
		{name: "name only", hint: "io-q", in: nil, want: map[string]string{"name": "io-q"}},
		{name: "name plus url", hint: "io-q", in: map[string]string{"url": "u"}, want: map[string]string{"name": "io-q", "url": "u"}},
		{name: "drops empty values", hint: "io-q", in: map[string]string{"arn": "", "url": "u"}, want: map[string]string{"name": "io-q", "url": "u"}},
		{name: "empty everything returns nil", hint: "", in: nil, want: nil},
		{name: "empty hint with extras", hint: "", in: map[string]string{"arn": "a"}, want: map[string]string{"arn": "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mergeNativeIDs(tc.hint, tc.in)
			if len(got) != len(tc.want) {
				t.Errorf("len=%d, want %d (%v)", len(got), len(tc.want), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("got[%q]=%q, want %q", k, got[k], v)
				}
			}
		})
	}
}
