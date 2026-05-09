package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	dbInstanceTFType    = "aws_db_instance"
	dbInstanceAssetType = "rds:db"
	dbInstanceSlug      = "db_instance"
)

// dbInstanceClient is the narrow subset of the RDS SDK the
// db_instance discoverer uses. Tags ride inline on each
// rdstypes.DBInstance.TagList — no per-resource ListTagsForResource
// fetch needed.
type dbInstanceClient interface {
	DescribeDBInstances(ctx context.Context, in *rds.DescribeDBInstancesInput, opts ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error)
}

type dbInstanceDiscoverer struct {
	new func(region string) dbInstanceClient
}

func newDBInstanceDiscoverer(cfg aws.Config) Discoverer {
	return &dbInstanceDiscoverer{new: func(region string) dbInstanceClient {
		return rds.NewFromConfig(cfg, func(o *rds.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *dbInstanceDiscoverer) ResourceType() string { return dbInstanceTFType }

// Discover paginates DescribeDBInstances and filters by
// DBInstanceIdentifier-prefix matching args.Project. RDS does NOT
// support `tag:Key` filters on DescribeDBInstances (the input takes
// `Filters` shaped for DB-engine/DB-cluster-id selection only — tag
// filtering must be applied client-side), so we fall back to the
// prefix convention shared with the rest of Bundle 4. Tags ride inline
// on each instance's TagList field; no per-resource
// ListTagsForResource round-trip is needed.
//
// Skip-list: instances in DBInstanceStatus="deleting" or "deleted"
// are tombstones — RDS keeps them visible for ~1 hour after deletion,
// terraform import rejects them. Same shape as the NAT-gateway
// State=deleted/deleting skip in #321.
//
// Multi-region (#291): outer loop walks args.Regions building a
// per-region SDK client.
//
// Import ID for aws_db_instance is the bare DBInstanceIdentifier
// (e.g. io-yqoemaqoxiqu-prod-...-rds0).
func (d *dbInstanceDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(dbInstanceSlug, region)
		regionCount := 0
		client := d.new(region)

		var instances []rdstypes.DBInstance
		input := &rds.DescribeDBInstancesInput{}
		for {
			page, err := client.DescribeDBInstances(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(dbInstanceSlug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("DescribeDBInstances (region=%s): %w", region, err)
			}
			instances = append(instances, page.DBInstances...)
			if page.Marker == nil || *page.Marker == "" {
				break
			}
			input.Marker = page.Marker
		}

		// Sort by DBInstanceIdentifier so the emitted manifest is
		// deterministic across runs.
		sort.Slice(instances, func(i, j int) bool {
			return aws.ToString(instances[i].DBInstanceIdentifier) < aws.ToString(instances[j].DBInstanceIdentifier)
		})

		for i := range instances {
			db := &instances[i]
			id := aws.ToString(db.DBInstanceIdentifier)
			if args.Project != "" && !strings.HasPrefix(id, args.Project) {
				continue
			}
			// Skip tombstones — see header comment.
			status := aws.ToString(db.DBInstanceStatus)
			if status == "deleting" || status == "deleted" {
				continue
			}
			tags := rdsTagsToMap(db.TagList)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			native := map[string]string{
				"db_instance_id": id,
				"arn":            aws.ToString(db.DBInstanceArn),
				"engine":         aws.ToString(db.Engine),
			}
			if db.Endpoint != nil {
				if v := aws.ToString(db.Endpoint.Address); v != "" {
					native["endpoint_address"] = v
				}
			}
			if db.DBSubnetGroup != nil {
				if v := aws.ToString(db.DBSubnetGroup.DBSubnetGroupName); v != "" {
					native["db_subnet_group_name"] = v
				}
			}
			out = append(out, makeImportedResource(
				book,
				dbInstanceTFType,
				id,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(dbInstanceSlug, region, dbInstanceTFType, id)
			regionCount++
		}
		args.Emitter.ServiceFinish(dbInstanceSlug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a DB instance by DBInstanceIdentifier (bare
// name). Issues a single DescribeDBInstances call to verify
// existence. ARN inputs are not accepted — the RDS API only takes the
// bare identifier on this endpoint, and the dep-chase loop already
// hands us the bare identifier in the import {} block. Tags are not
// fetched — dep-chase only needs address/import-ID resolution.
func (d *dbInstanceDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := dbInstanceNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{DBInstanceIdentifier: aws.String(name)})
	if err != nil {
		var notFound *rdstypes.DBInstanceNotFoundFault
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_db_instance %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeDBInstances: %w", err)
	}
	if len(out.DBInstances) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_db_instance %q: %w", name, ErrNotFound)
	}
	db := &out.DBInstances[0]
	native := map[string]string{
		"db_instance_id": name,
		"arn":            aws.ToString(db.DBInstanceArn),
		"engine":         aws.ToString(db.Engine),
	}
	if db.Endpoint != nil {
		if v := aws.ToString(db.Endpoint.Address); v != "" {
			native["endpoint_address"] = v
		}
	}
	if db.DBSubnetGroup != nil {
		if v := aws.ToString(db.DBSubnetGroup.DBSubnetGroupName); v != "" {
			native["db_subnet_group_name"] = v
		}
	}
	return makeImportedResource(
		addressBook{},
		dbInstanceTFType,
		name,
		name,
		region,
		accountID,
		native,
		nil,
	), nil
}

// dbInstanceNameFromID validates a DBInstanceIdentifier shape. Empty
// or whitespace-laden inputs return ErrNotSupported so dep-chase
// routes them to the unresolvable bucket. The DBInstanceIdentifier
// rules (1-63 alphanumeric/hyphen) are not re-validated here — the
// API returns DBInstanceNotFoundFault for malformed names anyway.
func dbInstanceNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("db_instance: empty id: %w", ErrNotSupported)
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("db_instance: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// rdsTagsToMap converts the RDS SDK's []Tag slice into a string-keyed
// map. Returns a non-nil empty map (not nil) so the filter+persist
// contract holds: nil = "didn't fetch", empty = "fetched, no tags".
// Shared across all RDS discoverers in this package.
func rdsTagsToMap(in []rdstypes.Tag) map[string]string {
	out := make(map[string]string, len(in))
	for _, t := range in {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}
