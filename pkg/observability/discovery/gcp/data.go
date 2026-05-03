// Stateful data-store inspectors: Cloud SQL, Memorystore (Redis),
// Firestore.
//
// Mirrors:
//   - inspectGCPCloudSQL    — reliable gcp_inspect.go:929
//   - inspectGCPMemorystore — reliable gcp_inspect.go:868
//   - inspectGCPFirestore   — reliable gcp_inspect.go:1365
//
// Cloud SQL uses the older google.golang.org/api/sqladmin/v1 surface
// (not a cloud.google.com/go SDK — there isn't one for the admin API).
// Memorystore uses the apiv1 Redis client. Firestore uses the
// firestore Go client which is project-scoped at construction.

package gcp

import (
	"context"
	"fmt"
	"log"
	"regexp"

	"cloud.google.com/go/firestore"
	redis "cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// firestoreDatabaseNameSafe constrains caller-supplied database names
// before they're passed to firestore.NewClientWithDatabase. The
// firestore admin spec allows lowercase letters, digits, and hyphens,
// 4-63 chars, must start with a letter, must end with a letter or
// digit. We accept that subset plus the literal "(default)" sentinel
// which is the SDK's symbolic name for the unnamed database. Anything
// else falls back to the SDK default with a warn log so a malformed
// caller value doesn't escape into a gRPC target path (#245, defense-
// in-depth on a string interpolated into the Firestore client target).
var firestoreDatabaseNameSafe = regexp.MustCompile(`^(\(default\)|[a-z][a-z0-9-]{2,61}[a-z0-9])$`)

func inspectCloudSQL(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	svc, err := sqladmin.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	switch action {
	case "list-instances":
		resp, err := svc.Instances.List(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		// sqladmin v1 has no server-side label filter; post-filter on
		// Settings.UserLabels (the preset uses `user_labels` instead
		// of `labels` on the Cloud SQL resource type).
		project := projectFromFilters(filters)
		if project == "" {
			return resp.Items, nil
		}
		var items []*sqladmin.DatabaseInstance
		for _, inst := range resp.Items {
			if inst.Settings == nil {
				continue
			}
			if gcpLabelMatches(inst.Settings.UserLabels, "project", project) {
				items = append(items, inst)
			}
		}
		return items, nil

	case "describe-instance":
		fm := parseFilterMap(filters)
		instance := fm["instance"]
		if instance == "" {
			return nil, fmt.Errorf("describe-instance requires instance in filters")
		}
		return svc.Instances.Get(projectID, instance).Context(ctx).Do()

	default:
		return nil, unsupportedActionError("Cloud SQL", action, observability.GCPServiceActions["cloudsql"])
	}
}

func inspectMemorystore(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-instances":
		client, err := redis.NewCloudRedisClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		// Parent format projects/<id>/locations/-, which the server
		// expands to every region the caller can see.
		it := client.ListInstances(ctx, &redispb.ListInstancesRequest{
			Parent: fmt.Sprintf("projects/%s/locations/-", projectID),
		})
		// ListInstances has no server-side label filter; post-filter
		// on Instance.Labels.
		project := projectFromFilters(filters)
		var instances []*redispb.Instance
		for {
			inst, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if !gcpLabelMatches(inst.GetLabels(), "project", project) {
				continue
			}
			instances = append(instances, inst)
		}
		return instances, nil

	case "describe-instance":
		fm := parseFilterMap(filters)
		location := fm["location"]
		instance := fm["instance"]
		if location == "" || instance == "" {
			return nil, fmt.Errorf("describe-instance requires location and instance in filters")
		}
		client, err := redis.NewCloudRedisClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		return client.GetInstance(ctx, &redispb.GetInstanceRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/instances/%s", projectID, location, instance),
		})

	default:
		return nil, unsupportedActionError("Memorystore", action, observability.GCPServiceActions["memorystore"])
	}
}

// inspectFirestore — no labels.project filter applies. Firestore is
// project-scoped at the client level. The preset (gcp/firestore/main.
// tf, issue #159) deliberately creates a non-default named database
// so retries after state loss don't 409 on the singleton; callers
// must thread that name in via filters as `database_name`. When
// omitted, the SDK falls back to "(default)" — which the preset
// never creates, so production calls hit `NotFound` (#245). The
// caller-supplied name is regex-validated before it reaches
// firestore.NewClientWithDatabase as defense-in-depth.
func inspectFirestore(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	dbName := firestoreDatabaseFromFilters(filters)
	var (
		client *firestore.Client
		err    error
	)
	if dbName != "" && dbName != "(default)" {
		client, err = firestore.NewClientWithDatabase(ctx, projectID, dbName, opts...)
	} else {
		if dbName == "" {
			log.Printf("[discovery/gcp firestore] no database_name in filters; falling back to (default) — preset gcp/firestore creates a non-default DB (#159), pass database_name from the preset's database_name output")
		}
		client, err = firestore.NewClient(ctx, projectID, opts...)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-collections":
		it := client.Collections(ctx)
		var collections []string
		for {
			c, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			collections = append(collections, c.ID)
		}
		return collections, nil

	default:
		return nil, unsupportedActionError("Firestore", action, observability.GCPServiceActions["firestore"])
	}
}

// firestoreDatabaseFromFilters extracts and validates the
// `database_name` filter key. Returns "" when missing, malformed, or
// unsafe so the caller falls back to the SDK default.
func firestoreDatabaseFromFilters(filters string) string {
	m := parseFilterMap(filters)
	if m == nil {
		return ""
	}
	name := m["database_name"]
	if name == "" {
		return ""
	}
	if !firestoreDatabaseNameSafe.MatchString(name) {
		log.Printf("[discovery/gcp firestore] rejected unsafe database_name=%q (must match %s); falling back to default", name, firestoreDatabaseNameSafe.String())
		return ""
	}
	return name
}
