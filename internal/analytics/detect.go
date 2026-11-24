package analytics

import (
	"encoding/json"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

const RuleVersion = "1"

type Evidence struct {
	RequestIDs []string `json:"request_ids"`
	Count      int      `json:"count"`
	Window     string   `json:"window"`
	Identity   string   `json:"identity"`
}

type bucket struct {
	ids  []string
	when []time.Time
}

func Scan(events []store.AuditEvent, now time.Time) []store.Finding {
	denials := map[string]*bucket{}
	bursts := map[string]*bucket{}
	high := map[string]*bucket{}
	failures := map[string]*bucket{}
	add := func(groups map[string]*bucket, key, requestID string, at time.Time) {
		item := groups[key]
		if item == nil {
			item = &bucket{}
			groups[key] = item
		}
		item.ids = append(item.ids, requestID)
		item.when = append(item.when, at)
	}
	for _, event := range events {
		kind := store.Classify(event)
		add(bursts, event.UserID, event.RequestID, event.OccurredAt)
		if kind == "denied" {
			add(denials, event.UserID, event.RequestID, event.OccurredAt)
		}
		if event.Risk == "high" {
			add(high, event.UserID, event.RequestID, event.OccurredAt)
		}
		if kind == "downstream_error" {
			add(failures, event.UserID, event.RequestID, event.OccurredAt)
		}
	}
	var findings []store.Finding
	findings = append(findings, threshold(denials, now, 10*time.Minute, 5, "repeated_auth_denials", "medium", "Repeated authorization denials")...)
	findings = append(findings, threshold(bursts, now, time.Minute, 30, "request_burst", "medium", "Unusual request burst")...)
	findings = append(findings, threshold(high, now, 15*time.Minute, 3, "repeated_high_risk", "high", "Repeated high-risk requests")...)
	findings = append(findings, threshold(failures, now, 10*time.Minute, 4, "downstream_failures", "medium", "Frequent downstream failures")...)
	return findings
}

func threshold(groups map[string]*bucket, now time.Time, window time.Duration, minimum int, rule, severity, summary string) []store.Finding {
	var findings []store.Finding
	start := now.Add(-window)
	for identity, group := range groups {
		var ids []string
		for i, at := range group.when {
			if !at.Before(start) && at.Before(now.Add(time.Second)) {
				if len(ids) < 20 {
					ids = append(ids, group.ids[i])
				}
			}
		}
		if len(ids) < minimum {
			continue
		}
		evidence, _ := json.Marshal(Evidence{RequestIDs: ids, Count: len(ids), Window: window.String(), Identity: identity})
		findings = append(findings, store.Finding{
			Rule: rule, RuleVersion: RuleVersion, Severity: severity, Status: "open",
			Summary: summary, Evidence: evidence, DedupeKey: rule + ":" + identity + ":" + start.UTC().Format("200601021504"),
			WindowStart: start.UTC(), WindowEnd: now.UTC(), CreatedAt: now.UTC(),
		})
	}
	return findings
}

func FallbackExplanation(finding store.Finding) string {
	return "Rule " + finding.Rule + " version " + finding.RuleVersion + " observed " + finding.Summary + ". This explanation did not use a model. Review the evidence request IDs and the matching audit rows. No corrective action was taken."
}
