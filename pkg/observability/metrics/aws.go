package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// Service-name constants for the special-cases the GetMetricData path
// has to know about. Keep these in sync with the keys in the InsideOut backend's
// metricDefinitions map (aws_metrics.go:258) — the inspector-side join
// key (observability.ComponentObservability.Service).
const (
	serviceCloudFront = "cloudfront"
	serviceS3         = "s3"
)

// S3 storage tile metrics need a daily aggregation period (CloudWatch
// only publishes BucketSizeBytes / NumberOfObjects once a day). Mirrors
// the S3 override in the InsideOut backend's getServiceMetricsWithDeps
// (aws_metrics.go:666-672).
const (
	s3PeriodSeconds = 86400 // 1 day
	s3MinHours      = 48    // need >=2 datapoints in the window
)

// ParseMetricsFilter parses the filters JSON into MetricsFilter with
// defaults applied. Mirrors the InsideOut backend's ParseMetricsFilter
// (aws_metrics.go:597). Empty / malformed input returns the defaults
// silently — callers that need to surface a parse error should
// json.Unmarshal directly.
func ParseMetricsFilter(filtersJSON string) MetricsFilter {
	f := MetricsFilter{Hours: 6, Period: 300}
	if filtersJSON != "" {
		_ = json.Unmarshal([]byte(filtersJSON), &f)
	}
	if f.Hours <= 0 {
		f.Hours = 6
	}
	if f.Period <= 0 {
		f.Period = 300
	}
	return f
}

// Fetch is the public metric-fetch entry point. It walks every
// resource in resources, builds the CloudWatch GetMetricData query set
// off every namespace/dimension group in groups (one element for the
// single-namespace common case; several for multi-namespace components
// like aws_opensearch — #778), issues one GetMetricData call per
// resource, and assembles the per-resource MetricSeries slice into a
// MetricsResult. Callers pass ComponentObservability.AWSGroups().
//
// service is the inspector-side join key from
// observability.ComponentObservability.Service ("ec2", "rds",
// "cloudfront", …). It's used only for the two production special
// cases:
//
//  1. service=="cloudfront" — AWS only publishes AWS/CloudFront metrics
//     in us-east-1; we ignore cw and pull a dedicated us-east-1 client
//     off the lazy CloudFront accessor.
//  2. service=="s3" — daily metrics; we override mf.Period to 86400 and
//     bump mf.Hours to >=48 so the chart has at least two datapoints.
//
// mf.AccountID supplies the dimension VALUE for account-keyed groups
// (the AOSS OCU group, #778, whose AWS/AOSS ClientId dimension is the
// account ID, not the collection ID). The caller resolves it once at
// dispatch (sts.GetCallerIdentity) and passes it in — Fetch issues no
// STS call of its own. When it's empty, account-keyed groups are skipped
// by the query builder; non-account groups are unaffected.
//
// Per-resource GetMetricData failures log+skip rather than aborting the
// whole call — mirrors the InsideOut backend (aws_metrics.go:692). Returning a
// partial result is preferable to losing every datapoint when one
// resource hits an IAM denial or throttle.
//
// CloudFront callers must pass a *Clients (not the bare CloudWatchAPI)
// so cloudFrontClient() can be invoked. The cw argument is honored for
// every other service; CloudFront callers may still pass cw — it's
// ignored. A future cleanup may consolidate to a single Clients arg.
func Fetch(
	ctx context.Context,
	clients *Clients,
	service string,
	groups []*observability.AWSObs,
	resources []ResourceID,
	mf MetricsFilter,
) (MetricsResult, error) {
	if clients == nil {
		return MetricsResult{}, fmt.Errorf("metrics: clients is required")
	}
	// At least one non-nil group is required. Most services pass exactly
	// one (Observability[k].AWS); multi-namespace components like
	// aws_opensearch pass several via ComponentObservability.AWSGroups()
	// (#778).
	hasGroup := false
	for _, g := range groups {
		if g != nil {
			hasGroup = true
			break
		}
	}
	if !hasGroup {
		return MetricsResult{}, fmt.Errorf("metrics: at least one obs group is required for service %q", service)
	}

	// Apply per-service overrides (mirrors aws_metrics.go:666-672).
	period := mf.Period
	hours := mf.Hours
	if service == serviceS3 {
		period = s3PeriodSeconds
		if hours < s3MinHours {
			hours = s3MinHours
		}
	}

	// Empty resource list short-circuits to a well-formed empty result
	// — same shape the InsideOut backend returns at aws_metrics.go:657.
	if len(resources) == 0 {
		return MetricsResult{
			Service:   service,
			TimeRange: fmt.Sprintf("last %d hours", hours),
			Period:    period,
			Resources: []ResourceMetrics{},
		}, nil
	}

	// Resolve which CloudWatch client to use. CloudFront pins to
	// us-east-1; everything else uses the caller's region client.
	cw := clients.CloudWatch
	if service == serviceCloudFront {
		cfClient, err := clients.cloudFrontClient(ctx)
		if err != nil {
			return MetricsResult{}, fmt.Errorf("metrics: cloudfront us-east-1 config: %w", err)
		}
		cw = cfClient
	}

	endTime := time.Now().UTC()
	startTime := endTime.Add(-time.Duration(hours) * time.Hour)
	clampedPeriod := max(min(period, 86400), 1)

	var out []ResourceMetrics
	for _, res := range resources {
		queries := BuildGetMetricDataQueries(groups, res, service, mf.AccountID)
		series, err := getMetricData(ctx, cw, queries, startTime, endTime, int32(clampedPeriod)) //nolint:gosec // clamped to [1, 86400]
		if err != nil {
			// Per-resource failures log and skip; matches the InsideOut backend's
			// aws_metrics.go:692 contract — a partial result beats
			// nothing when one resource hits an IAM denial or throttle.
			log.Printf("[metrics] warning: GetMetricData failed for %s/%s: %v", service, res.ID, err)
			continue
		}
		out = append(out, ResourceMetrics{
			ResourceID: res.ID,
			Metrics:    series,
		})
	}

	return MetricsResult{
		Service:   service,
		TimeRange: fmt.Sprintf("last %d hours", hours),
		Period:    period,
		Resources: out,
	}, nil
}

