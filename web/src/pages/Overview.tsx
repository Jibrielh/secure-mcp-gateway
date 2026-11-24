import { useEffect, useState } from "react";
import { api, percent } from "../api";

type Analytics = {
  total: number;
  success: number;
  denied: number;
  rate_limited: number;
  pending: number;
  downstream_error: number;
  unknown: number;
  success_rate: number | null;
  p50_ms: number | null;
  p95_ms: number | null;
  p99_ms: number | null;
  pending_approvals: number;
  latency: string;
  series: { bucket: string; count: number }[];
  tools: { tool: string; count: number }[];
};
type Integrations = { integrations: { id: string; display_name: string; status: string; enabled: boolean; detail: string }[] };

export function OverviewPage() {
  const [data, setData] = useState<Analytics | null>(null);
  const [services, setServices] = useState<Integrations["integrations"]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    const to = new Date();
    const from = new Date(to.getTime() - 24 * 60 * 60 * 1000);
    Promise.all([
      api<Analytics>(`/api/v1/analytics?from=${from.toISOString()}&to=${to.toISOString()}`),
      api<Integrations>("/api/v1/integrations"),
    ]).then(([analytics, integrations]) => {
      setData(analytics);
      setServices(integrations.integrations);
    }).catch((reason: Error) => setError(reason.message));
  }, []);
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <p>Loading overview…</p>;
  const max = Math.max(1, ...data.series.map((point) => point.count));
  return (
    <section className="grid">
      <h1>Overview</h1>
      <p className="muted">Counts are distinct tool requests from stored audit rows for the last 24 hours. Latency is gateway time, including downstream time. {data.latency}</p>
      <div className="cards">
        <Stat label="Requests" value={String(data.total)} />
        <Stat label="Allowed" value={String(data.success)} />
        <Stat label="Denied" value={String(data.denied)} />
        <Stat label="Rate limited" value={String(data.rate_limited)} />
        <Stat label="Pending" value={String(data.pending_approvals)} />
        <Stat label="Success rate" value={percent(data.success_rate)} />
        <Stat label="p50 ms" value={data.p50_ms === null ? "Unavailable" : String(data.p50_ms)} />
        <Stat label="p95 ms" value={data.p95_ms === null ? "Unavailable" : String(data.p95_ms)} />
        <Stat label="p99 ms" value={data.p99_ms === null ? "Unavailable" : String(data.p99_ms)} />
      </div>
      <div className="panel">
        <h2>Request volume</h2>
        {data.series.length === 0 ? <p>No requests in this range.</p> : (
          <svg role="img" aria-label="Requests per hour" viewBox={`0 0 ${data.series.length * 28} 120`} width="100%" height="140">
            {data.series.map((point, index) => (
              <rect key={point.bucket} x={index * 28 + 4} y={110 - (point.count / max) * 100} width="16" height={(point.count / max) * 100} fill="currentColor">
                <title>{`${point.bucket}: ${point.count}`}</title>
              </rect>
            ))}
          </svg>
        )}
        <table>
          <caption className="muted">Same series as text</caption>
          <thead><tr><th>Hour</th><th>Requests</th></tr></thead>
          <tbody>{data.series.map((point) => <tr key={point.bucket}><td>{point.bucket}</td><td>{point.count}</td></tr>)}</tbody>
        </table>
      </div>
      <div className="panel">
        <h2>Connectors</h2>
        {services.length === 0 ? <p>No connectors are configured.</p> : services.map((service) => (
          <p key={service.id}><strong>{service.display_name}</strong> <span className="pill">{service.enabled ? service.status : "disabled"}</span> <span className="muted">{service.detail}</span></p>
        ))}
      </div>
    </section>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return <article className="panel"><p className="muted">{label}</p><strong>{value}</strong></article>;
}
