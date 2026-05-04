// Storage / secret-management AWS service inspectors: S3, Secrets
// Manager, KMS, Backup, SQS.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go (s3:567,
// kms:580, secretsmanager:599, backup:972) plus the helpers in
// aws_metrics.go (filterS3BucketsByProjectTag:1233,
// filterKMSAliasesByProjectTag:198,
// kmsKeyOwnedByProject:174, filterBackupVaultsByProjectTag:845).
//
// S3 is the outlier among tag-lookup services: GetBucketTagging on an
// untagged bucket returns NoSuchTagSet *as an error*, and cross-region/
// cross-account buckets commonly return AccessDenied or
// PermanentRedirect. All of those are treated as "bucket is not ours"
// (fail-closed) and log-skipped.

package aws

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	smithy "github.com/aws/smithy-go"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// --- S3 ---

// s3BucketsClient is the subset of the s3 SDK used by the bucket filter
// helper. Mirrors the InsideOut backend's s3BucketsClient (aws_metrics.go:1173).
type s3BucketsClient interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketTagging(ctx context.Context, params *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error)
}

// s3TaggingSkipCodes are the SDK error codes returned by GetBucketTagging
// that the filter should tolerate by excluding the bucket (fail-closed)
// rather than aborting the whole pass.
//
// Mirrors the InsideOut backend's s3TaggingSkipCodes (aws_metrics.go:1194).
var s3TaggingSkipCodes = map[string]struct{}{
	"NoSuchTagSet":       {},
	"AccessDenied":       {},
	"AllAccessDisabled":  {},
	"PermanentRedirect":  {},
	"BucketRegionError":  {},
	"AuthorizationError": {},
}

func isS3TaggingSkip(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		if _, ok := s3TaggingSkipCodes[ae.ErrorCode()]; ok {
			return true
		}
	}
	return false
}

func inspectS3(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-buckets":
		return filterS3BucketsByProjectTag(ctx, s3.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("s3")
	default:
		return nil, unsupportedActionError("s3", action)
	}
}

// filterS3BucketsByProjectTag lists buckets and, when project!="",
// fans out GetBucketTagging per bucket. Tolerates the pile of "this
// bucket is not addressable from your creds" error codes by treating
// them as "not ours" (see s3TaggingSkipCodes); other errors abort so
// callers don't silently get a partial scan.
//
// Mirrors the InsideOut backend's filterS3BucketsByProjectTag (aws_metrics.go:1233).
func filterS3BucketsByProjectTag(ctx context.Context, client s3BucketsClient, project string) ([]s3types.Bucket, error) {
	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("s3 ListBuckets: %w", err)
	}
	if project == "" {
		return nilSliceToEmpty(out.Buckets), nil
	}
	matched := make([]s3types.Bucket, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		name := aws.ToString(b.Name)
		if name == "" {
			continue
		}
		tagsOut, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(name)})
		if err != nil {
			if isS3TaggingSkip(err) {
				log.Printf("[s3 GetBucketTagging] skip bucket=%s: %v", name, err)
				continue
			}
			return nil, fmt.Errorf("s3 GetBucketTagging bucket=%s: %w", name, err)
		}
		for _, t := range tagsOut.TagSet {
			if aws.ToString(t.Key) == "Project" && aws.ToString(t.Value) == project {
				matched = append(matched, b)
				break
			}
		}
	}
	return matched, nil
}

// --- Secrets Manager ---

func inspectSecretsManager(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := secretsmanager.NewFromConfig(cfg)
	project := filter.Project(filters)

	switch action {
	case "list-secrets":
		input := &secretsmanager.ListSecretsInput{}
		if project != "" {
			// SecretsManager supports server-side tag filtering on both
			// key and value — the only AWS service in this dispatcher
			// that exposes a Project=<value> equality filter natively.
			input.Filters = []smtypes.Filter{
				{Key: smtypes.FilterNameStringTypeTagKey, Values: []string{"Project"}},
				{Key: smtypes.FilterNameStringTypeTagValue, Values: []string{project}},
			}
		}
		out, err := client.ListSecrets(ctx, input)
		if err != nil {
			return nil, err
		}
		return nilSliceToEmpty(out.SecretList), nil
	case "get-metrics":
		return metricsRouted("secretsmanager")
	default:
		return nil, unsupportedActionError("secretsmanager", action)
	}
}

