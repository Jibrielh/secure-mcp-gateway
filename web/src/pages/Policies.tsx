import { useEffect, useState } from "react";
import { api } from "../api";
import { useSession } from "../App";

type Catalog = { name: string; risk: string; roles: string[]; description: string; server: string };
type Restriction = { tool: string; disabled: boolean; roles: string[] };

export function PoliciesPage() {
  const { csrf, session } = useSession();
  const [catalog, setCatalog] = useState<Catalog[]>([]);
  const [restrictions, setRestrictions] = useState<Restriction[]>([]);
  const [error, setError] = useState("");
  function load() {
    api<{ catalog: Catalog[]; restrictions: Restriction[] }>("/api/v1/policies").then((payload) => {
      setCatalog(payload.catalog);
      setRestrictions(payload.restrictions ?? []);
    }).catch((reason: Error) => setError(reason.message));
  }
  useEffect(load, []);
  return (
    <section className="grid">
      <h1>Access policies</h1>
      <p className="muted">Missing permission is a denial. An administrator can remove roles or disable a tool. They cannot add a role the catalog does not allow.</p>
      {error && <p role="alert">{error}</p>}
      <div className="panel" style={{ overflowX: "auto" }}>
        <table>
          <thead><tr><th>Tool</th><th>Risk</th><th>Roles</th><th>Action</th></tr></thead>
          <tbody>
            {catalog.map((tool) => {
              const restriction = restrictions.find((item) => item.tool === tool.name);
              return (
                <tr key={tool.name}>
                  <td>{tool.name}<div className="muted">{tool.description}</div></td>
                  <td>{tool.risk}</td>
                  <td>{(restriction?.roles ?? tool.roles).join(", ")} {restriction?.disabled ? "(disabled)" : ""}</td>
                  <td>{session.user?.role === "admin" && <button className="btn secondary" type="button" onClick={() => api("/api/v1/policies", { method: "PUT", csrf, body: { tool: tool.name, disabled: true, roles: tool.roles } }).then(load).catch((reason: Error) => setError(reason.message))}>Disable</button>}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
