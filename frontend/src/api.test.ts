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