// --- KMS ---

// kmsKeysClient is the subset of the kms SDK used by the alias-filter
// helper. Mirrors the InsideOut backend's kmsKeysClient (aws_metrics.go:151).
type kmsKeysClient interface {
	ListKeys(ctx context.Context, params *kms.ListKeysInput, optFns ...func(*kms.Options)) (*kms.ListKeysOutput, error)
	ListAliases(ctx context.Context, params *kms.ListAliasesInput, optFns ...func(*kms.Options)) (*kms.ListAliasesOutput, error)
	DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	GetKeyRotationStatus(ctx context.Context, params *kms.GetKeyRotationStatusInput, optFns ...func(*kms.Options)) (*kms.GetKeyRotationStatusOutput, error)
	ListResourceTags(ctx context.Context, params *kms.ListResourceTagsInput, optFns ...func(*kms.Options)) (*kms.ListResourceTagsOutput, error)
}

func inspectKMS(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-keys", "list-aliases":
		// Both actions return the project-owned alias list keyed by
		// target key id. ListAliases is the useful view (aliases carry
		// names a human recognises); raw key ids only mean something
		// when cross-referenced against an alias. Pre-migration the two
		// actions diverged; consolidating keeps the contract sane.
		return filterKMSAliasesByProjectTag(ctx, kms.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("kms")
	default:
		return nil, unsupportedActionError("kms", action)
	}
}

// hasProjectTagKMS checks a KMS tag slice for Project=<project>. KMS is
// the outlier — the SDK uses TagKey/TagValue field names, not the
// Key/Value pattern every other service uses. Wrapping here keeps that
// weirdness contained.
func hasProjectTagKMS(tags []kmstypes.Tag, project string) bool {
	for _, t := range tags {
		if aws.ToString(t.TagKey) == "Project" && aws.ToString(t.TagValue) == project {
			return true
		}
	}
	return false
}

// kmsKeyOwnedByProject returns (matched, skip, err): true when the key
// is customer-managed AND tagged Project=<project>. AWS-managed keys
// can't carry our Project tag so they're rejected. DescribeKey or
// ListResourceTags errors return (false, true, err) — caller log+skip.
// project=="" short-circuits to true (demo-session fallback).
//
// Mirrors the InsideOut backend's kmsKeyOwnedByProject (aws_metrics.go:174).
func kmsKeyOwnedByProject(ctx context.Context, client kmsKeysClient, keyID, project string) (matched bool, skip bool, err error) {
	if project == "" {
		return true, false, nil
	}
	descOut, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		return false, true, err
	}
	if descOut.KeyMetadata == nil || descOut.KeyMetadata.KeyManager != kmstypes.KeyManagerTypeCustomer {
		return false, false, nil
	}
	tagsOut, err := client.ListResourceTags(ctx, &kms.ListResourceTagsInput{KeyId: aws.String(keyID)})
	if err != nil {
		return false, true, err
	}
	return hasProjectTagKMS(tagsOut.Tags, project), false, nil
}

