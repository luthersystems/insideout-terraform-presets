package awsdiscover

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// KMS key PostDiscover follow-up (#cust3 item 1).
//
// AWS-managed KMS keys (KeyManager == "AWS", e.g. the per-region ACM /
// RDS / DynamoDB default keys) cannot be adopted into customer Terraform
// state: the AWS provider's read path calls kms:GetKeyRotationStatus,
// which AWS-managed keys deny, so `terraform plan -generate-config-out`
// produces no body and the key is silently dropped as no_generated_config.
//
// imported.UnimportableReason already classifies a key as un-importable
// when Identity.NativeIDs["key_manager"] == "AWS" (#709). The gap: the
// Cloud Control AWS::KMS::Key schema does NOT expose KeyManager among its
// read-only properties, so the discoverer's NativeIDsFromProperties
// extractor never sees it and the classifier never fires. The
// hand-rolled AttributeEnricher path COULD surface it, but the
// reverse-import / genconfig dry-run never runs EnrichAttributes — so the
// discriminator must be resolved at DISCOVER time.
//
// kmsKeyPostDiscover issues one kms:DescribeKey per discovered key and
// stamps KeyManager (+ KeyState, useful context) onto NativeIDs so the
// shared classifier excludes AWS-managed keys into unsupported.json
// rather than letting them fall through to a generic orphan drop. The
// identifier the discoverer stamps as the import ID / NativeIDs["arn"]
// is the bare KeyId UUID / key ARN — either resolves the key for
// DescribeKey.

// kmsKeyDescriber is the narrow subset of the KMS API the PostDiscover
// hook issues. Real *kms.Client and in-test fakes satisfy it; the
// production hook constructs the real client per region.
type kmsKeyDescriber interface {
	DescribeKey(ctx context.Context, in *kms.DescribeKeyInput, opts ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

// newKMSKeyDescriber is the production factory; tests swap it (or call
// kmsKeyPostDiscoverWithClient directly) to inject a fake.
var newKMSKeyDescriber = func(awsCfg aws.Config, region string) kmsKeyDescriber {
	return kms.NewFromConfig(awsCfg, func(o *kms.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// kmsKeyPostDiscover is the cloudControlConfig.PostDiscover hook for
// aws_kms_key. It resolves KeyManager via DescribeKey and stamps it onto
// Identity.NativeIDs["key_manager"] so imported.UnimportableReason can
// classify AWS-managed keys. Soft-fails (returns an error the discoverer
// logs) when the key id is unresolvable or DescribeKey fails — a
// customer-managed key with no key_manager set is still treated as
// importable, the same posture as the genconfig prune backstop (#708).
func kmsKeyPostDiscover(ctx context.Context, awsCfg aws.Config, region string, ir *imported.ImportedResource) error {
	return kmsKeyPostDiscoverWithClient(ctx, newKMSKeyDescriber(awsCfg, region), ir)
}

func kmsKeyPostDiscoverWithClient(ctx context.Context, client kmsKeyDescriber, ir *imported.ImportedResource) error {
	if ir == nil {
		return nil
	}
	keyID := kmsKeyIDForDescribe(&ir.Identity)
	if keyID == "" {
		return fmt.Errorf("kms_key: cannot derive key id from Identity (Address=%q ImportID=%q)",
			ir.Identity.Address, ir.Identity.ImportID)
	}
	out, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		return fmt.Errorf("kms_key %q: DescribeKey: %w", keyID, err)
	}
	if out == nil || out.KeyMetadata == nil {
		return fmt.Errorf("kms_key %q: DescribeKey returned no key metadata", keyID)
	}
	if ir.Identity.NativeIDs == nil {
		ir.Identity.NativeIDs = map[string]string{}
	}
	if km := string(out.KeyMetadata.KeyManager); km != "" {
		ir.Identity.NativeIDs["key_manager"] = km
	}
	if ks := string(out.KeyMetadata.KeyState); ks != "" {
		ir.Identity.NativeIDs["key_state"] = ks
	}
	return nil
}

// kmsKeyIDForDescribe resolves a DescribeKey-acceptable identifier from
// the identity the aws_kms_key discoverer populates. NativeIDs["arn"] is
// the most specific (region-qualified) form; ImportID / NameHint carry
// the bare KeyId UUID — DescribeKey accepts either.
func kmsKeyIDForDescribe(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if arn := id.NativeIDs["arn"]; arn != "" {
		return arn
	}
	if id.ImportID != "" {
		return id.ImportID
	}
	if id.NativeIDs["name"] != "" {
		return id.NativeIDs["name"]
	}
	return id.NameHint
}
