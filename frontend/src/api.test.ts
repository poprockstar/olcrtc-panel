import { afterEach, describe, expect, test, vi } from "vitest";
import { api, panelUrl, setRuntimeBasePath } from "./api";

afterEach(() => {
  setRuntimeBasePath("");
  vi.unstubAllGlobals();
});

describe("API base path", () => {
  test("prefixes initial state requests from the runtime base path", async () => {
    setRuntimeBasePath("/panel");
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      service: "olcpanel",
      api_version: "v1",
      setup_required: true,
      bind_address: "0.0.0.0:8888",
      base_path: "/panel"
    })));
    vi.stubGlobal("fetch", fetchMock);

    await api.state();

    expect(fetchMock).toHaveBeenCalledWith("/panel/api/v1/state", expect.any(Object));
  });

  test("updates later requests from the state response base path", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/v1/state") {
        return new Response(JSON.stringify({
          service: "olcpanel",
          api_version: "v1",
          setup_required: false,
          bind_address: "0.0.0.0:8888",
          base_path: "/admin"
        }));
      }
      return new Response(JSON.stringify({
        ui_locale: "en",
        public_client_endpoint_enabled: false,
        backup_path: "/var/lib/olcpanel/backups",
        quota_lock_mode: "stop"
      }));
    });
    vi.stubGlobal("fetch", fetchMock);

    await api.state();
    await api.settings();

    expect(fetchMock).toHaveBeenLastCalledWith("/admin/api/v1/settings", expect.any(Object));
  });

  test("builds public panel URLs under the base path", () => {
    setRuntimeBasePath("/panel");

    expect(panelUrl("/sub/sub_secret")).toBe(`${window.location.origin}/panel/sub/sub_secret`);
    expect(panelUrl("/c/cl_1")).toBe(`${window.location.origin}/panel/c/cl_1`);
  });
});

describe("API keys", () => {
  test("lists, creates, and revokes API keys with CSRF where required", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/api/v1/api-keys" && (init?.method ?? "GET") === "GET") {
        return new Response(JSON.stringify([{ id: 1, name: "deploy", created_at: "2026-05-29T00:00:00Z" }]));
      }
      if (url === "/api/v1/api-keys" && init?.method === "POST") {
        return new Response(JSON.stringify({ id: 2, name: "automation", token: "olcp_api_secret" }));
      }
      if (url === "/api/v1/api-keys/2" && init?.method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      return new Response("unexpected", { status: 500 });
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.apiKeys()).resolves.toEqual([{ id: 1, name: "deploy", created_at: "2026-05-29T00:00:00Z" }]);
    await expect(api.createApiKey("automation", "csrf-token")).resolves.toMatchObject({ token: "olcp_api_secret" });
    await expect(api.revokeApiKey(2, "csrf-token")).resolves.toBeUndefined();

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/api-keys", expect.objectContaining({ method: "GET" }));
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/api-keys",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "X-CSRF-Token": "csrf-token" }),
        body: JSON.stringify({ name: "automation" })
      })
    );
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/api-keys/2",
      expect.objectContaining({ method: "DELETE", headers: expect.objectContaining({ "X-CSRF-Token": "csrf-token" }) })
    );
  });
});
