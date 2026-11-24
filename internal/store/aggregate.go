package store

import (
	"sort"
	"time"
)

func Classify(event AuditEvent) string {
	switch {
	case event.Outcome == "unknown":
		return "unknown"
	case event.Reason == "rate_limited":
		return "rate_limited"
	case event.Decision == "pending" || event.Reason == "approval_required":
		return "pending"
	case event.Outcome == "downstream_error" || event.Decision == "error":
		return "downstream_error"
	case event.Decision == "denied":
		return "denied"
	case event.Decision == "allowed" && (event.Outcome == "success" || event.Outcome == ""):
		return "success"
	case event.Phase == "intent" || event.Decision == "forwarding":
		return "in_progress"
	default:
		return "other"
	}
}

func Aggregate(events []AuditEvent, from, to time.Time, pending int) Analytics {
	latest := map[string]AuditEvent{}
	for _, event := range events {
		current, ok := latest[event.RequestID]
		if !ok || event.OccurredAt.After(current.OccurredAt) || (event.OccurredAt.Equal(current.OccurredAt) && event.ID >= current.ID) {
			latest[event.RequestID] = event
		}
	}
	result := Analytics{From: from, To: to, Latency: "gateway_duration_including_downstream", PendingApprovals: pending, Series: []Series{}, Tools: []ToolCount{}}
	latencies := []float64{}
	buckets := map[time.Time]int{}
	tools := map[string]int{}
	for _, event := range latest {
		kind := Classify(event)
		if kind == "in_progress" {
			continue
		}
		result.Total++
		switch kind {
		case "success":
			result.Success++
		case "denied":
			result.Denied++
		case "rate_limited":
			result.RateLimited++
		case "pending":
			result.Pending++
		case "downstream_error":
			result.DownstreamError++
		case "unknown":
			result.Unknown++
		}
		if event.DurationMS > 0 && (kind == "success" || kind == "downstream_error") {
			latencies = append(latencies, float64(event.DurationMS))
		}
		bucket := event.OccurredAt.UTC().Truncate(time.Hour)
		buckets[bucket]++
		if event.Tool != "" && event.Tool != "<unknown>" && event.Tool != "<approval>" {
			tools[event.Tool]++
		}
	}
	if result.Total > 0 {
		rate := float64(result.Success) / float64(result.Total)
		result.SuccessRate = &rate
	}
	sort.Float64s(latencies)
	if len(latencies) >= 1 {
		result.P50MS = percentile(latencies, 0.50)
	}
	if len(latencies) >= 20 {
		result.P95MS = percentile(latencies, 0.95)
	}
	if len(latencies) >= 100 {
		result.P99MS = percentile(latencies, 0.99)
	}
	for bucket, count := range buckets {
		result.Series = append(result.Series, Series{Bucket: bucket, Count: count})
	}
	sort.Slice(result.Series, func(i, j int) bool { return result.Series[i].Bucket.Before(result.Series[j].Bucket) })
	for tool, count := range tools {
		result.Tools = append(result.Tools, ToolCount{Tool: tool, Count: count})
	}
	sort.Slice(result.Tools, func(i, j int) bool { return result.Tools[i].Count > result.Tools[j].Count })
	return result
}

func percentile(sorted []float64, p float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	index := int(float64(len(sorted)-1) * p)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	value := sorted[index]
	return &value
}