// filterKMSAliasesByProjectTag paginates ListAliases and keeps only
// those whose TargetKeyId resolves to a customer-managed key tagged
// Project=<project>. Memoises per-key results — multiple aliases may
// target the same key; re-issuing DescribeKey + ListResourceTags per
// alias is wasteful and worsens Cognito-style throttling.
//
// Mirrors the InsideOut backend's filterKMSAliasesByProjectTag (aws_metrics.go:198).
func filterKMSAliasesByProjectTag(ctx context.Context, client kmsKeysClient, project string) ([]kmstypes.AliasListEntry, error) {
	all := []kmstypes.AliasListEntry{}
	paginator := kms.NewListAliasesPaginator(client, &kms.ListAliasesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("kms ListAliases: %w", err)
		}
		all = append(all, page.Aliases...)
	}
	if project == "" {
		return all, nil
	}
	keyOwned := make(map[string]bool)
	matched := make([]kmstypes.AliasListEntry, 0, len(all))
	for _, alias := range all {
		keyID := aws.ToString(alias.TargetKeyId)
		if keyID == "" {
			// AWS-reserved aliases (e.g. alias/aws/*) without a target
			// key id — not ours.
			continue
		}
		owned, cached := keyOwned[keyID]
		if !cached {
			ownedNow, skip, err := kmsKeyOwnedByProject(ctx, client, keyID, project)
			if err != nil {
				if skip {
					log.Printf("[kms alias-filter] skip key=%s: %v", keyID, err)
					keyOwned[keyID] = false
					continue
				}
				return nil, fmt.Errorf("kms alias-filter key=%s: %w", keyID, err)
			}
			keyOwned[keyID] = ownedNow
			owned = ownedNow
		}
		if owned {
			matched = append(matched, alias)
		}
	}
	return matched, nil
}

// --- Backup ---

// backupVaultsClient is the subset of the backup SDK used by the vault
// filter helper. Mirrors the InsideOut backend's backupVaultsClient
// (aws_metrics.go:834).
type backupVaultsClient interface {
	ListBackupVaults(ctx context.Context, params *backup.ListBackupVaultsInput, optFns ...func(*backup.Options)) (*backup.ListBackupVaultsOutput, error)
	ListTags(ctx context.Context, params *backup.ListTagsInput, optFns ...func(*backup.Options)) (*backup.ListTagsOutput, error)
}

func inspectBackup(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-backup-vaults":
		return filterBackupVaultsByProjectTag(ctx, backup.NewFromConfig(cfg), project)
	default:
		return nil, unsupportedActionError("backup", action)
	}
}

// filterBackupVaultsByProjectTag paginates ListBackupVaults and, when
// project!="", fans out ListTags(BackupVaultArn) keeping only vaults
// tagged Project=<project>. Per-vault errors log+skip; ListBackupVaults
// errors abort.
//
// Mirrors the InsideOut backend's filterBackupVaultsByProjectTag (aws_metrics.go:845).
func filterBackupVaultsByProjectTag(ctx context.Context, client backupVaultsClient, project string) ([]backuptypes.BackupVaultListMember, error) {
	all := []backuptypes.BackupVaultListMember{}
	paginator := backup.NewListBackupVaultsPaginator(client, &backup.ListBackupVaultsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("backup ListBackupVaults: %w", err)
		}
		all = append(all, page.BackupVaultList...)
	}
	if project == "" {
		return all, nil
	}
	matched := make([]backuptypes.BackupVaultListMember, 0, len(all))
	for _, v := range all {
		arn := aws.ToString(v.BackupVaultArn)
		tagsOut, err := client.ListTags(ctx, &backup.ListTagsInput{ResourceArn: aws.String(arn)})
		if err != nil {
			log.Printf("[backup ListTags] skip arn=%s: %v", arn, err)
			continue
		}
		if tagsOut.Tags["Project"] == project {
			matched = append(matched, v)
		}
	}
	return matched, nil
}

// --- SQS ---

func inspectSQS(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := sqs.NewFromConfig(cfg)
	project := filter.Project(filters)

	switch action {
	case "list-queues":
		input := &sqs.ListQueuesInput{}
		// SQS supports server-side queue-name prefix filtering. Preset
		// convention is to name SQS queues with the project as a prefix
		// so this is the cheapest scope filter we have on this service.
		if project != "" {
			input.QueueNamePrefix = &project
		}
		out, err := client.ListQueues(ctx, input)
		if err != nil {
			return nil, err
		}
		return nilSliceToEmpty(out.QueueUrls), nil
	case "get-metrics":
		return metricsRouted("sqs")
	default:
		return nil, unsupportedActionError("sqs", action)
	}
}
