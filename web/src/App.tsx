import { createContext, useContext, useEffect, useState } from "react";
import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import { api, type Session } from "./api";
import { AgentsPage } from "./pages/Agents";
import { ApprovalsPage } from "./pages/Approvals";
import { AuditPage } from "./pages/Audit";
import { ChatPage } from "./pages/Chat";
import { InsightsPage } from "./pages/Insights";
import { OverviewPage } from "./pages/Overview";
import { PoliciesPage } from "./pages/Policies";
import { SettingsPage } from "./pages/Settings";
import { ToolsPage } from "./pages/Tools";

type SessionContext = { session: Session; refresh: () => Promise<void>; csrf: string };
const SessionState = createContext<SessionContext | null>(null);
export function useSession() {
  const value = useContext(SessionState);
  if (!value) throw new Error("session missing");
  return value;
}

const links = [
  ["/", "Overview"],
  ["/chat", "Chat"],
  ["/agents", "Agents"],
  ["/tools", "Connected Tools"],
  ["/approvals", "Approvals"],
  ["/audit", "Audit Logs"],
  ["/policies", "Access Policies"],
  ["/insights", "Security Insights"],
  ["/settings", "Settings"],
];

export function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [error, setError] = useState("");
  async function refresh() {
    setSession(await api<Session>("/api/v1/session"));
  }
  useEffect(() => {
    refresh().catch((reason: Error) => setError(reason.message));
  }, []);
  if (error) return <main className="main"><p role="alert">The workspace API is unreachable. {error}</p></main>;
  if (!session) return <main className="main"><p>Loading session…</p></main>;
  if (!session.authenticated) return <Login session={session} onDone={refresh} />;
  return (
    <SessionState.Provider value={{ session, refresh, csrf: session.csrf_token ?? "" }}>
      <div className="shell">
        <nav className="nav" aria-label="Workspace">
          <strong>AgentGate</strong>
          <p className="muted">{session.user?.display_name}</p>
          {links.map(([path, label]) => (
            <NavLink key={path} to={path} end={path === "/"}>{label}</NavLink>
          ))}
          <button className="btn secondary" type="button" onClick={() => {
            const next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
            document.documentElement.dataset.theme = next;
            localStorage.setItem("agentgate-theme", next);
          }}>Theme</button>
        </nav>
        <main className="main">
          <Routes>
            <Route path="/" element={<OverviewPage />} />
            <Route path="/chat" element={<ChatPage />} />
            <Route path="/agents" element={<AgentsPage />} />
            <Route path="/tools" element={<ToolsPage />} />
            <Route path="/approvals" element={<ApprovalsPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="/policies" element={<PoliciesPage />} />
            <Route path="/insights" element={<InsightsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </SessionState.Provider>
  );
}

function Login({ session, onDone }: { session: Session; onDone: () => Promise<void> }) {
  const [username, setUsername] = useState("ada");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  return (
    <main className="main">
      <section className="panel" style={{ maxWidth: 420 }}>
        <h1>Sign in</h1>
        <p className="muted">Local workspace. Development accounts are enabled only when the server starts with development login.</p>
        {session.oidc && <p><a href="/api/v1/auth/login">Continue with the local identity provider</a></p>}
        {session.dev_login && (
          <form className="grid" onSubmit={async (event) => {
            event.preventDefault();
            setError("");
            try {
              await api("/api/v1/auth/dev-login", { method: "POST", body: { username, password } });
              await onDone();
            } catch (reason) {
              setError(reason instanceof Error ? reason.message : "invalid_login");
            }
          }}>
            <label>Username<input className="field" value={username} onChange={(event) => setUsername(event.target.value)} /></label>
            <label>Password<input className="field" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" /></label>
            {error && <p role="alert">{error}</p>}
            <button className="btn" type="submit">Sign in</button>
            <p className="muted">Development usernames: ada, owen, vera, amy. The password is the one generated in your local .env.</p>
          </form>
        )}
        {!session.dev_login && !session.oidc && <p role="status">No sign-in method is enabled.</p>}
      </section>
    </main>
  );
}
