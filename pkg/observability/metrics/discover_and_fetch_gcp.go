// discover_and_fetch_gcp.go
//
// GCP Cloud Monitoring twin of DiscoverAndFetch. Ported from reliable's
// internal/agentapi/gcp_metrics.go::getGCPServiceMetrics. Unlike AWS,
// there is no per-service resource discovery on the GCP path: Cloud
// Monitoring's ListTimeSeries returns every resource in the project that
// publishes the metric, so the "discover" half is implicit and FetchGCP
// is called with a nil resource list. The bastion→compute alias and the
// Secret Manager operational-health envelope are the two reliable-side
// behaviors ported here; the metric fetch itself is FetchGCP (already in
// this repo).
//
// Required IAM permissions (or predefined roles) on the GCP project:
//
//	Monitoring Viewer (roles/monitoring.viewer)
//	  monitoring.timeSeries.list — all metric services.
//	Secret Manager Viewer (roles/secretmanager.viewer)
//	  secretmanager.secrets.list / versions.list — secretmanager health.
package metrics

import (
	"context"
	"fmt"
	"log"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// GCPSecretHealthResult holds operational health data for Secret Manager secrets.
type GCPSecretHealthResult struct {
	Service string                `json:"service"`
	Note    string                `json:"note"`
	Secrets []GCPSecretHealthInfo `json:"secrets"`
}

// GCPSecretHealthInfo holds health data for a single secret.
type GCPSecretHealthInfo struct {
	Name            string `json:"name"`
	VersionCount    int    `json:"version_count"`
	ReplicationType string `json:"replication_type,omitempty"`
	CreateTime      string `json:"create_time,omitempty"`
}

// gcpMetricDefinitions maps service names to upstream's GCPObs spec.
// Reliable kept a service-keyed view because observability.Observability
// is keyed by composer.ComponentKey; the inspector dispatch path joins on
// the service tag (obs.Service). The values are pointers into upstream's
// catalog — single source of truth, no copy. Ported from reliable's
// gcpMetricDefinitions.
var gcpMetricDefinitions = func() map[string]*observability.GCPObs {
	out := make(map[string]*observability.GCPObs)
	for _, obs := range observability.Observability {
		if obs.GCP == nil || obs.Service == "" {
			continue
		}
		if _, seen := out[obs.Service]; seen {
			continue
		}
		out[obs.Service] = obs.GCP
	}
	return out
}()

// newGCPClientsForFetch is the seam DiscoverAndFetchGCP uses to build a
// *GCPClients. Defaults to NewGCPClients; tests swap it so the fetch tail
// runs against a mocked Cloud Monitoring client.
var newGCPClientsForFetch = func(ctx context.Context, projectID string, opts ...option.ClientOption) (*GCPClients, error) {
	return NewGCPClients(ctx, projectID, opts...)
}

// DiscoverAndFetchGCP retrieves Cloud Monitoring metrics (or operational
// health data) for a GCP service. Reproduces reliable's
// getGCPServiceMetrics (gcp_metrics.go) behavior exactly.
//
// projectID scopes the query; filters carries the hours/period window
// (the project scope on GCP comes from projectID, not the filters tag).
// opts carry the broker-issued credentials — the same role
// createGCPClientOption played reliable-side. The return value is `any`
// to mirror reliable: the Cloud Monitoring path holds a MetricsResult
// (value), the secretmanager path a *GCPSecretHealthResult.
//
// The bastion alias resolves to the compute spec; the GCS daily-period
// override and label-breakdown grouping are owned by FetchGCP. Secret
// Manager returns an operational-health envelope, not Cloud Monitoring
// time-series. Note FetchGCP receives the ORIGINAL service (so the
// result's Service field and the gcs override key on the caller's
// service) while obs is the resolved spec — matching reliable.
func DiscoverAndFetchGCP(ctx context.Context, projectID, service, filters string, opts ...option.ClientOption) (any, error) {
	if service == "secretmanager" {
		return getGCPSecretHealth(ctx, projectID, opts...)
	}

	resolvedService := service
	if service == "bastion" {
		resolvedService = "compute"
	}
	obs, ok := gcpMetricDefinitions[resolvedService]
	if !ok {
		return nil, fmt.Errorf("no metric definitions for GCP service: %s", service)
	}

	clients, err := newGCPClientsForFetch(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("metrics gcp clients: %w", err)
	}
	defer func() { _ = clients.Close() }()

	mf := ParseMetricsFilter(filters)
	return FetchGCP(ctx, clients, service, obs, nil, mf)
}

// --- Secret Manager Operational Health ---

// gcpSecretIterator / gcpSecretVersionIterator are the minimal Next()
// surfaces the secret-health reader consumes. The concrete gapic
// iterators (*secretmanager.SecretIterator, *secretmanager.SecretVersionIterator)
// satisfy them structurally, so the real adapter returns them directly.
type gcpSecretIterator interface {
	Next() (*secretmanagerpb.Secret, error)
}

type gcpSecretVersionIterator interface {
	Next() (*secretmanagerpb.SecretVersion, error)
}

// gcpSecretManagerAPI is the subset of the Secret Manager client
// getGCPSecretHealth invokes. Seam for test injection; the real
// *secretmanager.Client is wrapped by realGCPSecretClient.
type gcpSecretManagerAPI interface {
	ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) gcpSecretIterator
	ListSecretVersions(ctx context.Context, req *secretmanagerpb.ListSecretVersionsRequest) gcpSecretVersionIterator
	Close() error
}

