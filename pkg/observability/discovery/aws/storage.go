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
	"strconv"

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
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	smithy "github.com/aws/smithy-go"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// --- S3 ---

// s3BucketsClient is the subset of the s3 SDK used by the bucket filter
// helper. Mirrors the InsideOut backend's s3BucketsClient (aws_metrics.go:1173).
//
// GetBucketVersioning enriches each returned bucket with its versioning
// state (the same fan-out pattern inspectVPCWithIGW uses for IGW
// attachments) so extractS3Config can surface aws_s3.versioning without a
// second inspector round-trip (#712, reliable#1089).
type s3BucketsClient interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketTagging(ctx context.Context, params *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error)
	GetBucketVersioning(ctx context.Context, params *s3.GetBucketVersioningInput, optFns ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
}

// bucketWithVersioning is the enriched element filterS3BucketsByProjectTag
// returns: the AWS SDK Bucket type extended with a Versioning bool computed
// via a GetBucketVersioning fan-out. ListBuckets does not carry versioning
// on the Bucket resource itself, so this wrapper documents the derived
// field explicitly. The embed flattens the SDK's Name/CreationDate at the
// top level and appends Versioning alongside — the shape extractS3Config
// reads.
//
// Versioning is a *bool so the JSON envelope OMITS the field when the
// GetBucketVersioning call failed (cross-region / access-denied buckets),
// letting extractS3Config tell "versioning genuinely off" apart from "not
// fetched" — the same presence check extractVPCConfig does for
// HasInternetGateway.
//
// Mirrors the vpcWithIGW enrichment pattern (network.go).
type bucketWithVersioning struct {
	s3types.Bucket
	Versioning *bool `json:"Versioning,omitempty"`
}

// s3VersioningSkipCodes are GetBucketVersioning error codes tolerated by
// excluding the versioning flag (the bucket is still returned) rather than
// aborting the pass. Same fail-soft set S3 GetBucketTagging tolerates:
// cross-region / cross-account buckets commonly return these.
var s3VersioningSkipCodes = map[string]struct{}{
	"AccessDenied":       {},
	"AllAccessDisabled":  {},
	"PermanentRedirect":  {},
	"BucketRegionError":  {},
	"AuthorizationError": {},
	"NoSuchBucket":       {},
}

func isS3VersioningSkip(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		if _, ok := s3VersioningSkipCodes[ae.ErrorCode()]; ok {
			return true
		}
	}
	return false
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
// Each returned bucket is then enriched with its versioning state via a
// GetBucketVersioning fan-out (see enrichS3Versioning) so extractS3Config
// can surface aws_s3.versioning (#712, reliable#1089).
//
// Mirrors the InsideOut backend's filterS3BucketsByProjectTag (aws_metrics.go:1233).
func filterS3BucketsByProjectTag(ctx context.Context, client s3BucketsClient, project string) ([]bucketWithVersioning, error) {
	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("s3 ListBuckets: %w", err)
	}
	if project == "" {
		// Demo-session fallback: skip the per-bucket GetBucketTagging
		// scope filter, but still enrich versioning so live config is
		// consistent across both paths.
		return enrichS3Versioning(ctx, client, nilSliceToEmpty(out.Buckets)), nil
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
	return enrichS3Versioning(ctx, client, matched), nil
}

// enrichS3Versioning fans out GetBucketVersioning per bucket and wraps each
// in a bucketWithVersioning. A versioning lookup failure is non-fatal — the
// bucket is still returned, just with Versioning left nil (the field is then
// omitted from the JSON envelope), mirroring inspectVPCWithIGW's non-fatal
// IGW join. Versioning is "on" only when Status==Enabled; Suspended and the
// never-configured empty status both map to false.
func enrichS3Versioning(ctx context.Context, client s3BucketsClient, buckets []s3types.Bucket) []bucketWithVersioning {
	out := make([]bucketWithVersioning, 0, len(buckets))
	for _, b := range buckets {
		wrapped := bucketWithVersioning{Bucket: b}
		name := aws.ToString(b.Name)
		if name != "" {
			vOut, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(name)})
			switch {
			case err == nil:
				enabled := vOut.Status == s3types.BucketVersioningStatusEnabled
				wrapped.Versioning = &enabled
			case isS3VersioningSkip(err):
				log.Printf("[s3 GetBucketVersioning] skip bucket=%s: %v", name, err)
			default:
				// Non-skip errors (transport, throttling) are also
				// non-fatal here: the bucket inventory + tag scope is the
				// primary signal; versioning is a best-effort enrichment.
				log.Printf("[s3 GetBucketVersioning] bucket=%s: %v (versioning omitted)", name, err)
			}
		}
		out = append(out, wrapped)
	}
	return out
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
	return inspectKMSWithClient(ctx, kms.NewFromConfig(cfg), action, filter.Project(filters))
}

