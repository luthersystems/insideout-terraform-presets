// Stateful data-store inspectors: Cloud SQL, Memorystore (Redis),
// Firestore.
//
// Mirrors:
//   - inspectGCPCloudSQL    — the InsideOut backend gcp_inspect.go:929
//   - inspectGCPMemorystore — the InsideOut backend gcp_inspect.go:868
//   - inspectGCPFirestore   — the InsideOut backend gcp_inspect.go:1365
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
	"time"

	"cloud.google.com/go/firestore"
	firestoreadmin "cloud.google.com/go/firestore/apiv1/admin"
	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
	redis "cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

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
		items := []*sqladmin.DatabaseInstance{}
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
		// ListInstances has no server-side label filter; post-filter
		// on Instance.Labels.
		project := projectFromFilters(filters)
		return drainIterator(
			client.ListInstances(ctx, &redispb.ListInstancesRequest{
				Parent: fmt.Sprintf("projects/%s/locations/-", projectID),
			}),
			func(inst *redispb.Instance) bool {
				return gcpLabelMatches(inst.GetLabels(), "project", project)
			},
		)

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
//
// list-collections walks the data plane (Firestore client) and is the
// LLM-agent-facing introspection action ("what data is in this DB?").
// describe-database walks the admin plane (Firestore Admin client,
// distinct from the data-plane client) and is the panel-probe action
// ("does this resource exist? what type is it?"). Collections are
// application data created lazily on first write, so list-collections
// is a poor "is the resource live?" probe (#258).
func inspectFirestore(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-collections":
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
		return collectFirestoreCollectionIDs(client.Collections(ctx))

	case "describe-database":
		// For describe-database, an absent or unsafe database_name is a
		// hard error (vs list-collections which falls back to "(default)").
		// We have nothing to describe without a name.
		dbName := firestoreDatabaseFromFilters(filters)
		if dbName == "" {
			return nil, fmt.Errorf("firestore describe-database requires database_name in filters (preset gcp/firestore creates a non-default DB; pass the database_name output)")
		}
		admin, err := firestoreadmin.NewFirestoreAdminClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = admin.Close() }()
		return describeFirestoreDatabase(ctx, admin, projectID, dbName)

	default:
		return nil, unsupportedActionError("Firestore", action, observability.GCPServiceActions["firestore"])
	}
}

// firestoreCollectionIterator is the minimal slice of
// *firestore.CollectionIterator that collectFirestoreCollectionIDs
// consumes, narrow enough to fake in unit tests without a real
// Firestore client.
type firestoreCollectionIterator interface {
	Next() (*firestore.CollectionRef, error)
}

// collectFirestoreCollectionIDs drains a Firestore collection iterator
// into a non-nil []string. The empty-iterator path returns [], NOT nil
// — pinned by TestInspectFirestore_NoCollections_EmptySlice (#255) so
// downstream JSON marshals as `[]` instead of `null`.
func collectFirestoreCollectionIDs(it firestoreCollectionIterator) ([]string, error) {
	collections := []string{}
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
}

// firestoreAdminAPI is the minimal slice of *firestoreadmin.FirestoreAdminClient
// that describeFirestoreDatabase consumes — narrow enough to fake in
// unit tests without a real admin client (#258).
type firestoreAdminAPI interface {
	GetDatabase(ctx context.Context, req *adminpb.GetDatabaseRequest, opts ...gax.CallOption) (*adminpb.Database, error)
}

// describeFirestoreDatabase fetches the admin-plane Database object and
// returns a normalized JSON shape with enums rendered as strings (e.g.
// "FIRESTORE_NATIVE", "DATASTORE_MODE") and timestamps in RFC3339, so
// downstream UIs don't have to grok protobuf enum integers (#258).
func describeFirestoreDatabase(ctx context.Context, admin firestoreAdminAPI, projectID, dbName string) (any, error) {
	db, err := admin.GetDatabase(ctx, &adminpb.GetDatabaseRequest{
		Name: fmt.Sprintf("projects/%s/databases/%s", projectID, dbName),
	})
	if err != nil {
		return nil, fmt.Errorf("firestore GetDatabase: %w", err)
	}
	return map[string]any{
		"name":                          db.GetName(),
		"uid":                           db.GetUid(),
		"locationId":                    db.GetLocationId(),
		"type":                          db.GetType().String(),
		"concurrencyMode":               db.GetConcurrencyMode().String(),
		"appEngineIntegrationMode":      db.GetAppEngineIntegrationMode().String(),
		"pointInTimeRecoveryEnablement": db.GetPointInTimeRecoveryEnablement().String(),
		"createTime":                    formatTimestamp(db.GetCreateTime()),
		"updateTime":                    formatTimestamp(db.GetUpdateTime()),
		"etag":                          db.GetEtag(),
	}, nil
}

func formatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
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
