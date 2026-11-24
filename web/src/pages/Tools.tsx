import { useEffect, useState } from "react";
import { api } from "../api";

type Tool = { name: string; server: string; risk: string; description: string; roles: string[]; allowed: boolean; disabled: boolean; schema: unknown };

export function ToolsPage() {
  const [tools, setTools] = useState<Tool[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    api<{ tools: Tool[] }>("/api/v1/tools").then((payload) => setTools(payload.tools)).catch((reason: Error) => setError(reason.message));
  }, []);
  return (
    <section className="grid">
      <h1>Connected tools</h1>
      <p className="muted">The catalog is fixed. The interface cannot add executables, URLs, or shell commands.</p>
      {error && <p role="alert">{error}</p>}
      {tools.length === 0 && <p>No tools are visible for this role.</p>}
      {tools.map((tool) => (
        <article className="panel" key={tool.name}>
          <h2>{tool.name}</h2>
          <p>{tool.description}</p>
          <p><span className="pill">{tool.server}</span> <span className={`pill ${tool.risk === "high" ? "danger" : ""}`}>{tool.risk}</span> {tool.disabled ? <span className="pill">disabled</span> : null}</p>
          <p className="muted">Roles: {tool.roles.join(", ") || "none"}. {tool.allowed ? "You can call this tool." : "Your role cannot call this tool."}</p>
          <pre>{JSON.stringify(tool.schema, null, 2)}</pre>
        </article>
      ))}
    </section>
  );
}
