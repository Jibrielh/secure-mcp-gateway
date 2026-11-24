import { useEffect, useState } from "react";
import { api, formatWhen } from "../api";
import { useSession } from "../App";

type Approval = { approval_id: string; identity: string; role: string; tool: string; arguments: unknown; status: string; expires_at: string; agent_id?: string; execution_outcome: string };

export function ApprovalsPage() {
  const { csrf, session } = useSession();
  const [items, setItems] = useState<Approval[]>([]);
  const [error, setError] = useState("");
  function load() {
    api<{ approvals: Approval[] }>("/api/v1/approvals").then((payload) => setItems(payload.approvals)).catch((reason: Error) => setError(reason.message));
  }
  useEffect(load, []);
  async function decide(id: string, decision: "approve" | "deny") {
    await api(`/api/v1/approvals/${id}/decision`, { method: "POST", csrf, body: { decision } });
    load();
  }
  return (
    <section className="grid">
      <h1>Approvals</h1>
      <p className="muted">An operator must be a different person from the requester. Approval resumes the original user and agent.</p>
      {error && <p role="alert">{error}</p>}
      {!session.user?.approver && <p>This account cannot decide approvals.</p>}
      {items.length === 0 && <p>No approval records.</p>}
      {items.map((item) => (
        <article className="panel" key={item.approval_id}>
          <h2>{item.tool}</h2>
          <p><span className="pill">{item.status}</span> outcome {item.execution_outcome}</p>
          <p>Requester {item.identity} ({item.role}). Agent {item.agent_id || "legacy"}.</p>
          <p>Expires {formatWhen(item.expires_at)}</p>
          <pre>{JSON.stringify(item.arguments, null, 2)}</pre>
          {session.user?.approver && item.status === "pending" && (
            <div className="row">
              <button className="btn" type="button" onClick={() => decide(item.approval_id, "approve").catch((reason: Error) => setError(reason.message))}>Approve exact action</button>
              <button className="btn secondary" type="button" onClick={() => decide(item.approval_id, "deny").catch((reason: Error) => setError(reason.message))}>Deny</button>
            </div>
          )}
        </article>
      ))}
    </section>
  );
}
