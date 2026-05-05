package awsdiscover

import (
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// awsProviderSource is the canonical Terraform Registry source for the AWS
// provider. Hardcoded because Stage 2a does not bind a specific provider
// version — Stage 2b will set ProviderVersion from the actual terraform
// init output.
const awsProviderSource = "registry.terraform.io/hashicorp/aws"

// addressBook is the de-dup state passed to imported.GenerateAddress as the
// `exists` predicate. Each discoverer's loop seeds the book with addresses
// it has already produced so collisions within a single resource type, or
// across types in the same DiscoverTypes call, are resolved with the
// deterministic _<8hex> suffix.
type addressBook map[string]struct{}

func (b addressBook) exists(addr string) bool { _, ok := b[addr]; return ok }

func (b addressBook) add(addr string) { b[addr] = struct{}{} }

// makeImportedResource builds a Phase-2 ImportedResource from the common
// shape every AWS discoverer feeds in. typ is the Terraform type
// ("aws_sqs_queue"), name is the human-readable name (used as NameHint
// AND populated into NativeIDs["name"] so GenerateAddress's hint-precedence
// is consistent), importID is whatever the provider's import {} block
// expects, and nativeIDs lets a discoverer attach extra cloud-side IDs
// (arn, url) without mutating the merged map after construction.
//
// region and accountID are passed in by the aggregator (one STS call per
// run); the discoverer does not re-derive them.
func makeImportedResource(book addressBook, typ, name, importID, region, accountID string, nativeIDs map[string]string) imported.ImportedResource {
	id := imported.ResourceIdentity{
		Cloud:          "aws",
		Type:           typ,
		NameHint:       name,
		ProviderSource: awsProviderSource,
		ProviderConfig: "aws.imported",
		AccountID:      accountID,
		Region:         region,
		ImportID:       importID,
		NativeIDs:      mergeNativeIDs(name, nativeIDs),
	}
	addr := imported.GenerateAddress(id, book.exists)
	id.Address = addr
	book.add(addr)

	return imported.ImportedResource{
		Identity: id,
		Tier:     imported.TierImportedFlat,
		Source:   imported.SourceImporter,
	}
}

// mergeNativeIDs guarantees the discoverer's NativeIDs map carries the
// "name" key (used by GenerateAddress's pickNameHint precedence). The
// caller can pass nil for `extra` when no other IDs are available.
func mergeNativeIDs(name string, extra map[string]string) map[string]string {
	out := make(map[string]string, 1+len(extra))
	if name != "" {
		out["name"] = name
	}
	for k, v := range extra {
		if v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
