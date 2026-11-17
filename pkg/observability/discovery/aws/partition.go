package aws

import "strings"

// partitionRegionPrefixes maps a region-name prefix to its ARN partition.
// It follows the regionRegex values in the AWS SDK's partitions.json
// (internal/endpoints/awsrulesfn), which Go code outside the SDK cannot
// import. An ARN built with the wrong partition is refused by the service,
// so a table in GovCloud or China would never match its Project tag.
//
// The more specific prefixes come first: "us-gov-" and the "us-iso*"
// prefixes must win over the plain commercial "us-" regions.
var partitionRegionPrefixes = []struct {
	prefix    string
	partition string
}{
	{"us-gov-", "aws-us-gov"},
	{"us-iso-", "aws-iso"},
	{"us-isob-", "aws-iso-b"},
	{"us-isof-", "aws-iso-f"},
	{"eu-isoe-", "aws-iso-e"},
	{"eusc-", "aws-eusc"},
	{"cn-", "aws-cn"},
}

// partitionForRegion returns the ARN partition for an AWS region, for
// example "aws-us-gov" for us-gov-west-1 and "aws-cn" for cn-north-1.
// Every other region, and an empty one, is in the commercial "aws"
// partition.
func partitionForRegion(region string) string {
	for _, p := range partitionRegionPrefixes {
		if strings.HasPrefix(region, p.prefix) {
			return p.partition
		}
	}
	return "aws"
}