// BuildGetMetricDataQueries constructs the per-resource MetricDataQuery
// slice from every namespace/dimension group in groups. Mirrors the
// InsideOut backend's BuildMetricDataQueries (aws_metrics.go:712), extended to
// walk multiple groups (#778): aws_opensearch publishes managed-domain
// metrics under AWS/ES + DomainName AND serverless OCU metrics under
// AWS/AOSS + ClientId, so a single resource emits queries across both
// namespaces. Single-group services pass a one-element slice and get the
// original behavior.
//
// Query IDs are globally unique across groups ("m0", "m1", …) so a
// multi-group GetMetricData call never collides — CloudWatch rejects
// duplicate query IDs in one request.
//
// res.DimensionName overrides the dimension name ONLY for the primary
// (first) group — the per-resource ID was discovered against that group's
// dimension. Extra groups use their own DimensionName (e.g. the AOSS
// group's account-level ClientId), which is fixed by the namespace, not
// by the per-resource override.
//
// The dimension VALUE is res.ID for every normal group. A group whose
// AWSObs.DimensionValueAccountID is set (the AOSS OCU group, #778)
// instead keys on accountID — AWS/AOSS publishes OCU account-wide under
// ClientId=<account-id>, so res.ID (the collection/domain ID) returns an
// empty series. When such a group is present but accountID is empty, its
// queries are SKIPPED entirely: emitting a query with an empty dimension
// value silently matches nothing (the exact bug this guards), so a clean
// skip is preferable to a bogus, empty-result query.
//
// Two service-shaped quirks survive intact:
//
//   - CloudFront requires an extra Region=Global dimension — AWS uses
//     it to disambiguate the us-east-1-only metric publication from any
//     hypothetical regional split. (aws_metrics.go:726-731)
//   - S3 BucketSizeBytes / NumberOfObjects require a StorageType
//     dimension; the value depends on the metric name.
//     (aws_metrics.go:733-743)
//
// Period on each MetricStat is a placeholder (300); getMetricData
// overwrites it with the caller's clamped period before issuing the
// CloudWatch call.
func BuildGetMetricDataQueries(groups []*observability.AWSObs, res ResourceID, service, accountID string) []cwtypes.MetricDataQuery {
	var queries []cwtypes.MetricDataQuery
	primary := true
	for _, obs := range groups {
		if obs == nil {
			continue
		}
		isPrimary := primary
		primary = false

		// Resolve this group's dimension value source. Account-keyed groups
		// (the AOSS OCU group, #778) take the account ID; everything else
		// takes the per-resource ID. An account-keyed group with no account
		// ID is skipped — an empty dimension value silently matches nothing,
		// so emitting the query would just hand back an empty series.
		dimValue := res.ID
		if obs.DimensionValueAccountID {
			if accountID == "" {
				continue
			}
			dimValue = accountID
		}

		// The per-resource dimension-NAME override only applies to the
		// primary group; extra groups carry a namespace-fixed dimension of
		// their own (the AOSS group's ClientId, #778).
		dimName := obs.DimensionName
		if isPrimary && res.DimensionName != "" {
			dimName = res.DimensionName
		}

		for _, m := range obs.Metrics {
			id := fmt.Sprintf("m%d", len(queries))

			dimensions := []cwtypes.Dimension{{
				Name:  aws.String(dimName),
				Value: aws.String(dimValue),
			}}

			if service == serviceCloudFront {
				dimensions = append(dimensions, cwtypes.Dimension{
					Name:  aws.String("Region"),
					Value: aws.String("Global"),
				})
			}

			if service == serviceS3 {
				storageType := "StandardStorage"
				if m.Name == "NumberOfObjects" {
					storageType = "AllStorageTypes"
				}
				dimensions = append(dimensions, cwtypes.Dimension{
					Name:  aws.String("StorageType"),
					Value: aws.String(storageType),
				})
			}

			queries = append(queries, cwtypes.MetricDataQuery{
				Id:    aws.String(id),
				Label: aws.String(m.Name),
				MetricStat: &cwtypes.MetricStat{
					Metric: &cwtypes.Metric{
						Namespace:  aws.String(obs.Namespace),
						MetricName: aws.String(m.Name),
						Dimensions: dimensions,
					},
					Period: aws.Int32(300), // overwritten by getMetricData
					Stat:   aws.String(m.Stat),
				},
			})
		}
	}
	return queries
}

