export type User = {
  id: string;
  username: string;
  display_name: string;
  role: string;
  approver: boolean;
};

export type Session = {
  authenticated: boolean;
  user?: User;
  csrf_token?: string;
  dev_login: boolean;
  oidc: boolean;
};

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export async function api<T>(path: string, init: { method?: string; body?: unknown; csrf?: string } = {}): Promise<T> {
  const headers: Record<string, string> = {};
  if (init.body !== undefined) headers["Content-Type"] = "application/json";
  if (init.csrf) headers["X-CSRF-Token"] = init.csrf;
  const response = await fetch(path, {
    method: init.method ?? "GET",
    headers,
    credentials: "include",
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
  });
  const text = await response.text();
  const payload = text ? JSON.parse(text) : {};
  if (!response.ok) throw new ApiError(response.status, payload.error ?? "request_failed");
  return payload as T;
}

export function formatWhen(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

export function percent(value: number | null | undefined) {
  if (value === null || value === undefined) return "Unavailable";
  return `${Math.round(value * 1000) / 10}%`;
}
