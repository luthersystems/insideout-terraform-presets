// Cost Explorer service inspector.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go (cost-explorer:
// 1505-1772). Issue #225: AWSServiceActions advertised "cost-explorer" but
// the discovery dispatcher had no arm — calls fell through to
// ErrUnsupportedService. This brings the surface back here so the InsideOut backend's
// Phase B swap to pkg/observability (the InsideOut backend#1252 PR 2) doesn't regress
// production cost reporting.
//
// Cost Explorer is a global API: callers should construct cfg in
// us-east-1 (Cost Explorer doesn't accept other regions). The
// `DataUnavailable` graceful-degradation path matters for new accounts
// — billing data lags first usage by 24-48h, and forecasts need at least
// 14 days of history. Returning a {note, period} map instead of an error
// keeps the panel rendering instead of showing a wall of red.

package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// CostExplorerAPI is the subset of the Cost Explorer SDK used by the
// inspector. Mirrors the InsideOut backend's CostExplorerAPI
// (aws_inspect.go:1505-1509) — exported so tests can inject a mock and
// callers porting from the InsideOut backend need no rename.
type CostExplorerAPI interface {
	GetCostAndUsage(ctx context.Context, params *costexplorer.GetCostAndUsageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error)
	GetCostForecast(ctx context.Context, params *costexplorer.GetCostForecastInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetCostForecastOutput, error)
}

// formatUSD formats a Cost Explorer amount string (e.g. "12.345600") as
// "$12.35". Returns the input unchanged on parse failure so we surface the
// raw API response rather than masking it with "$0.00".
func formatUSD(amount string) string {
	f := 0.0
	if _, err := fmt.Sscanf(amount, "%f", &f); err != nil {
		return amount
	}
	return fmt.Sprintf("$%.2f", f)
}

func inspectCostExplorer(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	return inspectCostExplorerWithDeps(ctx, costexplorer.NewFromConfig(cfg), action, filters)
}

