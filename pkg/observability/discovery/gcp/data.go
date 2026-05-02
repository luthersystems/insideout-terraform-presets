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

	"cloud.google.com/go/firestore"
	redis "cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

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

// inspectFirestore — no labels.project filter applies. Firestore
// exposes a single Database per project, and Collections() returns
// root collection names. The API is project-scoped; nothing to
// label-filter against.
func inspectFirestore(ctx context.Context, projectID, action, _ string, opts ...option.ClientOption) (any, error) {
	client, err := firestore.NewClient(ctx, projectID, opts...)
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