// realGCPSecretClient adapts *secretmanager.Client to gcpSecretManagerAPI.
// The gapic iterators are returned as-is — they satisfy the narrow
// iterator interfaces structurally.
type realGCPSecretClient struct {
	client *secretmanager.Client
}

func (r *realGCPSecretClient) ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) gcpSecretIterator {
	return r.client.ListSecrets(ctx, req)
}

func (r *realGCPSecretClient) ListSecretVersions(ctx context.Context, req *secretmanagerpb.ListSecretVersionsRequest) gcpSecretVersionIterator {
	return r.client.ListSecretVersions(ctx, req)
}

func (r *realGCPSecretClient) Close() error { return r.client.Close() }

// newGCPSecretClient is the seam getGCPSecretHealth uses to obtain its
// client.
var newGCPSecretClient = func(ctx context.Context, opts ...option.ClientOption) (gcpSecretManagerAPI, error) {
	c, err := secretmanager.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &realGCPSecretClient{client: c}, nil
}

// getGCPSecretHealth returns operational health data for Secret Manager
// secrets. Ported verbatim from reliable's getGCPSecretHealth (the creds
// argument is replaced by the option.ClientOption opts the caller
// supplies, same as the rest of the GCP path).
func getGCPSecretHealth(ctx context.Context, projectID string, opts ...option.ClientOption) (*GCPSecretHealthResult, error) {
	client, err := newGCPSecretClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create secret manager client: %w", err)
	}
	defer func() { _ = client.Close() }()

	it := client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: fmt.Sprintf("projects/%s", projectID),
	})

	var secrets []GCPSecretHealthInfo
	for {
		s, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list secrets: %w", err)
		}

		info := GCPSecretHealthInfo{
			Name: s.Name,
		}

		// Replication type
		if s.Replication != nil {
			switch s.Replication.Replication.(type) {
			case *secretmanagerpb.Replication_Automatic_:
				info.ReplicationType = "automatic"
			case *secretmanagerpb.Replication_UserManaged_:
				info.ReplicationType = "user-managed"
			}
		}

		// Create time
		if s.CreateTime != nil {
			info.CreateTime = s.CreateTime.AsTime().Format(time.RFC3339)
		}

		// Count versions
		versIt := client.ListSecretVersions(ctx, &secretmanagerpb.ListSecretVersionsRequest{
			Parent: s.Name,
		})
		versionCount := 0
		for {
			_, vErr := versIt.Next()
			if vErr == iterator.Done {
				break
			}
			if vErr != nil {
				log.Printf("[gcp-metrics] warning: list versions for %s: %v", s.Name, vErr)
				break
			}
			versionCount++
		}
		info.VersionCount = versionCount

		secrets = append(secrets, info)
	}

	return &GCPSecretHealthResult{
		Service: "secretmanager",
		Note:    "Secret health: version count, replication type, create time. Use list-secrets for full metadata.",
		Secrets: secrets,
	}, nil
}
