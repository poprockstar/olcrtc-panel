import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { App } from "./App";
import { parseSubscriptionUris, providerSupportsTransport } from "./domain";

type MockResponse = {
  url?: string;
  method?: string;
  status?: number;
  headers?: Record<string, string>;
  body?: unknown;
};

function renderApp(responses: Array<MockResponse | ((input: RequestInfo | URL, init?: RequestInit) => MockResponse | Promise<MockResponse>)>) {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    calls.push({ url, init });
    const index = responses.findIndex((candidate) => {
      if (typeof candidate === "function") {
        return true;
      }
      const urlMatches = !candidate.url || candidate.url === url;
      const methodMatches = (candidate.method ?? "GET") === method;
      return urlMatches && methodMatches;
    });
    const next = index >= 0 ? responses.splice(index, 1)[0] : undefined;
    if (!next) {
      throw new Error(`unexpected fetch ${String(input)}`);
    }
    const response = typeof next === "function" ? await next(input, init) : next;
    const status = response.status ?? 200;
    const headers = new Headers(response.headers ?? { "content-type": "application/json" });
    const body = typeof response.body === "string" ? response.body : JSON.stringify(response.body ?? {});
    return new Response(body, { status, headers });
  });
  vi.stubGlobal("fetch", fetchMock);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } }
  });
  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  );
  return { calls, fetchMock };
}

