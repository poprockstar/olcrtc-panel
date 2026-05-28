import type {
  Client,
  ClientInput,
  Location,
  LocationInput,
  LogResponse,
  MetricsSnapshot,
  SessionResponse,
  Settings,
  StateResponse
} from "./types";

type RequestOptions = {
  method?: string;
  body?: unknown;
  csrfToken?: string;
  text?: boolean;
};

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number
  ) {
    super(message);
  }
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {};
  if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  if (options.csrfToken) {
    headers["X-CSRF-Token"] = options.csrfToken;
  }

  const response = await fetch(path, {
    method: options.method ?? "GET",
    credentials: "same-origin",
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body)
  });

  if (!response.ok) {
    throw new ApiError(await readError(response), response.status);
  }

  if (options.text) {
    return (await response.text()) as T;
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

async function readError(response: Response): Promise<string> {
  const text = await response.text();
  if (!text) {
    return `request failed with ${response.status}`;
  }
  try {
    const parsed = JSON.parse(text);
    return typeof parsed?.message === "string" ? parsed.message : text;
  } catch {
    return text.trim();
  }
}

export const api = {
  state: () => request<StateResponse>("/api/v1/state"),
  setup: (payload: { username: string; password: string }) =>
    request<SessionResponse>("/api/v1/setup", { method: "POST", body: payload }),
  login: (payload: { username: string; password: string }) =>
    request<SessionResponse>("/api/v1/login", { method: "POST", body: payload }),
  logout: (csrfToken: string) => request<void>("/api/v1/logout", { method: "POST", csrfToken }),
  settings: () => request<Settings>("/api/v1/settings"),
  saveSettings: (settings: Settings, csrfToken: string) =>
    request<Settings>("/api/v1/settings", { method: "PUT", body: settings, csrfToken }),
  metrics: () => request<MetricsSnapshot>("/api/v1/metrics"),
  reload: (csrfToken: string) => request<unknown>("/api/v1/reload", { method: "POST", csrfToken }),
  clients: () => request<Client[]>("/api/v1/clients"),
  createClient: (input: ClientInput, csrfToken: string) =>
    request<Client>("/api/v1/clients", { method: "POST", body: input, csrfToken }),
  updateClient: (id: string, input: ClientInput, csrfToken: string) =>
    request<Client>(`/api/v1/clients/${id}`, { method: "PUT", body: input, csrfToken }),
  deleteClient: (id: string, csrfToken: string) =>
    request<void>(`/api/v1/clients/${id}`, { method: "DELETE", csrfToken }),
  rotateClient: (id: string, input: { rotate_rooms?: boolean; rotate_subscription_token?: boolean }, csrfToken: string) =>
    request<Location[]>(`/api/v1/clients/${id}/rotate`, { method: "POST", body: input, csrfToken }),
  locations: (clientId: string) => request<Location[]>(`/api/v1/clients/${clientId}/locations`),
  createLocation: (clientId: string, input: LocationInput, csrfToken: string) =>
    request<Location>(`/api/v1/clients/${clientId}/locations`, { method: "POST", body: input, csrfToken }),
  updateLocation: (clientId: string, locationId: string, input: LocationInput, csrfToken: string) =>
    request<Location>(`/api/v1/clients/${clientId}/locations/${locationId}`, { method: "PUT", body: input, csrfToken }),
  deleteLocation: (clientId: string, locationId: string, csrfToken: string) =>
    request<void>(`/api/v1/clients/${clientId}/locations/${locationId}`, { method: "DELETE", csrfToken }),
  logs: (params: URLSearchParams) => request<LogResponse>(`/api/v1/logs?${params.toString()}`),
  logsText: (params: URLSearchParams) => request<string>(`/api/v1/logs?${params.toString()}`, { text: true }),
  subscription: (token: string) => request<string>(`/sub/${token}`, { text: true })
};
