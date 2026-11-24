package security

import "regexp"

var secretPattern = regexp.MustCompile(`(?i)(bearer\s+[a-z0-9._~+/-]{8,}|sk-[a-z0-9]{8,}|[a-f0-9]{48,}|password\s*[:=]\s*\S+|api[_-]?key\s*[:=]\s*\S+)`)

func scrub(value string) string {
	return secretPattern.ReplaceAllString(value, "[redacted]")
}

func scrubEvent(event Event) Event {
	event.Identity = scrub(event.Identity)
	event.Role = scrub(event.Role)
	event.UserID = scrub(event.UserID)
	event.AgentID = scrub(event.AgentID)
	event.Tool = scrub(event.Tool)
	event.DownstreamServer = scrub(event.DownstreamServer)
	event.Decision = scrub(event.Decision)
	event.Reason = scrub(event.Reason)
	event.Risk = scrub(event.Risk)
	event.ApprovalID = scrub(event.ApprovalID)
	event.Phase = scrub(event.Phase)
	event.Outcome = scrub(event.Outcome)
	return event
}
