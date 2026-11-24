import { useEffect, useState } from "react";
import { api } from "../api";
import { useSession } from "../App";

type Agent = { id: string; name: string; description: string; system_prompt: string; model: string; tools: string[]; max_steps: number; enabled: boolean };
type Tool = { name: string; description: string; allowed: boolean };

export function AgentsPage() {
  const { csrf, session } = useSession();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [tools, setTools] = useState<Tool[]>([]);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState({ name: "", description: "", system_prompt: "", tools: [] as string[] });
  function load() {
    api<{ agents: Agent[] }>("/api/v1/agents").then((payload) => setAgents(payload.agents)).catch((reason: Error) => setError(reason.message));
    api<{ tools: Tool[] }>("/api/v1/tools").then((payload) => setTools(payload.tools.filter((tool) => tool.allowed))).catch((reason: Error) => setError(reason.message));
  }
  useEffect(load, []);
  const canEdit = session.user?.role === "developer" || session.user?.role === "admin";
  return (
    <section className="grid">
      <h1>Agents</h1>
      {error && <p role="alert">{error}</p>}
      {agents.length === 0 && <p>No agents yet.</p>}
      {agents.map((agent) => (
        <article className="panel" key={agent.id}>
          <h2>{agent.name} {!agent.enabled && <span className="pill">disabled</span>}</h2>
          <p>{agent.description}</p>
          <p className="muted">Model {agent.model}. Steps {agent.max_steps}. Tools: {agent.tools.join(", ") || "none"}.</p>
          {canEdit && <div className="row">
            <button className="btn secondary" type="button" onClick={() => api(`/api/v1/agents/${agent.id}/clone`, { method: "POST", csrf, body: {} }).then(load)}>Clone</button>
            <button className="btn secondary" type="button" onClick={() => api(`/api/v1/agents/${agent.id}/disable`, { method: "POST", csrf, body: {} }).then(load)}>Disable</button>
          </div>}
        </article>
      ))}
      {canEdit && (
        <form className="panel grid" onSubmit={(event) => {
          event.preventDefault();
          api("/api/v1/agents", { method: "POST", csrf, body: { ...draft, model: "", max_steps: 4, enabled: true } }).then(load).catch((reason: Error) => setError(reason.message));
        }}>
          <h2>Create agent</h2>
          <label>Name<input className="field" value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} required /></label>
          <label>Description<input className="field" value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} /></label>
          <label>Instructions<textarea className="area" value={draft.system_prompt} onChange={(event) => setDraft({ ...draft, system_prompt: event.target.value })} required /></label>
          <fieldset>
            <legend>Permitted tools</legend>
            {tools.map((tool) => (
              <label key={tool.name}><input type="checkbox" checked={draft.tools.includes(tool.name)} onChange={(event) => {
                const tools = event.target.checked ? [...draft.tools, tool.name] : draft.tools.filter((name) => name !== tool.name);
                setDraft({ ...draft, tools });
              }} /> {tool.name}</label>
            ))}
          </fieldset>
          <button className="btn" type="submit">Create</button>
        </form>
      )}
    </section>
  );
}
