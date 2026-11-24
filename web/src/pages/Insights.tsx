import { useEffect, useState } from "react";
import { api } from "../api";
import { useSession } from "../App";

type Finding = { id: string; rule: string; severity: string; status: string; summary: string; evidence: unknown; explanation: string; rule_version: string };

export function InsightsPage() {
  const { csrf, session } = useSession();
  const [findings, setFindings] = useState<Finding[]>([]);
  const [error, setError] = useState("");
  function load() {
    api<{ findings: Finding[] }>("/api/v1/findings").then((payload) => setFindings(payload.findings)).catch((reason: Error) => setError(reason.message));
  }
  useEffect(load, []);
  return (
    <section className="grid">
      <h1>Security insights</h1>
      <p className="muted">Findings come from fixed rules. A local model explanation is advisory and is not used for enforcement.</p>
      {session.user?.role === "admin" && <button className="btn" type="button" onClick={() => api("/api/v1/findings/scan", { method: "POST", csrf, body: {} }).then(load).catch((reason: Error) => setError(reason.message))}>Scan recent audit</button>}
      {error && <p role="alert">{error}</p>}
      {findings.length === 0 && <p>No findings. A scan only creates one when the rule thresholds are met.</p>}
      {findings.map((finding) => (
        <article className="panel" key={finding.id}>
          <h2>{finding.summary}</h2>
          <p><span className={`pill ${finding.severity === "high" ? "danger" : "warn"}`}>{finding.severity}</span> {finding.rule} v{finding.rule_version} · {finding.status}</p>
          <pre>{JSON.stringify(finding.evidence, null, 2)}</pre>
          {finding.explanation && <p>{finding.explanation}</p>}
          <div className="row">
            <button className="btn secondary" type="button" onClick={() => api(`/api/v1/findings/${finding.id}/explain`, { method: "POST", csrf, body: {} }).then(load)}>Explain locally</button>
            {session.user?.role === "admin" && <button className="btn secondary" type="button" onClick={() => api(`/api/v1/findings/${finding.id}/status`, { method: "POST", csrf, body: { status: "acknowledged" } }).then(load)}>Acknowledge</button>}
          </div>
        </article>
      ))}
    </section>
  );
}
