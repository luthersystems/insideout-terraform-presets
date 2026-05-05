package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// kmsClient is the narrow subset of the KMS SDK the discoverer uses.
// We probe via aliases (KMS keys are typically referenced by alias in
// Terraform) and resolve to KeyId via DescribeKey.
type kmsClient interface {
	ListAliases(ctx context.Context, in *kms.ListAliasesInput, opts ...func(*kms.Options)) (*kms.ListAliasesOutput, error)
	DescribeKey(ctx context.Context, in *kms.DescribeKeyInput, opts ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

type kmsDiscoverer struct {
	new func() kmsClient
}

func newKMSDiscoverer(cfg aws.Config) Discoverer {
	return &kmsDiscoverer{new: func() kmsClient { return kms.NewFromConfig(cfg) }}
}

func (d *kmsDiscoverer) ResourceType() string { return "aws_kms_key" }

// Discover walks ListAliases and emits one aws_kms_key per alias whose
// name contains the project string. KMS aliases are the conventional
// human-readable handle for keys — Terraform stacks set
// `alias = "alias/<project>-<purpose>"` on aws_kms_alias resources, so
// the alias name carries the project association even when the
// underlying key is untagged.
//
// Untagged customer-managed keys whose aliases do not contain the
// project name are skipped. Operators with "naked" keys (no alias)
// must hand the key UUID to discover via the --resource-types path.
//
// Import ID for aws_kms_key is the key UUID (from the alias's
// TargetKeyId).
func (d *kmsDiscoverer) Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	client := d.new()

	type key struct {
		uuid  string
		arn   string
		alias string
	}
	var keys []key

	paginator := kms.NewListAliasesPaginator(client, &kms.ListAliasesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("ListAliases: %w", err)
		}
		for _, a := range page.Aliases {
			alias := aws.ToString(a.AliasName)
			target := aws.ToString(a.TargetKeyId)
			if target == "" {
				// AWS-managed alias with no customer-managed key behind
				// it; skip — cannot import.
				continue
			}
			if project != "" && !strings.Contains(alias, project) {
				continue
			}
			// Skip AWS-managed aliases (e.g. "alias/aws/...") that may
			// happen to contain the project name as a substring.
			if strings.HasPrefix(alias, "alias/aws/") {
				continue
			}
			keys = append(keys, key{
				uuid:  target,
				arn:   aws.ToString(a.AliasArn),
				alias: alias,
			})
		}
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i].alias < keys[j].alias })

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(keys))
	for _, k := range keys {
		var arn string
		if accountID != "" && region != "" {
			arn = fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", region, accountID, k.uuid)
		}
		// NameHint is the alias (without "alias/" prefix) to keep the
		// generated Terraform address human-readable.
		nameHint := strings.TrimPrefix(k.alias, "alias/")
		imps = append(imps, makeImportedResource(
			book,
			"aws_kms_key",
			nameHint,
			k.uuid,
			region,
			accountID,
			map[string]string{"arn": arn, "alias": k.alias},
		))
	}
	return imps, nil
}

// DiscoverByID resolves a KMS key by ARN
// (arn:aws:kms:<region>:<account>:key/<uuid>), bare key UUID, or alias
// ARN/name. Issues a single DescribeKey call to verify existence and
// resolve aliases.
func (d *kmsDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	probe, err := kmsKeyIDFromInput(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new()
	out, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(probe)})
	if err != nil {
		var notFound *kmstypes.NotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_kms_key %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeKey: %w", err)
	}
	if out.KeyMetadata == nil {
		return imported.ImportedResource{}, fmt.Errorf("DescribeKey returned no metadata for %q", id)
	}
	uuid := aws.ToString(out.KeyMetadata.KeyId)
	arn := aws.ToString(out.KeyMetadata.Arn)
	if arn == "" && accountID != "" && region != "" {
		arn = fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", region, accountID, uuid)
	}
	return makeImportedResource(
		addressBook{},
		"aws_kms_key",
		uuid,
		uuid,
		region,
		accountID,
		map[string]string{"arn": arn},
	), nil
}

// kmsKeyIDFromInput normalizes any of {key UUID, key ARN, alias name,
// alias ARN} to a probe value DescribeKey accepts (it accepts all four
// natively, so this function is mainly a validation gate). Returns
// ErrNotSupported for inputs that obviously can't be a KMS reference.
func kmsKeyIDFromInput(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("kms: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("kms: parse arn: %w", err)
		}
		if parsed.Service != "kms" {
			return "", fmt.Errorf("kms: not a kms arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// kms ARN resource is "key/<uuid>" or "alias/<name>".
		if !strings.HasPrefix(parsed.Resource, "key/") && !strings.HasPrefix(parsed.Resource, "alias/") {
			return "", fmt.Errorf("kms: arn resource %q is not key/<uuid> or alias/<name>: %w", parsed.Resource, ErrNotSupported)
		}
		return id, nil
	}
	// Allow alias names ("alias/<name>") and bare UUIDs.
	if strings.HasPrefix(id, "alias/") {
		return id, nil
	}
	if strings.ContainsAny(id, " :") {
		return "", fmt.Errorf("kms: unrecognized id %q: %w", id, ErrNotSupported)
	}
	// Bare UUID — DescribeKey accepts this.
	return id, nil
}