// getMetricData is the unexported CloudWatch GetMetricData wrapper.
// Mirrors the InsideOut backend's fetchMetrics (aws_metrics.go:765). Overwrites the
// per-query Period with the caller's clamped value before issuing the
// call so the placeholder set in BuildGetMetricDataQueries doesn't
// leak into production. Caller-side timestamp/value-len mismatches in
// the response are tolerated by truncating to the shorter of the two
// — same defensive trim the InsideOut backend does at aws_metrics.go:787.
func getMetricData(
	ctx context.Context,
	cw CloudWatchAPI,
	queries []cwtypes.MetricDataQuery,
	startTime, endTime time.Time,
	period int32,
) ([]MetricSeries, error) {
	for i := range queries {
		if queries[i].MetricStat != nil {
			queries[i].MetricStat.Period = aws.Int32(period)
		}
	}

	out, err := cw.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		MetricDataQueries: queries,
		StartTime:         aws.Time(startTime),
		EndTime:           aws.Time(endTime),
	})
	if err != nil {
		return nil, err
	}

	results := make([]MetricSeries, 0, len(out.MetricDataResults))
	for _, r := range out.MetricDataResults {
		series := MetricSeries{Name: aws.ToString(r.Label)}
		for i, ts := range r.Timestamps {
			if i >= len(r.Values) {
				break
			}
			series.Datapoints = append(series.Datapoints, Datapoint{
				Timestamp: ts.Format(time.RFC3339),
				Average:   r.Values[i],
			})
		}
		results = append(results, series)
	}
	return results, nil
}
