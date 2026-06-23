// Project-tag → EC2 filter helper.
//
// This lives here, in the AWS discovery package, rather than in
// pkg/observability/filter so that the filter package stays free of the
// AWS SDK. reliable imports filter.{Project,EnsureProject} in its thin
// observability-via-oracle proxy (reliable#2141 / #2153) and must NOT drag
// in aws-sdk-go-v2/service/ec2/types — its cmd/api Vercel function is at
// the 250 MB size limit, and every imported SDK service package carries an
// irreducible linker baseline. The only callers of ProjectTagFilter are
// the AWS discovery inspectors in this package, which already import the
// EC2 SDK, so moving it here costs nothing and keeps `filter` SDK-free.
package aws

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ProjectTagFilter returns EC2-style tag filters for the Project tag.
// Used by EC2, VPC, and other services that share the EC2 filter API.
func ProjectTagFilter(project string) []ec2types.Filter {
	if project == "" {
		return nil
	}
	return []ec2types.Filter{{
		Name:   aws.String("tag:Project"),
		Values: []string{project},
	}}
}
