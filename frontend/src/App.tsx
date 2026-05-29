import type React from "react";
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Archive,
  BarChart3,
  CheckCircle2,
  Clipboard,
  Copy,
  Download,
  FileText,
  LogOut,
  RefreshCw,
  Save,
  Server,
  Settings as SettingsIcon,
  Smartphone,
  Upload,
  Users,
  XCircle
} from "lucide-react";
import QRCode from "qrcode";
import { api, ApiError, panelUrl } from "./api";
import {
  asNumberOrNull,
  formatBytes,
  formatUptime,
  parseSubscriptionUris,
  providerSupportsTransport,
  providers,
  transports,
  transportsForProvider
} from "./domain";
import { browserLocale, copy } from "./i18n";
import { clearStoredSession, loadStoredSession, saveStoredSession } from "./session";
import type { BackupRecord, Client, Location, Locale, MetricsSnapshot, Provider, Settings, Transport } from "./types";

type Screen = "dashboard" | "clients" | "logs" | "settings" | "backups";

type SessionState = {
  username: string;
  csrfToken: string;
};

export function App() {
  const queryClient = useQueryClient();
  const [session, setSession] = useState<SessionState | null>(() => loadStoredSession());
  const stateQuery = useQuery({ queryKey: ["state"], queryFn: api.state });
  const authenticated = Boolean(session && stateQuery.data?.authenticated);

  const settingsQuery = useQuery({
    queryKey: ["settings"],
    queryFn: api.settings,
    enabled: authenticated
  });
  const locale = settingsQuery.data?.ui_locale ?? browserLocale();

  const enterSession = (next: SessionState) => {
    setSession(next);
    saveStoredSession(next);
    void queryClient.invalidateQueries({ queryKey: ["state"] });
  };

  const leaveSession = () => {
    setSession(null);
    clearStoredSession();
    queryClient.clear();
  };

  if (stateQuery.isLoading) {
    return <LoadingPanel />;
  }

  if (stateQuery.error) {
    return <CenteredMessage title="OlcRTC Panel" message={errorMessage(stateQuery.error)} />;
  }

  if (stateQuery.data?.setup_required) {
    return <AuthScreen mode="setup" locale={locale} onSuccess={enterSession} />;
  }

  if (!authenticated) {
    return <AuthScreen mode="login" locale={locale} onSuccess={enterSession} />;
  }

  return (
    <AdminShell
      locale={locale}
      session={session!}
      settings={settingsQuery.data}
      onLogout={leaveSession}
    />
  );
}

function AuthScreen({
  mode,
  locale,
  onSuccess
}: {
  mode: "setup" | "login";
  locale: Locale;
  onSuccess: (session: SessionState) => void;
}) {
  const text = copy[locale];
  const mutation = useMutation({
    mutationFn: (payload: { username: string; password: string }) => (mode === "setup" ? api.setup(payload) : api.login(payload)),
    onSuccess: (response) => onSuccess({ username: response.username, csrfToken: response.csrf_token })
  });
  const [formError, setFormError] = useState("");

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError("");
    const data = new FormData(event.currentTarget);
    const username = String(data.get("username") ?? "").trim();
    const password = String(data.get("password") ?? "");
    if (username.length < 3) {
      setFormError("Username must be at least 3 characters.");
      return;
    }
    if (password.length < 12) {
      setFormError("Password must be at least 12 characters.");
      return;
    }
    mutation.mutate({ username, password });
  };

  return (
    <main className="auth-shell">
      <section className="auth-panel" aria-labelledby="auth-title">
        <div className="brand-lockup">
          <Server aria-hidden="true" />
          <span>OlcRTC Panel</span>
        </div>
        <h1 id="auth-title">{mode === "setup" ? text.setup : text.login}</h1>
        <form className="form-grid" onSubmit={submit}>
          <label>
            <span>{text.username}</span>
            <input name="username" autoComplete="username" />
          </label>
          <label>
            <span>{text.password}</span>
            <input name="password" type="password" autoComplete={mode === "setup" ? "new-password" : "current-password"} />
          </label>
          {(formError || mutation.error) && <p className="error-line">{formError || errorMessage(mutation.error)}</p>}
          <button className="primary-action" type="submit" disabled={mutation.isPending}>
            <CheckCircle2 aria-hidden="true" />
            {mode === "setup" ? text.createAdmin : text.signIn}
          </button>
        </form>
      </section>
    </main>
  );
}

