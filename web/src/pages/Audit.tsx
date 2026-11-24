import { useEffect, useState } from "react";
import { api, formatWhen } from "../api";

type Event = { id: number; occurred_at: string; user_id: string; agent_id: string; tool: string; decision: string; reason: string; risk: string; outcome: string; request_id: string; duration_ms: number };

export function AuditPage() {
  const [events, setEvents] = useState<Event[]>([]);
  const [tool, setTool] = useState("");
  const [outcome, setOutcome] = useState("");
  const [error, setError] = useState("");
  function load() {
    const params = new URLSearchParams();
    if (tool) params.set("tool", tool);
    if (outcome) params.set("outcome", outcome);
    const to = new Date();
    const from = new Date(to.getTime() - 7 * 24 * 60 * 60 * 1000);
    params.set("from", from.toISOString());
    params.set("to", to.toISOString());
    api<{ events: Event[] }>(`/api/v1/audit?${params}`).then((payload) => setEvents(payload.events ?? [])).catch((reason: Error) => setError(reason.message));
  }
  useEffect(load, []);
  return (
    <section className="grid">
      <h1>Audit logs</h1>
      <form className="row" onSubmit={(event) => { event.preventDefault(); load(); }}>
        <label>Tool<input className="field" value={tool} onChange={(event) => setTool(event.target.value)} /></label>
        <label>Outcome<input className="field" value={outcome} onChange={(event) => setOutcome(event.target.value)} /></label>
        <button className="btn" type="submit">Filter</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {events.length === 0 ? <p>No audit events in this range.</p> : (
        <div className="panel" style={{ overflowX: "auto" }}>
          <table>
            <thead><tr><th>Time</th><th>Identity</th><th>Tool</th><th>Decision</th><th>Reason</th><th>Request</th></tr></thead>
            <tbody>
              {events.map((event) => (
                <tr key={event.id}><td>{formatWhen(event.occurred_at)}</td><td>{event.user_id}</td><td>{event.tool}</td><td>{event.decision}</td><td>{event.reason}</td><td>{event.request_id}</td></tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
