package awsdiscover

import "github.com/aws/aws-sdk-go-v2/aws"

// awsDummyConfig returns an aws.Config with no real credentials. Tests
// that build the production AWSDiscoverer just to inspect its registry
// (e.g. TestNewAWSDiscoverer_Registers5PhaseOneTypes) need *some* config
// to call NewAWSDiscoverer; they do not perform any SDK calls.
func awsDummyConfig() aws.Config { return aws.Config{Region: "us-east-1"} }