// inspectKMSWithClient is the action switch of inspectKMS, split out so
// tests can pass a fake client.
func inspectKMSWithClient(ctx context.Context, client kmsKeysClient, action, project string) (any, error) {
	switch action {
	case "list-aliases":
		// The project-owned alias list keyed by target key id. Aliases
		// carry names a human recognises, so every row has an AliasName.
		return filterKMSAliasesByProjectTag(ctx, client, project)
	case "list-keys":
		// The alias rows plus a KeyOnly row for each project key that
		// has no alias. A caller asking for keys needs every key, and the
		// aws_kms drift extractor counts keys from this action (it is the
		// panel default in ComponentMetricsMapping), so the extra
		// ListKeys pass lives here (#927). list-aliases keeps its
		// alias-only rows, where a row without an AliasName would be
		// noise.
		return listProjectKMSKeys(ctx, client, project)
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
	return filterKMSAliasesByProjectTagMemo(ctx, client, project, map[string]bool{})
}

// filterKMSAliasesByProjectTagMemo is filterKMSAliasesByProjectTag with
// the per-key ownership memo passed in, so listProjectKMSKeys can reuse
// the answers for its ListKeys pass instead of describing each aliased
// key twice.
func filterKMSAliasesByProjectTagMemo(ctx context.Context, client kmsKeysClient, project string, keyOwned map[string]bool) ([]kmstypes.AliasListEntry, error) {
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

// kmsKeyEntry is one row of the kms list-keys result: a
// project alias, or (KeyOnly) a project key that has no alias at all.
// Embedding AliasListEntry keeps the alias rows' JSON exactly as it was
// before #927, so readers of AliasName / TargetKeyId see no change. A
// KeyOnly row carries only TargetKeyId (the key's id), so a reader that
// shows "AliasName or TargetKeyId" still shows something useful.
type kmsKeyEntry struct {
	kmstypes.AliasListEntry
	KeyOnly bool `json:"KeyOnly,omitempty"`
}

// kmsUnaliasedKeyCheckLimit bounds the DescribeKey + ListResourceTags
// pairs listProjectKMSKeys spends on keys the alias pass did not already
// check. ListKeys returns every key in the region, including other
// projects' keys and AWS-managed keys, and only an ownership check tells
// them apart. The aws/kms preset aliases every key it creates, so in a
// preset stack this pass checks nothing new; the bound only matters in a
// crowded shared account, where it keeps one panel load from making
// hundreds of KMS calls. It is larger than configFanOutLimit because
// these calls find our keys among other keys (stopping at ten could miss
// ours), not describe keys already known to be ours.
const kmsUnaliasedKeyCheckLimit = 50

// listProjectKMSKeys returns the project's aliases plus one KeyOnly row
// for each project key that has no alias (#927). The alias count alone
// is not a key count: two aliases can name one key, and a key can have
// no alias. extractKMSConfig counts distinct TargetKeyId values over
// these rows, which is the true number of project keys.
//
// The ListKeys pass is best effort. A ListKeys failure, or an ownership
// check failure for one key, is logged and the result degrades to the
// alias rows (the pre-#927 result). A ListAliases failure still fails
// the call, as before.
//
// With no project (the demo-session fallback) the alias list is not
// scoped either, so there is no "our keys" set to complete; the pass is
// skipped and the rows are the alias list as before.
func listProjectKMSKeys(ctx context.Context, client kmsKeysClient, project string) ([]kmsKeyEntry, error) {
	keyOwned := map[string]bool{}
	aliases, err := filterKMSAliasesByProjectTagMemo(ctx, client, project, keyOwned)
	if err != nil {
		return nil, err
	}
	out := make([]kmsKeyEntry, 0, len(aliases))
	aliased := make(map[string]struct{}, len(aliases))
	for _, a := range aliases {
		out = append(out, kmsKeyEntry{AliasListEntry: a})
		if id := aws.ToString(a.TargetKeyId); id != "" {
			aliased[id] = struct{}{}
		}
	}
	if project == "" {
		return out, nil
	}

	checks := 0
	paginator := kms.NewListKeysPaginator(client, &kms.ListKeysInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.Printf("[kms ListKeys] project=%s: %v (key count falls back to aliased keys)", project, err)
			return out, nil
		}
		for _, k := range page.Keys {
			keyID := aws.ToString(k.KeyId)
			if keyID == "" {
				continue
			}
			if _, ok := aliased[keyID]; ok {
				continue
			}
			owned, cached := keyOwned[keyID]
			if !cached {
				if checks >= kmsUnaliasedKeyCheckLimit {
					log.Printf("[kms ListKeys] project=%s: stopped after %d unaliased-key checks (key count may be low)", project, checks)
					return out, nil
				}
				checks++
				ownedNow, _, err := kmsKeyOwnedByProject(ctx, client, keyID, project)
				if err != nil {
					log.Printf("[kms ListKeys] skip key=%s: %v", keyID, err)
					keyOwned[keyID] = false
					continue
				}
				keyOwned[keyID] = ownedNow
				owned = ownedNow
			}
			if owned {
				out = append(out, kmsKeyEntry{
					AliasListEntry: kmstypes.AliasListEntry{TargetKeyId: aws.String(keyID)},
					KeyOnly:        true,
				})
			}
		}
	}
	return out, nil
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

// sqsQueuesClient is the subset of the sqs SDK the describe-queues action
// uses. Narrowed so tests can inject a fake.
type sqsQueuesClient interface {
	ListQueues(ctx context.Context, params *sqs.ListQueuesInput, optFns ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error)
	GetQueueAttributes(ctx context.Context, params *sqs.GetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}

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
	case "describe-queues":
		// The list-queues result is a bare array of URLs that callers
		// render as is, so the config read for drift (#927) is a separate
		// action that returns one object per queue.
		return describeSQSQueues(ctx, client, project)
	case "get-metrics":
		return metricsRouted("sqs")
	default:
		return nil, unsupportedActionError("sqs", action)
	}
}

// sqsQueue is one row of the describe-queues result (#927). ListQueues
// returns only URLs, so list-queues lets the design-vs-deployed card
// compare nothing but the queue count. This row also carries the two
// attributes the design config sets (aws_sqs.type and
// aws_sqs.visibilityTimeout), read with one GetQueueAttributes call per
// queue.
//
// The attribute fields are pointers so the JSON omits them when the
// GetQueueAttributes call was skipped (past configFanOutLimit) or failed;
// extractSQSConfig then tells "not fetched" apart from a real value, the
// same presence check extractS3Config does for Versioning.
type sqsQueue struct {
	QueueUrl          string `json:"QueueUrl"`
	VisibilityTimeout *int64 `json:"VisibilityTimeout,omitempty"`
	FifoQueue         *bool  `json:"FifoQueue,omitempty"`
}

// describeSQSQueues lists the project's queues the way list-queues does
// and reads the config attributes of the first configFanOutLimit of
// them. A failed GetQueueAttributes call is logged and that queue comes
// back with its URL only; it never fails the action.
func describeSQSQueues(ctx context.Context, client sqsQueuesClient, project string) ([]sqsQueue, error) {
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
	queues := make([]sqsQueue, 0, len(out.QueueUrls))
	for i, u := range out.QueueUrls {
		q := sqsQueue{QueueUrl: u}
		if i < configFanOutLimit {
			readSQSQueueAttributes(ctx, client, &q)
		}
		queues = append(queues, q)
	}
	return queues, nil
}

// readSQSQueueAttributes fills q's config fields from GetQueueAttributes.
// SQS returns attribute values as strings. FifoQueue is returned only for
// FIFO queues, so a successful call without it means a standard queue.
func readSQSQueueAttributes(ctx context.Context, client sqsQueuesClient, q *sqsQueue) {
	attrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(q.QueueUrl),
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameVisibilityTimeout,
			sqstypes.QueueAttributeNameFifoQueue,
		},
	})
	if err != nil {
		log.Printf("[sqs GetQueueAttributes] queue=%s: %v (config omitted)", q.QueueUrl, err)
		return
	}
	if v, ok := attrs.Attributes[string(sqstypes.QueueAttributeNameVisibilityTimeout)]; ok {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			q.VisibilityTimeout = &n
		}
	}
	fifo := attrs.Attributes[string(sqstypes.QueueAttributeNameFifoQueue)] == "true"
	q.FifoQueue = &fifo
}
