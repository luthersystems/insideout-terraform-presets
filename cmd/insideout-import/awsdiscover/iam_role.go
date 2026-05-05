package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// iamRoleClient is the narrow subset of the IAM SDK the role discoverer uses.
type iamRoleClient interface {
	ListRoles(ctx context.Context, in *iam.ListRolesInput, opts ...func(*iam.Options)) (*iam.ListRolesOutput, error)
	GetRole(ctx context.Context, in *iam.GetRoleInput, opts ...func(*iam.Options)) (*iam.GetRoleOutput, error)
}

type iamRoleDiscoverer struct {
	new func() iamRoleClient
}

func newIAMRoleDiscoverer(cfg aws.Config) Discoverer {
	return &iamRoleDiscoverer{new: func() iamRoleClient { return iam.NewFromConfig(cfg) }}
}

func (d *iamRoleDiscoverer) ResourceType() string { return "aws_iam_role" }

// Discover paginates ListRoles and filters by name prefix matching
// project. IAM has no server-side filter on ListRoles, but role names
// in InsideOut stacks are conventionally prefixed by the project name,
// so client-side prefix filtering matches the bounded-account
// assumption already used by the DynamoDB discoverer.
//
// Import ID for aws_iam_role is the role name.
func (d *iamRoleDiscoverer) Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	client := d.new()

	type role struct {
		name string
		arn  string
	}
	var roles []role

	paginator := iam.NewListRolesPaginator(client, &iam.ListRolesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("ListRoles: %w", err)
		}
		for _, r := range page.Roles {
			name := aws.ToString(r.RoleName)
			if project != "" && !strings.HasPrefix(name, project) {
				continue
			}
			roles = append(roles, role{name: name, arn: aws.ToString(r.Arn)})
		}
	}

	sort.Slice(roles, func(i, j int) bool { return roles[i].name < roles[j].name })

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(roles))
	for _, r := range roles {
		imps = append(imps, makeImportedResource(
			book,
			"aws_iam_role",
			r.name,
			r.name,
			region,
			accountID,
			map[string]string{"arn": r.arn},
		))
	}
	return imps, nil
}

// DiscoverByID resolves an IAM role by ARN
// (arn:aws:iam::<account>:role/<name>) or bare role name. Issues a
// single GetRole call to verify existence.
func (d *iamRoleDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := iamRoleNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new()
	out, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(name)})
	if err != nil {
		var notFound *iamtypes.NoSuchEntityException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_iam_role %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetRole: %w", err)
	}
	arn := ""
	if out.Role != nil {
		arn = aws.ToString(out.Role.Arn)
	}
	return makeImportedResource(
		addressBook{},
		"aws_iam_role",
		name,
		name,
		region,
		accountID,
		map[string]string{"arn": arn},
	), nil
}

// iamRoleNameFromID extracts the IAM role name from an ARN
// (arn:aws:iam::<account>:role/<name>) or bare name.
func iamRoleNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("iam: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("iam: parse arn: %w", err)
		}
		if parsed.Service != "iam" {
			return "", fmt.Errorf("iam: not an iam arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// Resource is "role/<name>" or "role/<path>/<name>".
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "role" || parts[1] == "" {
			return "", fmt.Errorf("iam: arn resource %q is not role/<name>: %w", parsed.Resource, ErrNotSupported)
		}
		// IAM role names are the *last* path segment for path-prefixed
		// roles (arn:...:role/<path>/<name>); GetRole accepts the bare
		// name without path.
		segs := strings.Split(parts[1], "/")
		return segs[len(segs)-1], nil
	}
	if strings.ContainsAny(id, " :") {
		return "", fmt.Errorf("iam: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