beforeEach(() => {
  sessionStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("App routing", () => {
  test("shows setup when the API reports first run", async () => {
    renderApp([{ url: "/api/v1/state", body: { service: "olcpanel", api_version: "v1", setup_required: true, bind_address: "127.0.0.1:8888" } }]);

    expect(await screen.findByRole("heading", { name: /initial setup/i })).toBeInTheDocument();
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
  });

  test("shows login when setup is complete but no session exists", async () => {
    renderApp([{ url: "/api/v1/state", body: { service: "olcpanel", api_version: "v1", setup_required: false, authenticated: false, bind_address: "127.0.0.1:8888" } }]);

    expect(await screen.findByRole("heading", { name: /sign in/i })).toBeInTheDocument();
  });

  test("shows the authenticated shell when the state endpoint confirms a session", async () => {
    sessionStorage.setItem("olcpanel.session", JSON.stringify({ username: "admin", csrfToken: "csrf-token" }));
    renderApp([
      { url: "/api/v1/state", body: { service: "olcpanel", api_version: "v1", setup_required: false, authenticated: true, bind_address: "127.0.0.1:8888" } },
      { url: "/api/v1/settings", body: { ui_locale: "en", public_client_endpoint_enabled: false, backup_path: "/var/lib/olcpanel/backups", quota_lock_mode: "stop" } },
      { url: "/api/v1/metrics", body: metricsFixture() }
    ]);

    expect(await screen.findByRole("heading", { name: /dashboard/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /reload/i })).toBeInTheDocument();
  });
});

describe("browser mutations", () => {
  test("stores setup session and sends CSRF on later mutations", async () => {
    const user = userEvent.setup();
    const { calls } = renderApp([
      { url: "/api/v1/state", body: { service: "olcpanel", api_version: "v1", setup_required: true, bind_address: "127.0.0.1:8888" } },
      { url: "/api/v1/setup", method: "POST", body: { username: "admin", csrf_token: "csrf-token" } },
      { url: "/api/v1/state", body: { service: "olcpanel", api_version: "v1", setup_required: false, authenticated: true, bind_address: "127.0.0.1:8888" } },
      { url: "/api/v1/settings", body: { ui_locale: "en", public_client_endpoint_enabled: false, backup_path: "/var/lib/olcpanel/backups", quota_lock_mode: "stop" } },
      { url: "/api/v1/metrics", body: metricsFixture() },
      { url: "/api/v1/reload", method: "POST", body: { summary: { started: 0, restarted: 0, stopped: 0, unchanged: 0, skipped: 0 }, actions: [] } }
    ]);

    await user.type(await screen.findByLabelText(/username/i), "admin");
    await user.type(screen.getByLabelText(/password/i), "correct horse battery");
    await user.click(screen.getByRole("button", { name: /create admin/i }));
    await user.click(await screen.findByRole("button", { name: /reload/i }));

    const reload = calls.find((call) => call.url === "/api/v1/reload");
    expect(reload?.init?.headers).toMatchObject({ "X-CSRF-Token": "csrf-token" });
  });
});

describe("clients and locations", () => {
  test("creates clients with validated form payloads and shows API errors", async () => {
    const user = userEvent.setup();
    const { calls } = renderAuthenticated([
      { url: "/api/v1/metrics", body: metricsFixture() },
      { url: "/api/v1/clients", body: [] },
      { url: "/api/v1/clients", method: "POST", status: 400, headers: { "content-type": "text/plain; charset=utf-8" }, body: "client name is required" },
      { url: "/api/v1/clients", method: "POST", body: clientFixture({ name: "Acme", quota_bytes: 1073741824 }) },
      { url: "/api/v1/clients", body: [clientFixture({ name: "Acme", quota_bytes: 1073741824 })] }
    ]);

    await user.click(await screen.findByRole("button", { name: /clients/i }));
    await user.click(screen.getByRole("button", { name: /save client/i }));
    expect(await screen.findByText(/client name is required/i)).toBeInTheDocument();
    await user.type(screen.getByLabelText(/client name/i), "Acme");
    await user.clear(screen.getByLabelText(/quota bytes/i));
    await user.type(screen.getByLabelText(/quota bytes/i), "1073741824");
    await user.click(screen.getByRole("button", { name: /save client/i }));

    expect(await screen.findAllByText("Acme")).toHaveLength(2);
    const create = calls.filter((call) => call.url === "/api/v1/clients" && call.init?.method === "POST").at(-1);
    expect(JSON.parse(String(create?.init?.body))).toMatchObject({ name: "Acme", enabled: true, quota_bytes: 1073741824 });
  });

  test("prevents unsupported provider and transport combinations", async () => {
    const user = userEvent.setup();
    renderAuthenticated([
      { url: "/api/v1/metrics", body: metricsFixture() },
      { url: "/api/v1/clients", body: [clientFixture({ id: "cl_1", name: "Client" })] },
      { url: "/api/v1/clients/cl_1/locations", body: [] }
    ]);

    await user.click(await screen.findByRole("button", { name: /clients/i }));
    await user.click(await screen.findByRole("button", { name: /manage client client/i }));
    await user.selectOptions(screen.getByLabelText(/provider/i), "telemost");
    await user.selectOptions(screen.getAllByLabelText(/transport/i)[0], "datachannel");
    await user.type(screen.getByLabelText(/location name/i), "Bad");
    await user.click(screen.getByRole("button", { name: /save location/i }));

    expect(await screen.findByText(/not supported by selected provider/i)).toBeInTheDocument();
    expect(providerSupportsTransport("telemost", "datachannel")).toBe(false);
  });
});

describe("settings and subscriptions", () => {
  test("saves the complete settings object", async () => {
    const user = userEvent.setup();
    const { calls } = renderAuthenticated([
      { url: "/api/v1/metrics", body: metricsFixture() },
      { url: "/api/v1/settings", method: "PUT", body: { ui_locale: "ru", public_client_endpoint_enabled: true, backup_path: "/srv/backups", quota_lock_mode: "disable_traffic" } }
    ]);

    await user.click(await screen.findByRole("button", { name: /settings/i }));
    await user.selectOptions(screen.getByLabelText(/ui language/i), "ru");
    await user.click(screen.getByLabelText(/public client endpoint/i));
    await user.clear(screen.getByLabelText(/backup path/i));
    await user.type(screen.getByLabelText(/backup path/i), "/srv/backups");
    await user.selectOptions(screen.getByLabelText(/quota lock mode/i), "disable_traffic");
    await user.click(screen.getByRole("button", { name: /save settings/i }));

    const put = calls.find((call) => call.url === "/api/v1/settings" && call.init?.method === "PUT");
    expect(JSON.parse(String(put?.init?.body))).toEqual({
      ui_locale: "ru",
      public_client_endpoint_enabled: true,
      backup_path: "/srv/backups",
      quota_lock_mode: "disable_traffic"
    });
  });

  test("parses subscription plaintext and hands URLs to QR rendering", async () => {
    const user = userEvent.setup();
    renderAuthenticated([
      { url: "/api/v1/metrics", body: metricsFixture() },
      { url: "/api/v1/clients", body: [clientFixture({ id: "cl_1", name: "Client", subscription_token: "sub_secret" })] },
      { url: "/api/v1/clients/cl_1/locations", body: [] },
      {
        url: "/sub/sub_secret",
        headers: { "content-type": "text/plain; charset=utf-8" },
        body: "#name: Client\nolcrtc://wbstream?datachannel@room#key$Client / Main\n##name: Main\n"
      }
    ]);

    await user.click(await screen.findByRole("button", { name: /clients/i }));
    await user.click(await screen.findByRole("button", { name: /manage client client/i }));
    await user.click(screen.getByRole("button", { name: /load subscription/i }));

    expect(await screen.findByText("olcrtc://wbstream?datachannel@room#key$Client / Main")).toBeInTheDocument();
    expect(screen.getAllByTestId("qr-code")).toHaveLength(2);
    expect(parseSubscriptionUris("#x\nolcrtc://one\n##name: One")).toEqual(["olcrtc://one"]);
  });
});

function renderAuthenticated(extraResponses: MockResponse[]) {
  sessionStorage.setItem("olcpanel.session", JSON.stringify({ username: "admin", csrfToken: "csrf-token" }));
  return renderApp([
    { url: "/api/v1/state", body: { service: "olcpanel", api_version: "v1", setup_required: false, authenticated: true, bind_address: "127.0.0.1:8888" } },
    { url: "/api/v1/settings", body: { ui_locale: "en", public_client_endpoint_enabled: false, backup_path: "/var/lib/olcpanel/backups", quota_lock_mode: "stop" } },
    ...extraResponses
  ]);
}

function clientFixture(overrides: Record<string, unknown> = {}) {
  return {
    id: "cl_1",
    name: "Client",
    subscription_token: "sub_token",
    enabled: true,
    expires_at: null,
    quota_bytes: null,
    quota_used_bytes: 0,
    quota_state: "unlimited",
    expiry_state: "none",
    locations_count: 0,
    created_at: "2026-05-28T00:00:00Z",
    updated_at: "2026-05-28T00:00:00Z",
    ...overrides
  };
}

function metricsFixture() {
  return {
    generated_at: "2026-05-28T00:00:00Z",
    panel: { uptime_seconds: 120 },
    host: { cpu_percent: null, memory_total_bytes: null, memory_used_bytes: null, disk_total_bytes: null, disk_used_bytes: null },
    clients: { total: 0, enabled: 0, disabled: 0, expired: 0 },
    locations: { total: 0, enabled: 0, disabled: 0 },
    processes: { running: 0, stopped: 0, failed: 0, pending: 0 },
    traffic: { total_bytes: 0, rx_bytes: 0, tx_bytes: 0 },
    quotas: { warning: 0, exceeded: 0 },
    per_client: []
  };
}
