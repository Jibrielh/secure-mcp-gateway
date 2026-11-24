import { useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";
import { api, type User } from "../api";
import { useSession } from "../App";

type Agent = { id: string; name: string; enabled: boolean };
type Conversation = { id: string; title: string; agent_id: string };
type Message = { id: string; role: string; body: string };
type RunEvent = { type: string; payload: Record<string, unknown> };

export function ChatPage() {
  const { csrf, session } = useSession();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentID, setAgentID] = useState("");
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationID, setConversationID] = useState("");
  const [messages, setMessages] = useState<Message[]>([]);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [prompt, setPrompt] = useState("");
  const [error, setError] = useState("");
  const [streaming, setStreaming] = useState("");
  const [pendingRun, setPendingRun] = useState("");
  useEffect(() => {
    api<{ agents: Agent[] }>("/api/v1/agents").then((payload) => {
      setAgents(payload.agents);
      if (payload.agents[0]) setAgentID(payload.agents[0].id);
    }).catch((reason: Error) => setError(reason.message));
    api<{ conversations: Conversation[] }>("/api/v1/conversations").then((payload) => setConversations(payload.conversations)).catch((reason: Error) => setError(reason.message));
  }, []);
  async function openConversation(id: string) {
    setConversationID(id);
    const payload = await api<{ messages: Message[] }>(`/api/v1/conversations/${id}`);
    setMessages(payload.messages);
    setEvents([]);
    setStreaming("");
  }
  async function createConversation() {
    const created = await api<Conversation>("/api/v1/conversations", { method: "POST", csrf, body: { agent_id: agentID, title: "New conversation" } });
    setConversations((current) => [created, ...current]);
    await openConversation(created.id);
  }
  async function send() {
    if (!conversationID || !prompt.trim()) return;
    const text = prompt;
    setPrompt("");
    setMessages((current) => [...current, { id: "local", role: "user", body: text }]);
    const run = await api<{ id: string }>(`/api/v1/conversations/${conversationID}/runs`, { method: "POST", csrf, body: { prompt: text } });
    setPendingRun("");
    watch(run.id);
  }
  async function resume() {
    if (!pendingRun) return;
    const id = pendingRun;
    await api(`/api/v1/runs/${id}/resume`, { method: "POST", csrf, body: {} });
    setPendingRun("");
    watch(id);
  }
  function watch(runID: string) {
    const source = new EventSource(`/api/v1/runs/${runID}/events`);
    source.onmessage = () => undefined;
    const types = ["assistant.delta", "tool.proposed", "tool.allowed", "tool.blocked", "tool.completed", "tool.failed", "approval.required", "run.completed", "run.failed", "run.cancelled", "run.started"];
    for (const type of types) {
      source.addEventListener(type, (event) => {
        const payload = JSON.parse((event as MessageEvent).data) as Record<string, unknown>;
        if (type === "assistant.delta") setStreaming((current) => current + String(payload.text ?? ""));
        setEvents((current) => [...current, { type, payload }]);
        if (type === "approval.required") setPendingRun(runID);
        if (type === "run.completed" || type === "run.failed" || type === "run.cancelled") setPendingRun("");
        if (type === "run.completed" || type === "run.failed" || type === "run.cancelled" || type === "approval.required") {
          source.close();
          if (conversationID) void openConversation(conversationID);
        }
      });
    }
    source.onerror = () => source.close();
  }
  return (
    <section>
      <h1>Chat</h1>
      {error && <p role="alert">{error}</p>}
      <div className="chat">
        <aside className="panel">
          <label>Agent<select className="field" value={agentID} onChange={(event) => setAgentID(event.target.value)}>{agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}</select></label>
          <button className="btn" type="button" onClick={() => createConversation().catch((reason: Error) => setError(reason.message))}>New conversation</button>
          {conversations.length === 0 && <p className="muted">No conversations yet.</p>}
          {conversations.map((conversation) => (
            <button className="btn secondary" type="button" key={conversation.id} onClick={() => openConversation(conversation.id).catch((reason: Error) => setError(reason.message))}>{conversation.title}</button>
          ))}
        </aside>
        <div>
          {messages.map((message) => <MessageView key={message.id + message.body.slice(0, 12)} message={message} />)}
          {streaming && <div className="bubble prose"><ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>{streaming}</ReactMarkdown></div>}
          {events.map((event, index) => <ToolCard key={index} event={event} user={session.user} />)}
          {pendingRun && (
            <div className="bubble">
              <p>Waiting for a different operator. Resume runs the original request after that person approves the exact action.</p>
              <button className="btn" type="button" onClick={() => resume().catch((reason: Error) => setError(reason.message))}>Resume approved action</button>
            </div>
          )}
          <form className="row" onSubmit={(event) => { event.preventDefault(); send().catch((reason: Error) => setError(reason.message)); }}>
            <label className="sr-only" htmlFor="composer">Message</label>
            <textarea id="composer" className="area" value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={3} placeholder="Ask the selected agent" />
            <button className="btn" type="submit">Send</button>
          </form>
        </div>
      </div>
    </section>
  );
}

function MessageView({ message }: { message: Message }) {
  return (
    <article className="bubble">
      <p className="muted">{message.role}</p>
      <div className="prose"><ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>{message.body}</ReactMarkdown></div>
    </article>
  );
}

function ToolCard({ event, user }: { event: RunEvent; user?: User }) {
  if (!event.type.startsWith("tool.") && event.type !== "approval.required") return null;
  const name = String(event.payload.name ?? "tool");
  return (
    <aside className="bubble" aria-label="Tool decision">
      <strong>{name}</strong> <span className="pill">{event.type}</span>
      {event.payload.code ? <p>Decision: {String(event.payload.code)}</p> : null}
      {event.payload.text ? <p>{String(event.payload.text)}</p> : null}
      {event.type === "approval.required" && <p>Waiting for a different operator. Signed in as {user?.username}.</p>}
    </aside>
  );
}