function AdminShell({
  locale,
  session,
  settings,
  onLogout
}: {
  locale: Locale;
  session: SessionState;
  settings?: Settings;
  onLogout: () => void;
}) {
  const text = copy[locale];
  const [screen, setScreen] = useState<Screen>("dashboard");
  const queryClient = useQueryClient();
  const logout = useMutation({
    mutationFn: () => api.logout(session.csrfToken),
    onSettled: onLogout
  });
  const reload = useMutation({
    mutationFn: () => api.reload(session.csrfToken),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["metrics"] });
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
    }
  });
  const nav = [
    ["dashboard", BarChart3, text.dashboard],
    ["clients", Users, text.clients],
    ["logs", FileText, text.logs],
    ["settings", SettingsIcon, text.settings],
    ["backups", Archive, text.backups]
  ] as const;

  return (
    <main className="app-shell">
      <aside className="sidebar">
        <div className="brand-lockup">
          <Server aria-hidden="true" />
          <span>OlcRTC</span>
        </div>
        <nav aria-label="Main">
          {nav.map(([key, Icon, label]) => (
            <button key={key} className={screen === key ? "nav-item active" : "nav-item"} onClick={() => setScreen(key)}>
              <Icon aria-hidden="true" />
              {label}
            </button>
          ))}
        </nav>
        <div className="session-box">
          <span>{session.username}</span>
          <button className="icon-button" type="button" onClick={() => logout.mutate()} aria-label={text.logout} title={text.logout}>
            <LogOut aria-hidden="true" />
          </button>
        </div>
      </aside>

      <section className="workbench">
        <header className="topbar">
          <div>
            <h1>{nav.find(([key]) => key === screen)?.[2]}</h1>
            <p>127.0.0.1 local node</p>
          </div>
          <button className="secondary-action" type="button" onClick={() => reload.mutate()} disabled={reload.isPending}>
            <RefreshCw aria-hidden="true" />
            {text.reload}
          </button>
        </header>
        {reload.error && <p className="error-line">{errorMessage(reload.error)}</p>}
        {screen === "dashboard" && <Dashboard />}
        {screen === "clients" && <ClientsView csrfToken={session.csrfToken} settings={settings} />}
        {screen === "logs" && <LogsView />}
        {screen === "settings" && settings && <SettingsView csrfToken={session.csrfToken} settings={settings} />}
        {screen === "backups" && <BackupsView csrfToken={session.csrfToken} settings={settings} />}
      </section>
    </main>
  );
}