// inspectCostExplorerWithDeps is the testable core. Accepts an injected
// [CostExplorerAPI] so unit tests can run against a mock without touching
// AWS.
func inspectCostExplorerWithDeps(ctx context.Context, client CostExplorerAPI, action, filters string) (any, error) {
	var filterMap map[string]string
	if filters != "" {
		_ = json.Unmarshal([]byte(filters), &filterMap)
	}

	switch action {
	case "get-cost-summary":
		days := 30
		if d := filterMap["days"]; d != "" {
			if _, err := fmt.Sscanf(d, "%d", &days); err != nil || days < 1 {
				days = 30
			}
			if days > 365 {
				days = 365
			}
		}

		granularity := cetypes.GranularityMonthly
		if g := filterMap["granularity"]; strings.EqualFold(g, "DAILY") {
			granularity = cetypes.GranularityDaily
		}

		now := time.Now().UTC()
		start := now.AddDate(0, 0, -days).Format("2006-01-02")
		end := now.Format("2006-01-02")

		out, err := client.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &cetypes.DateInterval{
				Start: aws.String(start),
				End:   aws.String(end),
			},
			Granularity: granularity,
			Metrics:     []string{"UnblendedCost", "UsageQuantity"},
			GroupBy: []cetypes.GroupDefinition{
				{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
			},
		})
		if err != nil {
			if strings.Contains(err.Error(), "DataUnavailable") {
				return map[string]any{
					"note":   "No billing data available yet. Cost data typically appears 24-48 hours after first usage.",
					"period": fmt.Sprintf("%s to %s", start, end),
				}, nil
			}
			return nil, err
		}

		serviceCosts := make(map[string]float64)
		var totalCost float64
		for _, result := range out.ResultsByTime {
			for _, group := range result.Groups {
				svcName := ""
				if len(group.Keys) > 0 {
					svcName = group.Keys[0]
				}
				if cost, ok := group.Metrics["UnblendedCost"]; ok && cost.Amount != nil {
					var amount float64
					if _, err := fmt.Sscanf(*cost.Amount, "%f", &amount); err == nil {
						serviceCosts[svcName] += amount
						totalCost += amount
					}
				}
			}
		}

		type serviceCostEntry struct {
			Service string `json:"service"`
			Cost    string `json:"cost"`
		}
		sorted := []serviceCostEntry{}
		for svc, cost := range serviceCosts {
			sorted = append(sorted, serviceCostEntry{Service: svc, Cost: formatUSD(fmt.Sprintf("%f", cost))})
		}
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if serviceCosts[sorted[j].Service] > serviceCosts[sorted[i].Service] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}

		return map[string]any{
			"period":        fmt.Sprintf("%s to %s", start, end),
			"granularity":   string(granularity),
			"total_cost":    formatUSD(fmt.Sprintf("%f", totalCost)),
			"by_service":    sorted,
			"service_count": len(sorted),
		}, nil

	case "get-cost-forecast":
		now := time.Now().UTC()
		start := now.AddDate(0, 0, 1).Format("2006-01-02")
		endOfMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		// If less than 2 days until end of month, forecast next month —
		// AWS rejects intervals under ~1 day.
		if endOfMonth.Sub(now).Hours() < 48 {
			endOfMonth = endOfMonth.AddDate(0, 1, 0)
		}
		end := endOfMonth.Format("2006-01-02")

		out, err := client.GetCostForecast(ctx, &costexplorer.GetCostForecastInput{
			TimePeriod: &cetypes.DateInterval{
				Start: aws.String(start),
				End:   aws.String(end),
			},
			Granularity: cetypes.GranularityMonthly,
			Metric:      cetypes.MetricUnblendedCost,
		})
		if err != nil {
			if strings.Contains(err.Error(), "DataUnavailable") || strings.Contains(err.Error(), "not enough data") ||
				strings.Contains(err.Error(), "BillEstimateLineItemDataUnavailable") {
				return map[string]any{
					"note":   "Cost forecast unavailable. AWS requires at least 14 days of billing history to generate forecasts.",
					"period": fmt.Sprintf("%s to %s", start, end),
				}, nil
			}
			return nil, err
		}

		result := map[string]any{
			"period": fmt.Sprintf("%s to %s", start, end),
		}
		if out.Total != nil && out.Total.Amount != nil {
			result["forecast_total"] = formatUSD(*out.Total.Amount)
			result["unit"] = aws.ToString(out.Total.Unit)
		}
		if len(out.ForecastResultsByTime) > 0 {
			var periods []map[string]string
			for _, f := range out.ForecastResultsByTime {
				p := map[string]string{}
				if f.TimePeriod != nil {
					p["start"] = aws.ToString(f.TimePeriod.Start)
					p["end"] = aws.ToString(f.TimePeriod.End)
				}
				if f.MeanValue != nil {
					p["mean_value"] = formatUSD(*f.MeanValue)
				}
				periods = append(periods, p)
			}
			result["by_period"] = periods
		}
		return result, nil

	case "get-cost-by-tag":
		tagKey := filterMap["tag_key"]
		if tagKey == "" {
			return nil, fmt.Errorf("get-cost-by-tag requires tag_key in filters (e.g. {\"tag_key\":\"Environment\"})")
		}

		days := 30
		if d := filterMap["days"]; d != "" {
			if _, err := fmt.Sscanf(d, "%d", &days); err != nil || days < 1 {
				days = 30
			}
			if days > 365 {
				days = 365
			}
		}

		now := time.Now().UTC()
		start := now.AddDate(0, 0, -days).Format("2006-01-02")
		end := now.Format("2006-01-02")

		out, err := client.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &cetypes.DateInterval{
				Start: aws.String(start),
				End:   aws.String(end),
			},
			Granularity: cetypes.GranularityMonthly,
			Metrics:     []string{"UnblendedCost"},
			GroupBy: []cetypes.GroupDefinition{
				{Type: cetypes.GroupDefinitionTypeTag, Key: aws.String(tagKey)},
			},
		})
		if err != nil {
			if strings.Contains(err.Error(), "DataUnavailable") {
				return map[string]any{
					"note":   "No billing data available for the specified tag.",
					"period": fmt.Sprintf("%s to %s", start, end),
				}, nil
			}
			return nil, err
		}

		tagCosts := make(map[string]float64)
		var totalCost float64
		for _, result := range out.ResultsByTime {
			for _, group := range result.Groups {
				tagValue := "(untagged)"
				if len(group.Keys) > 0 && group.Keys[0] != "" {
					tagValue = group.Keys[0]
					// AWS returns "tag_key$tag_value" — split off the
					// key prefix so the panel shows just the value.
					if parts := strings.SplitN(tagValue, "$", 2); len(parts) == 2 {
						tagValue = parts[1]
						if tagValue == "" {
							tagValue = "(untagged)"
						}
					}
				}
				if cost, ok := group.Metrics["UnblendedCost"]; ok && cost.Amount != nil {
					var amount float64
					if _, err := fmt.Sscanf(*cost.Amount, "%f", &amount); err == nil {
						tagCosts[tagValue] += amount
						totalCost += amount
					}
				}
			}
		}

		type tagCostEntry struct {
			TagValue string `json:"tag_value"`
			Cost     string `json:"cost"`
		}
		sorted := []tagCostEntry{}
		for tag, cost := range tagCosts {
			sorted = append(sorted, tagCostEntry{TagValue: tag, Cost: formatUSD(fmt.Sprintf("%f", cost))})
		}
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if tagCosts[sorted[j].TagValue] > tagCosts[sorted[i].TagValue] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}

		return map[string]any{
			"period":     fmt.Sprintf("%s to %s", start, end),
			"tag_key":    tagKey,
			"total_cost": formatUSD(fmt.Sprintf("%f", totalCost)),
			"by_tag":     sorted,
		}, nil

	default:
		return nil, unsupportedActionError("cost-explorer", action)
	}
}
