package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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
	ListResourceTags(ctx context.Context, in *kms.ListResourceTagsInput, opts ...func(*kms.Options)) (*kms.ListResourceTagsOutput, error)
}

type kmsDiscoverer struct {
	new func(region string) kmsClient
}

func newKMSDiscoverer(cfg aws.Config) Discoverer {
	return &kmsDiscoverer{new: func(region string) kmsClient {
		return kms.NewFromConfig(cfg, func(o *kms.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
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
// Multi-region (#291): outer loop walks args.Regions, building a per-
// region SDK client. Per-key ListResourceTags fetches the key tag map
// for tag-selector post-filtering and tag persistence onto Identity.Tags.
//
// Import ID for aws_kms_key is the key UUID (from the alias's
// TargetKeyId).
func (d *kmsDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "kms"
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		type key struct {
			uuid  string
			alias string
		}
		var keys []key

		paginator := kms.NewListAliasesPaginator(client, &kms.ListAliasesInput{})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListAliases (region=%s): %w", region, err)
			}
			for _, a := range page.Aliases {
				alias := aws.ToString(a.AliasName)
				target := aws.ToString(a.TargetKeyId)
				if target == "" {
					// AWS-managed alias with no customer-managed key behind
					// it; skip — cannot import.
					continue
				}
				if args.Project != "" && !strings.Contains(alias, args.Project) {
					continue
				}
				// Skip AWS-managed aliases (e.g. "alias/aws/...") that may
				// happen to contain the project name as a substring.
				if strings.HasPrefix(alias, "alias/aws/") {
					continue
				}
				keys = append(keys, key{
					uuid:  target,
					alias: alias,
				})
			}
		}

		sort.Slice(keys, func(i, j int) bool { return keys[i].alias < keys[j].alias })

		for _, k := range keys {
			tags, err := fetchKMSTags(ctx, client, k.uuid)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListResourceTags (region=%s, key=%s): %w", region, k.uuid, err)
			}
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			var arn string
			if args.AccountID != "" && region != "" {
				arn = fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", region, args.AccountID, k.uuid)
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
				args.AccountID,
				map[string]string{"arn": arn, "alias": k.alias},
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_kms_key", k.uuid)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// fetchKMSTags returns the key's tag map. KMS's ListResourceTags
// returns `Tags []TagListEntry` (TagKey + TagValue). Empty (non-nil)
// map for "fetched, but the key has no tags."
func fetchKMSTags(ctx context.Context, client kmsClient, keyID string) (map[string]string, error) {
	tags := map[string]string{}
	input := &kms.ListResourceTagsInput{KeyId: aws.String(keyID)}
	for {
		out, err := client.ListResourceTags(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, t := range out.Tags {
			tags[aws.ToString(t.TagKey)] = aws.ToString(t.TagValue)
		}
		if !out.Truncated {
			return tags, nil
		}
		input.Marker = out.NextMarker
	}
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
	client := d.new(region)
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
		nil,
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
