import { useEffect, useState } from "react";
import { api } from "../api";
import { useSession } from "../App";

type Settings = { provider: string; model: string; models: string[]; ollama: string; paid_fallback: boolean; rate_limit_per_minute: number; max_steps: number };

export function SettingsPage() {
  const { csrf, refresh, session } = useSession();
  const [settings, setSettings] = useState<Settings | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api<Settings>("/api/v1/settings").then(setSettings).catch((reason: Error) => setError(reason.message));
  }, []);
  return (
    <section className="grid">
      <h1>Settings</h1>
      {error && <p role="alert">{error}</p>}
      {!settings ? <p>Loading settings…</p> : (
        <div className="panel">
          <p>Signed in as {session.user?.display_name} ({session.user?.role}).</p>
          <p>Provider: {settings.provider}. Selected model: {settings.model}. Ollama: {settings.ollama}.</p>
          <p>Installed models: {settings.models.length ? settings.models.join(", ") : "none reported"}.</p>
          <p>Rate limit: {settings.rate_limit_per_minute} calls per minute. Max agent steps: {settings.max_steps}.</p>
          <p>Paid model fallback: {settings.paid_fallback ? "enabled" : "disabled"}.</p>
        </div>
      )}
      <button className="btn secondary" type="button" onClick={() => api("/api/v1/auth/logout", { method: "POST", csrf, body: {} }).then(() => refresh())}>Sign out</button>
    </section>
  );
}