function Dashboard() {
  const metrics = useQuery({ queryKey: ["metrics"], queryFn: api.metrics, refetchInterval: 15000 });
  if (metrics.isLoading) {
    return <LoadingPanel />;
  }
  if (metrics.error) {
    return <CenteredMessage title="Dashboard unavailable" message={errorMessage(metrics.error)} />;
  }
  const snapshot = metrics.data;
  if (!snapshot) {
    return null;
  }
  return (
    <div className="screen-grid">
      <MetricGroup snapshot={snapshot} />
      <section className="panel-wide">
        <h2>Per-client traffic</h2>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Client</th>
                <th>Traffic</th>
                <th>Quota</th>
                <th>Locations</th>
                <th>Processes</th>
              </tr>
            </thead>
            <tbody>
              {snapshot.per_client.length === 0 ? (
                <tr><td colSpan={5}>No clients yet.</td></tr>
              ) : snapshot.per_client.map((client) => (
                <tr key={client.client_id}>
                  <td>{client.name}</td>
                  <td>{formatBytes(client.traffic_bytes)}</td>
                  <td><StatusBadge tone={client.quota_exceeded ? "bad" : client.quota_warning ? "warn" : "good"}>{client.quota_bytes == null ? "unlimited" : formatBytes(client.quota_bytes)}</StatusBadge></td>
                  <td>{client.locations}</td>
                  <td>{client.processes.running} running, {client.processes.failed} failed</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

function MetricGroup({ snapshot }: { snapshot: MetricsSnapshot }) {
  return (
    <div className="metrics-grid">
      <Metric icon={<Activity />} label="Uptime" value={formatUptime(snapshot.panel.uptime_seconds)} />
      <Metric icon={<Users />} label="Clients" value={`${snapshot.clients.enabled}/${snapshot.clients.total}`} detail={`${snapshot.clients.expired} expired`} />
      <Metric icon={<Server />} label="Locations" value={`${snapshot.locations.enabled}/${snapshot.locations.total}`} />
      <Metric icon={<RefreshCw />} label="Processes" value={`${snapshot.processes.running} running`} detail={`${snapshot.processes.failed} failed`} />
      <Metric icon={<BarChart3 />} label="Traffic" value={formatBytes(snapshot.traffic.total_bytes)} detail={`${formatBytes(snapshot.traffic.rx_bytes)} RX`} />
      <Metric icon={<XCircle />} label="Quota alerts" value={`${snapshot.quotas.exceeded} exceeded`} detail={`${snapshot.quotas.warning} warning`} />
      <Metric icon={<Smartphone />} label="CPU" value={snapshot.host.cpu_percent == null ? "n/a" : `${snapshot.host.cpu_percent.toFixed(1)}%`} />
      <Metric icon={<Archive />} label="Disk" value={hostRatio(snapshot.host.disk_used_bytes, snapshot.host.disk_total_bytes)} />
    </div>
  );
}

function Metric({ icon, label, value, detail }: { icon: React.ReactNode; label: string; value: string; detail?: string }) {
  return (
    <section className="metric-tile">
      <div className="metric-icon">{icon}</div>
      <span>{label}</span>
      <strong>{value}</strong>
      {detail && <small>{detail}</small>}
    </section>
  );
}

function ClientsView({ csrfToken, settings }: { csrfToken: string; settings?: Settings }) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const clients = useQuery({ queryKey: ["clients"], queryFn: api.clients });
  const selectedClient = clients.data?.find((client) => client.id === selectedId) ?? clients.data?.[0] ?? null;

  useEffect(() => {
    if (!selectedId && clients.data?.[0]) {
      setSelectedId(clients.data[0].id);
    }
  }, [clients.data, selectedId]);

  return (
    <div className="two-column">
      <section className="panel">
        <h2>Clients</h2>
        <ClientForm csrfToken={csrfToken} />
        <div className="list-stack">
          {clients.isLoading && <p>Loading clients...</p>}
          {clients.error && <p className="error-line">{errorMessage(clients.error)}</p>}
          {clients.data?.length === 0 && <p>No clients yet.</p>}
          {clients.data?.map((client) => (
            <button key={client.id} className={selectedClient?.id === client.id ? "row-button active" : "row-button"} onClick={() => setSelectedId(client.id)} aria-label={`Manage client ${client.name}`}>
              <span>{client.name}</span>
              <StatusBadge tone={client.enabled ? "good" : "bad"}>{client.enabled ? "enabled" : "disabled"}</StatusBadge>
            </button>
          ))}
        </div>
      </section>
      <section className="panel detail-panel">
        {selectedClient ? <ClientDetail client={selectedClient} csrfToken={csrfToken} settings={settings} /> : <p>Select or create a client.</p>}
      </section>
    </div>
  );
}

function ClientForm({ csrfToken }: { csrfToken: string }) {
  const queryClient = useQueryClient();
  const [error, setError] = useState("");
  const create = useMutation({
    mutationFn: (input: ReturnType<typeof clientInputFromForm>) => api.createClient(input, csrfToken),
    onSuccess: () => {
      setError("");
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
      void queryClient.invalidateQueries({ queryKey: ["metrics"] });
    },
    onError: (err) => setError(errorMessage(err))
  });

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    create.mutate(clientInputFromForm(new FormData(event.currentTarget)));
  };

  return (
    <form className="form-grid compact" onSubmit={submit}>
      <label>
        <span>Client name</span>
        <input name="name" />
      </label>
      <label>
        <span>Quota bytes</span>
        <input name="quota_bytes" inputMode="numeric" />
      </label>
      <label>
        <span>Expires at</span>
        <input name="expires_at" type="datetime-local" />
      </label>
      <label className="checkbox-line">
        <input name="enabled" type="checkbox" defaultChecked />
        <span>Enabled</span>
      </label>
      {error && <p className="error-line">{error}</p>}
      <button className="secondary-action" type="submit" disabled={create.isPending}>
        <Save aria-hidden="true" />
        Save client
      </button>
    </form>
  );
}

function ClientDetail({ client, csrfToken, settings }: { client: Client; csrfToken: string; settings?: Settings }) {
  const locations = useQuery({ queryKey: ["locations", client.id], queryFn: () => api.locations(client.id), enabled: Boolean(client.id) });
  return (
    <div className="detail-stack">
      <div className="detail-header">
        <div>
          <h2>{client.name}</h2>
          <p>{client.id}</p>
        </div>
        <StatusBadge tone={client.quota_state === "exceeded" || client.expiry_state === "expired" ? "bad" : "good"}>{client.quota_state}</StatusBadge>
      </div>
      <dl className="kv-grid">
        <div><dt>Subscription token</dt><dd>{client.subscription_token}</dd></div>
        <div><dt>Used quota</dt><dd>{formatBytes(client.quota_used_bytes)}</dd></div>
        <div><dt>Quota</dt><dd>{formatBytes(client.quota_bytes)}</dd></div>
        <div><dt>Expiry</dt><dd>{client.expiry_state}</dd></div>
      </dl>
      <SubscriptionPanel client={client} settings={settings} />
      <LocationForm clientId={client.id} csrfToken={csrfToken} />
      <div className="table-wrap">
        <table>
          <thead><tr><th>Name</th><th>Provider</th><th>Transport</th><th>Status</th><th>DNS</th></tr></thead>
          <tbody>
            {locations.data?.length === 0 && <tr><td colSpan={5}>No locations yet.</td></tr>}
            {locations.data?.map((location) => (
              <tr key={location.id}>
                <td>{location.name}</td>
                <td>{location.provider}</td>
                <td>{location.transport}</td>
                <td><StatusBadge tone={location.runtime_status === "failed" ? "bad" : location.runtime_status === "running" ? "good" : "muted"}>{location.runtime_status}</StatusBadge></td>
                <td>{location.dns}</td>
              </tr>
            ))}
            {locations.isLoading && <tr><td colSpan={5}>Loading locations...</td></tr>}
            {locations.error && <tr><td colSpan={5} className="error-line">{errorMessage(locations.error)}</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function LocationForm({ clientId, csrfToken }: { clientId: string; csrfToken: string }) {
  const queryClient = useQueryClient();
  const [provider, setProvider] = useState<Provider>("wbstream");
  const [transport, setTransport] = useState<Transport>("datachannel");
  const [error, setError] = useState("");
  const create = useMutation({
    mutationFn: (input: ReturnType<typeof locationInputFromForm>) => api.createLocation(clientId, input, csrfToken),
    onSuccess: () => {
      setError("");
      void queryClient.invalidateQueries({ queryKey: ["locations", clientId] });
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
      void queryClient.invalidateQueries({ queryKey: ["metrics"] });
    },
    onError: (err) => setError(errorMessage(err))
  });

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!providerSupportsTransport(provider, transport)) {
      setError("Transport is not supported by selected provider.");
      return;
    }
    create.mutate(locationInputFromForm(new FormData(event.currentTarget), provider, transport));
  };

  return (
    <form className="form-grid compact" onSubmit={submit}>
      <h3>Add location</h3>
      <label><span>Location name</span><input name="name" /></label>
      <label>
        <span>Provider</span>
        <select name="provider" value={provider} onChange={(event) => {
          const next = event.currentTarget.value as Provider;
          setProvider(next);
          if (!providerSupportsTransport(next, transport)) {
            setTransport(transportsForProvider(next)[0]);
          }
        }}>
          {providers.map((item) => <option key={item} value={item}>{item}</option>)}
        </select>
      </label>
      <label>
        <span>Transport</span>
        <select name="transport" value={transport} onChange={(event) => setTransport(event.currentTarget.value as Transport)}>
          {transports.map((item) => <option key={item} value={item}>{item}</option>)}
        </select>
      </label>
      <label><span>DNS</span><input name="dns" defaultValue="8.8.8.8:53" /></label>
      <label><span>Speed limit BPS</span><input name="speed_limit_bps" inputMode="numeric" /></label>
      <label><span>Transport payload JSON</span><textarea name="transport_payload" defaultValue="{}" /></label>
      <label className="checkbox-line"><input name="enabled" type="checkbox" defaultChecked /><span>Enabled</span></label>
      {error && <p className="error-line">{error}</p>}
      <button className="secondary-action" type="submit" disabled={create.isPending}>
        <Save aria-hidden="true" />
        Save location
      </button>
    </form>
  );
}

function SubscriptionPanel({ client, settings }: { client: Client; settings?: Settings }) {
  const subscriptionUrl = panelUrl(`/sub/${client.subscription_token}`);
  const publicUrl = panelUrl(`/c/${client.id}`);
  const [plainText, setPlainText] = useState("");
  const [selectedUri, setSelectedUri] = useState("");
  const subscription = useMutation({
    mutationFn: () => api.subscription(client.subscription_token),
    onSuccess: (text) => {
      setPlainText(text);
      setSelectedUri(parseSubscriptionUris(text)[0] ?? "");
    }
  });
  const uris = parseSubscriptionUris(plainText);

  return (
    <section className="sub-panel">
      <div className="detail-header">
        <h3>Subscription</h3>
        <button className="secondary-action" type="button" onClick={() => subscription.mutate()}>
          <Clipboard aria-hidden="true" />
          Load subscription
        </button>
      </div>
      <div className="copy-row">
        <code>{subscriptionUrl}</code>
        <CopyButton value={subscriptionUrl} label="Copy private subscription URL" />
      </div>
      {settings?.public_client_endpoint_enabled && <div className="copy-row"><code>{publicUrl}</code><CopyButton value={publicUrl} label="Copy public client URL" /></div>}
      {subscription.error && <p className="error-line">{errorMessage(subscription.error)}</p>}
      <div className="qr-grid">
        <QrCode value={subscriptionUrl} />
        {selectedUri && <QrCode value={selectedUri} />}
      </div>
      {uris.length > 0 && (
        <div className="uri-list">
          {uris.map((uri) => (
            <button key={uri} className={selectedUri === uri ? "row-button active" : "row-button"} onClick={() => setSelectedUri(uri)}>
              <span>{uri}</span>
              <Copy aria-hidden="true" />
            </button>
          ))}
        </div>
      )}
    </section>
  );
}

function QrCode({ value }: { value: string }) {
  const [src, setSrc] = useState("");
  useEffect(() => {
    let cancelled = false;
    QRCode.toDataURL(value, { margin: 1, width: 144 }).then((dataUrl) => {
      if (!cancelled) {
        setSrc(dataUrl);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [value]);
  return <img className="qr-code" data-testid="qr-code" src={src || undefined} alt="QR code" />;
}

function LogsView() {
  const [params, setParams] = useState(() => new URLSearchParams({ limit: "100" }));
  const logs = useQuery({ queryKey: ["logs", params.toString()], queryFn: () => api.logs(params) });
  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const next = new URLSearchParams();
    for (const key of ["level", "source", "client_id", "location_id", "q", "limit"]) {
      const value = String(data.get(key) ?? "").trim();
      if (value) {
        next.set(key, value);
      }
    }
    setParams(next);
  };
  const downloadText = async () => {
    const textParams = new URLSearchParams(params);
    textParams.set("format", "text");
    const text = await api.logsText(textParams);
    const blob = new Blob([text], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "olcpanel-logs.txt";
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <section className="panel-wide">
      <form className="filter-grid" onSubmit={submit}>
        <input name="level" placeholder="level" />
        <input name="source" placeholder="source" />
        <input name="client_id" placeholder="client id" />
        <input name="location_id" placeholder="location id" />
        <input name="q" placeholder="search" />
        <input name="limit" placeholder="limit" defaultValue="100" />
        <button className="secondary-action" type="submit">Apply</button>
        <button className="secondary-action" type="button" onClick={downloadText}><Copy aria-hidden="true" />Text</button>
      </form>
      {logs.error && <p className="error-line">{errorMessage(logs.error)}</p>}
      <div className="log-list">
        {logs.isLoading && <p>Loading logs...</p>}
        {logs.data?.entries.length === 0 && <p>No matching log entries.</p>}
        {logs.data?.entries.map((entry, index) => (
          <pre key={`${entry.time}-${index}`}>{JSON.stringify(entry, null, 2)}</pre>
        ))}
      </div>
    </section>
  );
}

function SettingsView({ csrfToken, settings }: { csrfToken: string; settings: Settings }) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState(settings);
  const save = useMutation({
    mutationFn: () => api.saveSettings(draft, csrfToken),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["settings"] })
  });

  useEffect(() => setDraft(settings), [settings]);

  return (
    <section className="panel">
      <form className="form-grid" onSubmit={(event) => { event.preventDefault(); save.mutate(); }}>
        <label><span>UI language</span><select value={draft.ui_locale} onChange={(event) => setDraft({ ...draft, ui_locale: event.currentTarget.value as Locale })}><option value="en">English</option><option value="ru">Русский</option></select></label>
        <label className="checkbox-line"><input type="checkbox" checked={draft.public_client_endpoint_enabled} onChange={(event) => setDraft({ ...draft, public_client_endpoint_enabled: event.currentTarget.checked })} /><span>Public client endpoint</span></label>
        <label><span>Backup path</span><input value={draft.backup_path} onChange={(event) => setDraft({ ...draft, backup_path: event.currentTarget.value })} /></label>
        <label><span>Quota lock mode</span><select value={draft.quota_lock_mode} onChange={(event) => setDraft({ ...draft, quota_lock_mode: event.currentTarget.value as Settings["quota_lock_mode"] })}><option value="stop">stop</option><option value="disable_traffic">disable_traffic</option></select></label>
        {save.error && <p className="error-line">{errorMessage(save.error)}</p>}
        <button className="primary-action" type="submit" disabled={save.isPending}><Save aria-hidden="true" />Save settings</button>
      </form>
    </section>
  );
}

function BackupsView({ csrfToken, settings }: { csrfToken: string; settings?: Settings }) {
  const queryClient = useQueryClient();
  const backups = useQuery({ queryKey: ["backups"], queryFn: api.backups });
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const create = useMutation({
    mutationFn: () => api.createBackup(csrfToken),
    onSuccess: () => {
      setMessage("Backup created.");
      setError("");
      void queryClient.invalidateQueries({ queryKey: ["backups"] });
    },
    onError: (err) => {
      setMessage("");
      setError(errorMessage(err));
    }
  });
  const restore = useMutation({
    mutationFn: (id: number) => api.restoreBackup(id, csrfToken),
    onSuccess: () => {
      setMessage("Backup restored.");
      setError("");
      void queryClient.invalidateQueries({ queryKey: ["backups"] });
      void queryClient.invalidateQueries({ queryKey: ["metrics"] });
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
    },
    onError: (err) => {
      setMessage("");
      setError(errorMessage(err));
    }
  });
  const importMutation = useMutation({
    mutationFn: (doc: unknown) => api.importPanel(doc, csrfToken),
    onSuccess: (result) => {
      setMessage(`Imported ${result.clients_created} clients and ${result.locations_created} locations.`);
      setError("");
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
      void queryClient.invalidateQueries({ queryKey: ["metrics"] });
    },
    onError: (err) => {
      setMessage("");
      setError(errorMessage(err));
    }
  });

  const restoreBackup = (record: BackupRecord) => {
    if (window.confirm(`Restore backup ${record.id}? This replaces the current database state.`)) {
      restore.mutate(record.id);
    }
  };
  const exportPanel = async () => {
    try {
      const doc = await api.exportPanel();
      const blob = new Blob([JSON.stringify(doc, null, 2)], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = "olcpanel-export.json";
      link.click();
      URL.revokeObjectURL(url);
      setMessage("Panel JSON exported.");
      setError("");
    } catch (err) {
      setMessage("");
      setError(errorMessage(err));
    }
  };
  const importPanel = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    if (!file) {
      return;
    }
    try {
      importMutation.mutate(JSON.parse(await file.text()));
    } catch {
      setMessage("");
      setError("Import file must be valid JSON.");
    } finally {
      event.currentTarget.value = "";
    }
  };

  return (
    <section className="panel-wide">
      <div className="detail-header">
        <h2>Backups</h2>
        <div className="button-row">
          <button className="secondary-action" type="button" onClick={() => create.mutate()} disabled={create.isPending}>
            <Archive aria-hidden="true" />
            Create backup
          </button>
          <button className="secondary-action" type="button" onClick={() => void exportPanel()}>
            <Download aria-hidden="true" />
            Export JSON
          </button>
          <label className="secondary-action file-action">
            <Upload aria-hidden="true" />
            Import JSON
            <input type="file" accept="application/json,.json" onChange={(event) => void importPanel(event)} />
          </label>
        </div>
      </div>
      <dl className="kv-grid">
        <div><dt>Configured backup path</dt><dd>{settings?.backup_path ?? "Loading..."}</dd></div>
      </dl>
      {message && <p className="success-line">{message}</p>}
      {error && <p className="error-line">{error}</p>}
      {backups.error && <p className="error-line">{errorMessage(backups.error)}</p>}
      <div className="table-wrap">
        <table>
          <thead>
            <tr><th>ID</th><th>File</th><th>Status</th><th>Size</th><th>Created</th><th>Action</th></tr>
          </thead>
          <tbody>
            {backups.isLoading && <tr><td colSpan={6}>Loading backups...</td></tr>}
            {backups.data?.length === 0 && <tr><td colSpan={6}>No backups yet.</td></tr>}
            {backups.data?.map((record) => (
              <tr key={record.id}>
                <td>{record.id}</td>
                <td>{baseName(record.path)}</td>
                <td><StatusBadge tone={record.status === "completed" ? "good" : record.status === "error" ? "bad" : "muted"}>{record.status}</StatusBadge></td>
                <td>{formatBytes(record.size_bytes)}</td>
                <td>{formatDate(record.created_at)}</td>
                <td>
                  <button className="secondary-action" type="button" onClick={() => restoreBackup(record)} disabled={restore.isPending || record.status !== "completed"} aria-label={`Restore backup ${record.id}`}>
                    <RefreshCw aria-hidden="true" />
                    Restore
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function StatusBadge({ tone, children }: { tone: "good" | "bad" | "warn" | "muted"; children: React.ReactNode }) {
  return <span className={`status-badge ${tone}`}>{children}</span>;
}

function CopyButton({ value, label }: { value: string; label: string }) {
  return <button className="icon-button" type="button" aria-label={label} title={label} onClick={() => void navigator.clipboard?.writeText(value)}><Copy aria-hidden="true" /></button>;
}

function LoadingPanel() {
  return <CenteredMessage title="OlcRTC Panel" message="Loading..." />;
}

function CenteredMessage({ title, message }: { title: string; message: string }) {
  return <main className="auth-shell"><section className="auth-panel"><h1>{title}</h1><p>{message}</p></section></main>;
}

function clientInputFromForm(data: FormData) {
  const expires = String(data.get("expires_at") ?? "").trim();
  return {
    name: String(data.get("name") ?? "").trim(),
    enabled: data.get("enabled") === "on",
    expires_at: expires ? new Date(expires).toISOString() : null,
    quota_bytes: asNumberOrNull(data.get("quota_bytes"))
  };
}

function locationInputFromForm(data: FormData, provider: Provider, transport: Transport) {
  let payload: Record<string, unknown> = {};
  try {
    payload = JSON.parse(String(data.get("transport_payload") ?? "{}"));
  } catch {
    payload = {};
  }
  return {
    name: String(data.get("name") ?? "").trim(),
    enabled: data.get("enabled") === "on",
    provider,
    transport,
    room_id: "",
    crypto_key: "",
    transport_payload: payload,
    dns: String(data.get("dns") ?? "").trim(),
    speed_limit_bps: asNumberOrNull(data.get("speed_limit_bps"))
  };
}

function hostRatio(used: number | null, total: number | null): string {
  if (used == null || total == null || total === 0) {
    return "n/a";
  }
  return `${Math.round((used / total) * 100)}%`;
}

function baseName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path;
}

function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "Unexpected error";
}
