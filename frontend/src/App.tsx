import type React from "react";
import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Archive,
  CheckCircle2,
  Clipboard,
  Copy,
  Download,
  FileKey2,
  FileText,
  KeyRound,
  LogOut,
  Plus,
  RefreshCw,
  Save,
  Search,
  Server,
  Settings as SettingsIcon,
  ShieldCheck,
  Trash2,
  Upload,
  Users,
  X
} from "lucide-react";
import QRCode from "qrcode";
import { api, ApiError, panelUrl } from "./api";
import {
  asNumberOrNull,
  formatBytes,
  formatUptime,
  formFromTransportPayload,
  hasAdvancedTransportPayload,
  parseSubscriptionUris,
  payloadFromTransportForm,
  providerSupportsTransport,
  providers,
  transportDefaults,
  transports,
  validateAdvancedTransportJson
} from "./domain";
import { browserLocale, copy, type CopyText } from "./i18n";
import { clearStoredSession, loadStoredSession, saveStoredSession } from "./session";
import type {
  APIKey,
  BackupRecord,
  Client,
  ClientInput,
  Locale,
  Location,
  LocationInput,
  MetricsSnapshot,
  Provider,
  ReloadResult,
  Settings,
  Transport
} from "./types";

type Screen = "overview" | "clients" | "runtime" | "backups" | "security" | "settings";
type SessionState = { username: string; csrfToken: string };
type DrawerState =
  | { kind: "client"; client?: Client }
  | { kind: "location"; clientId: string; location?: Location }
  | null;
type ConfirmState =
  | { kind: "delete-client"; client: Client }
  | { kind: "delete-location"; clientId: string; location: Location }
  | { kind: "rotate"; client: Client }
  | null;

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
    return <LoadingPanel text={copy[locale]} />;
  }
  if (stateQuery.error) {
    return <CenteredMessage title="OlcRTC Panel" message={errorMessage(stateQuery.error, copy[locale])} />;
  }
  if (stateQuery.data?.setup_required) {
    return <AuthScreen mode="setup" locale={locale} onSuccess={enterSession} />;
  }
  if (!authenticated) {
    return <AuthScreen mode="login" locale={locale} onSuccess={enterSession} />;
  }

  return <AdminShell locale={locale} session={session!} settings={settingsQuery.data} onLogout={leaveSession} />;
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
          {(formError || mutation.error) && <p className="error-line">{formError || errorMessage(mutation.error, text)}</p>}
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
  const [screen, setScreen] = useState<Screen>("overview");
  const [lastReload, setLastReload] = useState<ReloadResult | null>(null);
  const queryClient = useQueryClient();
  const metrics = useQuery({ queryKey: ["metrics"], queryFn: api.metrics, refetchInterval: 15000 });
  const logout = useMutation({ mutationFn: () => api.logout(session.csrfToken), onSettled: onLogout });
  const reload = useMutation({
    mutationFn: () => api.reload(session.csrfToken),
    onSuccess: (result) => {
      setLastReload(result);
      void queryClient.invalidateQueries({ queryKey: ["locations"] });
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
    }
  });
  const nav = [
    ["overview", Activity, text.overview],
    ["clients", Users, text.clients],
    ["runtime", FileText, text.runtimeLogs],
    ["backups", Archive, text.backups],
    ["security", ShieldCheck, text.securityApiKeys],
    ["settings", SettingsIcon, text.settings]
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
            <p>{nodeHealthLabel(metrics.data, text)}</p>
          </div>
          <div className="topbar-actions">
            {lastReload && <ReloadSummary result={lastReload} text={text} />}
            {reload.error && <p className="error-line">{text.reloadFailed}: {errorMessage(reload.error, text)}</p>}
            <button className="secondary-action" type="button" onClick={() => reload.mutate()} disabled={reload.isPending}>
              <RefreshCw aria-hidden="true" />
              {text.reload}
            </button>
          </div>
        </header>
        {screen === "overview" && <Overview metrics={metrics} text={text} />}
        {screen === "clients" && <ClientsView csrfToken={session.csrfToken} settings={settings} text={text} />}
        {screen === "runtime" && <LogsView text={text} />}
        {screen === "backups" && <BackupsView csrfToken={session.csrfToken} settings={settings} text={text} />}
        {screen === "security" && <APIKeysView csrfToken={session.csrfToken} text={text} />}
        {screen === "settings" && settings && <SettingsView csrfToken={session.csrfToken} settings={settings} text={text} />}
      </section>
    </main>
  );
}

function Overview({ metrics, text }: { metrics: ReturnType<typeof useQuery<MetricsSnapshot>>; text: CopyText }) {
  if (metrics.isLoading) {
    return <LoadingPanel text={text} />;
  }
  if (metrics.error) {
    return <CenteredMessage title={text.overview} message={errorMessage(metrics.error, text)} />;
  }
  const snapshot = metrics.data;
  if (!snapshot) {
    return null;
  }
  return (
    <div className="screen-grid">
      <MetricGroup snapshot={snapshot} text={text} />
      <section className="panel-wide">
        <h2>{text.perClientTraffic}</h2>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{text.clients}</th>
                <th>{text.traffic}</th>
                <th>{text.quota}</th>
                <th>{text.locations}</th>
                <th>{text.processes}</th>
              </tr>
            </thead>
            <tbody>
              {snapshot.per_client.length === 0 ? (
                <tr><td colSpan={5}>{text.noClients}</td></tr>
              ) : snapshot.per_client.map((client) => (
                <tr key={client.client_id}>
                  <td>{client.name}</td>
                  <td>{formatBytes(client.traffic_bytes)}</td>
                  <td><StatusBadge tone={client.quota_exceeded ? "bad" : client.quota_warning ? "warn" : "good"}>{client.quota_bytes == null ? text.unlimited : formatBytes(client.quota_bytes)}</StatusBadge></td>
                  <td>{client.locations}</td>
                  <td>{client.processes.running} {text.running}, {client.processes.failed} {text.failed}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

function MetricGroup({ snapshot, text }: { snapshot: MetricsSnapshot; text: CopyText }) {
  return (
    <div className="metrics-grid">
      <Metric icon={<Activity />} label={text.uptime} value={formatUptime(snapshot.panel.uptime_seconds)} />
      <Metric icon={<Users />} label={text.clients} value={`${snapshot.clients.enabled}/${snapshot.clients.total}`} detail={`${snapshot.clients.expired} expired`} />
      <Metric icon={<Server />} label={text.locations} value={`${snapshot.locations.enabled}/${snapshot.locations.total}`} />
      <Metric icon={<RefreshCw />} label={text.processes} value={`${snapshot.processes.running} ${text.running}`} detail={`${snapshot.processes.failed} ${text.failed}`} />
      <Metric icon={<Activity />} label={text.traffic} value={formatBytes(snapshot.traffic.total_bytes)} detail={`${formatBytes(snapshot.traffic.rx_bytes)} RX`} />
      <Metric icon={<ShieldCheck />} label={text.quotaAlerts} value={`${snapshot.quotas.exceeded} exceeded`} detail={`${snapshot.quotas.warning} warning`} />
      <Metric icon={<Server />} label={text.cpu} value={snapshot.host.cpu_percent == null ? "n/a" : `${snapshot.host.cpu_percent.toFixed(1)}%`} />
      <Metric icon={<Archive />} label={text.disk} value={hostRatio(snapshot.host.disk_used_bytes, snapshot.host.disk_total_bytes)} />
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

function ClientsView({ csrfToken, settings, text }: { csrfToken: string; settings?: Settings; text: CopyText }) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [drawer, setDrawer] = useState<DrawerState>(null);
  const [confirm, setConfirm] = useState<ConfirmState>(null);
  const clients = useQuery({ queryKey: ["clients"], queryFn: api.clients });
  const filtered = useMemo(() => clients.data?.filter((client) => client.name.toLowerCase().includes(query.toLowerCase())) ?? [], [clients.data, query]);
  const selectedClient = clients.data?.find((client) => client.id === selectedId) ?? filtered[0] ?? clients.data?.[0] ?? null;

  useEffect(() => {
    if (!selectedId && clients.data?.[0]) {
      setSelectedId(clients.data[0].id);
    }
  }, [clients.data, selectedId]);

  return (
    <>
      <div className="clients-workspace">
        <section className="client-rail panel">
          <div className="section-title-row">
            <h2>{text.clients}</h2>
            <button className="icon-button" type="button" onClick={() => setDrawer({ kind: "client" })} aria-label={text.newClient} title={text.newClient}>
              <Plus aria-hidden="true" />
            </button>
          </div>
          <label className="search-field">
            <Search aria-hidden="true" />
            <span className="sr-only">{text.searchClients}</span>
            <input type="search" role="searchbox" aria-label={text.searchClients} value={query} onChange={(event) => setQuery(event.currentTarget.value)} />
          </label>
          <div className="list-stack">
            {clients.isLoading && <p>{text.loadingClients}</p>}
            {clients.error && <p className="error-line">{errorMessage(clients.error, text)}</p>}
            {clients.data?.length === 0 && <p>{text.noClients}</p>}
            {filtered.map((client) => (
              <button key={client.id} className={selectedClient?.id === client.id ? "client-row active" : "client-row"} onClick={() => setSelectedId(client.id)} aria-label={`Select client ${client.name}`}>
                <span>
                  <strong>{client.name}</strong>
                  <small>{client.locations_count} {text.locations}</small>
                </span>
                <StatusBadge tone={client.enabled ? "good" : "bad"}>{client.enabled ? text.enabled : text.disabled}</StatusBadge>
              </button>
            ))}
          </div>
        </section>
        <section className="client-detail">
          {selectedClient ? (
            <ClientDetail
              client={selectedClient}
              csrfToken={csrfToken}
              settings={settings}
              text={text}
              onEditClient={() => setDrawer({ kind: "client", client: selectedClient })}
              onDeleteClient={() => setConfirm({ kind: "delete-client", client: selectedClient })}
              onRotate={() => setConfirm({ kind: "rotate", client: selectedClient })}
              onAddLocation={() => setDrawer({ kind: "location", clientId: selectedClient.id })}
              onEditLocation={(location) => setDrawer({ kind: "location", clientId: selectedClient.id, location })}
              onDeleteLocation={(location) => setConfirm({ kind: "delete-location", clientId: selectedClient.id, location })}
            />
          ) : (
            <section className="panel empty-panel"><p>{text.selectOrCreateClient}</p></section>
          )}
        </section>
      </div>
      {drawer?.kind === "client" && <ClientDrawer csrfToken={csrfToken} client={drawer.client} text={text} onClose={() => setDrawer(null)} />}
      {drawer?.kind === "location" && <LocationDrawer csrfToken={csrfToken} clientId={drawer.clientId} location={drawer.location} text={text} onClose={() => setDrawer(null)} />}
      {confirm && <ConfirmModal state={confirm} csrfToken={csrfToken} text={text} onClose={() => setConfirm(null)} />}
    </>
  );
}

function ClientDetail({
  client,
  csrfToken,
  settings,
  text,
  onEditClient,
  onDeleteClient,
  onRotate,
  onAddLocation,
  onEditLocation,
  onDeleteLocation
}: {
  client: Client;
  csrfToken: string;
  settings?: Settings;
  text: CopyText;
  onEditClient: () => void;
  onDeleteClient: () => void;
  onRotate: () => void;
  onAddLocation: () => void;
  onEditLocation: (location: Location) => void;
  onDeleteLocation: (location: Location) => void;
}) {
  const locations = useQuery({ queryKey: ["locations", client.id], queryFn: () => api.locations(client.id), enabled: Boolean(client.id) });
  const enabledLocations = locations.data?.filter((location) => location.enabled) ?? [];
  return (
    <div className="detail-stack">
      <section className="panel">
        <div className="detail-header">
          <div>
            <h2>{client.name}</h2>
            <p>{client.id}</p>
          </div>
          <div className="button-row">
            <button className="secondary-action" type="button" onClick={onEditClient}>{text.editClient}</button>
            <button className="secondary-action" type="button" onClick={onRotate}><KeyRound aria-hidden="true" />{text.rotateCredentials}</button>
            <button className="danger-action" type="button" onClick={onDeleteClient}><Trash2 aria-hidden="true" />{text.deleteClient}</button>
          </div>
        </div>
        <dl className="kv-grid">
          <div><dt>{text.subscriptionToken}</dt><dd>{client.subscription_token}</dd></div>
          <div><dt>{text.usedQuota}</dt><dd>{formatBytes(client.quota_used_bytes)}</dd></div>
          <div><dt>{text.quota}</dt><dd>{formatBytes(client.quota_bytes)}</dd></div>
          <div><dt>{text.expiry}</dt><dd>{client.expiry_state}</dd></div>
        </dl>
      </section>
      <SubscriptionPanel client={client} settings={settings} locations={enabledLocations} locationsLoading={locations.isLoading} text={text} />
      <section className="panel-wide">
        <div className="detail-header">
          <h3>{text.locations}</h3>
          <button className="secondary-action" type="button" onClick={onAddLocation}><Plus aria-hidden="true" />{text.addLocation}</button>
        </div>
        <div className="location-grid">
          {locations.isLoading && <p>{text.loadingLocations}</p>}
          {locations.error && <p className="error-line">{errorMessage(locations.error, text)}</p>}
          {locations.data?.length === 0 && <p>{text.noLocations}</p>}
          {locations.data?.map((location) => (
            <article className="location-card" key={location.id}>
              <div className="detail-header">
                <div>
                  <h4>{location.name}</h4>
                  <p>{location.id}</p>
                </div>
                <StatusBadge tone={location.runtime_status === "failed" ? "bad" : location.runtime_status === "running" ? "good" : "muted"}>{location.runtime_status}</StatusBadge>
              </div>
              <dl className="mini-kv">
                <div><dt>{text.provider}</dt><dd>{location.provider}</dd></div>
                <div><dt>{text.transport}</dt><dd>{location.transport}</dd></div>
                <div><dt>{text.dns}</dt><dd>{location.dns}</dd></div>
                <div><dt>{text.speedLimitBps}</dt><dd>{formatBytes(location.speed_limit_bps)}</dd></div>
                <div><dt>{text.roomId}</dt><dd>{location.room_id}</dd></div>
                <div><dt>{text.stability}</dt><dd>{location.transport_stability}</dd></div>
                <div><dt>{text.updated}</dt><dd>{formatDate(location.updated_at)}</dd></div>
              </dl>
              <div className="button-row">
                <button className="secondary-action" type="button" onClick={() => onEditLocation(location)}>{text.editLocation}</button>
                <button className="danger-action" type="button" onClick={() => onDeleteLocation(location)}>{text.deleteLocation}</button>
              </div>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}

function ClientDrawer({ csrfToken, client, text, onClose }: { csrfToken: string; client?: Client; text: CopyText; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [error, setError] = useState("");
  const save = useMutation({
    mutationFn: (input: ClientInput) => client ? api.updateClient(client.id, input, csrfToken) : api.createClient(input, csrfToken),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
      onClose();
    },
    onError: (err) => setError(errorMessage(err, text))
  });
  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError("");
    save.mutate(clientInputFromForm(new FormData(event.currentTarget)));
  };
  return (
    <Drawer title={client ? text.editClient : text.newClient} onClose={onClose}>
      <form className="form-grid" onSubmit={submit}>
        <label><span>{text.clientName}</span><input name="name" defaultValue={client?.name ?? ""} /></label>
        <label><span>{text.quotaBytes}</span><input name="quota_bytes" inputMode="numeric" defaultValue={client?.quota_bytes ?? ""} /></label>
        <label><span>{text.expiresAt}</span><input name="expires_at" type="datetime-local" defaultValue={datetimeLocalValue(client?.expires_at)} /></label>
        <label className="checkbox-line"><input name="enabled" type="checkbox" defaultChecked={client?.enabled ?? true} /><span>{text.enabled}</span></label>
        {error && <p className="error-line">{error}</p>}
        <button className="primary-action" type="submit" disabled={save.isPending}><Save aria-hidden="true" />{client ? text.saveClient : text.createClient}</button>
      </form>
    </Drawer>
  );
}

function LocationDrawer({ csrfToken, clientId, location, text, onClose }: { csrfToken: string; clientId: string; location?: Location; text: CopyText; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [provider, setProvider] = useState<Provider>(location?.provider ?? "wbstream");
  const [transport, setTransport] = useState<Transport>(location?.transport ?? "datachannel");
  const [payloadValues, setPayloadValues] = useState(() => formFromTransportPayload(location?.transport ?? "datachannel", location?.transport_payload ?? {}));
  const [advanced, setAdvanced] = useState(() => Boolean(location && hasAdvancedTransportPayload(location.transport, location.transport_payload)));
  const [advancedText, setAdvancedText] = useState(() => location ? JSON.stringify(location.transport_payload, null, 2) : "{}");
  const [error, setError] = useState("");
  const save = useMutation({
    mutationFn: (input: LocationInput) =>
      location ? api.updateLocation(clientId, location.id, input, csrfToken) : api.createLocation(clientId, input, csrfToken),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["locations", clientId] });
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
      onClose();
    },
    onError: (err) => setError(errorMessage(err, text))
  });

  const changeTransport = (next: Transport) => {
    setTransport(next);
    setPayloadValues(transportDefaults(next));
    setAdvanced(false);
    setAdvancedText("{}");
  };
  const updatePayload = (key: string, value: string) => setPayloadValues((current) => ({ ...current, [key]: value }));
  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError("");
    if (!providerSupportsTransport(provider, transport)) {
      setError(text.unsupportedTransport);
      return;
    }
    const form = new FormData(event.currentTarget);
    const advancedPayload = advanced ? validateAdvancedTransportJson(advancedText) : null;
    if (advancedPayload && !advancedPayload.ok) {
      setError(advancedPayload.error.includes("valid") ? text.jsonMustBeValid : text.jsonMustBeObject);
      return;
    }
    save.mutate(locationInputFromForm(form, provider, transport, advancedPayload?.value ?? payloadFromTransportForm(transport, payloadValues)));
  };

  return (
    <Drawer title={location ? text.editLocation : text.addLocation} onClose={onClose}>
      <form className="form-grid" onSubmit={submit}>
        <label><span>{text.locationName}</span><input name="name" defaultValue={location?.name ?? ""} /></label>
        <div className="split-grid">
          <label>
            <span>{text.provider}</span>
            <select name="provider" value={provider} onChange={(event) => setProvider(event.currentTarget.value as Provider)}>
              {providers.map((item) => <option key={item} value={item}>{item}</option>)}
            </select>
          </label>
          <label>
            <span>{text.transport}</span>
            <select name="transport" value={transport} onChange={(event) => changeTransport(event.currentTarget.value as Transport)}>
              {transports.map((item) => <option key={item} value={item}>{item}</option>)}
            </select>
          </label>
        </div>
        <TransportPresetFields transport={transport} values={payloadValues} onChange={updatePayload} text={text} />
        <label><span>{text.dns}</span><input name="dns" defaultValue={location?.dns ?? "8.8.8.8:53"} /></label>
        <label><span>{text.speedLimitBps}</span><input name="speed_limit_bps" inputMode="numeric" defaultValue={location?.speed_limit_bps ?? ""} /></label>
        <label><span>{text.roomId}</span><input name="room_id" defaultValue={location?.room_id ?? ""} /></label>
        <label><span>{text.cryptoKey}</span><input name="crypto_key" defaultValue={location?.crypto_key ?? ""} /></label>
        <label className="checkbox-line"><input name="enabled" type="checkbox" defaultChecked={location?.enabled ?? true} /><span>{text.enabled}</span></label>
        <label className="checkbox-line"><input type="checkbox" checked={advanced} onChange={(event) => setAdvanced(event.currentTarget.checked)} /><span>{text.advancedJson}</span></label>
        {advanced && <label><span>{text.transportPayloadJson}</span><textarea name="transport_payload" value={advancedText} onChange={(event) => setAdvancedText(event.currentTarget.value)} /></label>}
        {error && <p className="error-line">{error}</p>}
        <button className="primary-action" type="submit" disabled={save.isPending}><Save aria-hidden="true" />{text.saveLocation}</button>
      </form>
    </Drawer>
  );
}

function TransportPresetFields({ transport, values, onChange, text }: { transport: Transport; values: Record<string, string>; onChange: (key: string, value: string) => void; text: CopyText }) {
  if (transport === "datachannel") {
    return <p className="muted-line">datachannel</p>;
  }
  if (transport === "vp8channel") {
    return (
      <div className="split-grid">
        <PayloadInput label={text.fps} field="fps" values={values} onChange={onChange} />
        <PayloadInput label={text.batchSize} field="batch_size" values={values} onChange={onChange} />
      </div>
    );
  }
  if (transport === "seichannel") {
    return (
      <div className="split-grid">
        <PayloadInput label={text.fps} field="fps" values={values} onChange={onChange} />
        <PayloadInput label={text.batchSize} field="batch_size" values={values} onChange={onChange} />
        <PayloadInput label={text.fragmentSize} field="fragment_size" values={values} onChange={onChange} />
        <PayloadInput label={text.ackTimeoutMs} field="ack_timeout_ms" values={values} onChange={onChange} />
      </div>
    );
  }
  return (
    <div className="split-grid">
      <label><span>{text.codec}</span><select value={values.codec} onChange={(event) => onChange("codec", event.currentTarget.value)}><option value="qrcode">qrcode</option><option value="tile">tile</option></select></label>
      <PayloadInput label={text.width} field="width" values={values} onChange={onChange} />
      <PayloadInput label={text.height} field="height" values={values} onChange={onChange} />
      <PayloadInput label={text.fps} field="fps" values={values} onChange={onChange} />
      <PayloadInput label={text.bitrate} field="bitrate" values={values} onChange={onChange} />
      <label><span>{text.hardware}</span><select value={values.hw} onChange={(event) => onChange("hw", event.currentTarget.value)}><option value="none">none</option><option value="nvenc">nvenc</option></select></label>
      <label><span>{text.qrRecovery}</span><select value={values.qr_recovery} onChange={(event) => onChange("qr_recovery", event.currentTarget.value)}><option value="low">low</option><option value="medium">medium</option><option value="high">high</option><option value="highest">highest</option></select></label>
      <PayloadInput label={text.qrSize} field="qr_size" values={values} onChange={onChange} />
      <PayloadInput label={text.tileModule} field="tile_module" values={values} onChange={onChange} />
      <PayloadInput label={text.tileRs} field="tile_rs" values={values} onChange={onChange} />
    </div>
  );
}

function PayloadInput({ label, field, values, onChange }: { label: string; field: string; values: Record<string, string>; onChange: (key: string, value: string) => void }) {
  return <label><span>{label}</span><input value={values[field] ?? ""} onChange={(event) => onChange(field, event.currentTarget.value)} /></label>;
}

function SubscriptionPanel({ client, settings, locations, locationsLoading, text }: { client: Client; settings?: Settings; locations: Location[]; locationsLoading: boolean; text: CopyText }) {
  const subscriptionUrl = panelUrl(`/sub/${client.subscription_token}`);
  const publicUrl = panelUrl(`/c/${client.id}`);
  const [plainText, setPlainText] = useState("");
  const [selectedUri, setSelectedUri] = useState("");
  const subscription = useMutation({
    mutationFn: () => api.subscription(client.subscription_token),
    onSuccess: (body) => {
      setPlainText(body);
      setSelectedUri(parseSubscriptionUris(body)[0] ?? "");
    }
  });
  const unavailable = subscriptionUnavailableReason(client, locations, locationsLoading, text);
  const uris = parseSubscriptionUris(plainText);

  return (
    <section className="sub-panel">
      <div className="detail-header">
        <h3>{text.subscription}</h3>
        <button className="secondary-action" type="button" onClick={() => subscription.mutate()} disabled={Boolean(unavailable)}>
          <Clipboard aria-hidden="true" />
          {text.loadSubscription}
        </button>
      </div>
      {unavailable && <p className="error-line">{unavailable}</p>}
      <LabeledCopyRow label={text.privateSubscriptionUrl} value={subscriptionUrl} copyLabel={text.copyPrivateSubscriptionUrl} />
      {settings?.public_client_endpoint_enabled && <LabeledCopyRow label={text.publicClientUrl} value={publicUrl} copyLabel={text.copyPublicClientUrl} />}
      {subscription.error && <p className="error-line">{errorMessage(subscription.error, text)}</p>}
      <div className="qr-grid">
        <QrCode value={subscriptionUrl} />
        {selectedUri && <QrCode value={selectedUri} />}
      </div>
      <div className="uri-list">
        <h4>{text.parsedUris}</h4>
        {uris.length === 0 && <p>{text.noUris}</p>}
        {uris.map((uri) => (
          <button key={uri} className={selectedUri === uri ? "row-button active" : "row-button"} onClick={() => setSelectedUri(uri)}>
            <span>{uri}</span>
            <Copy aria-hidden="true" />
          </button>
        ))}
      </div>
    </section>
  );
}

function LabeledCopyRow({ label, value, copyLabel }: { label: string; value: string; copyLabel: string }) {
  return (
    <div className="copy-row">
      <span>{label}</span>
      <code>{value}</code>
      <CopyButton value={value} label={copyLabel} />
    </div>
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

function ConfirmModal({ state, csrfToken, text, onClose }: { state: ConfirmState; csrfToken: string; text: CopyText; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [rotateToken, setRotateToken] = useState(false);
  const [rotateKeys, setRotateKeys] = useState(false);
  const [rotateRooms, setRotateRooms] = useState(false);
  const [error, setError] = useState("");
  const deleteClient = useMutation({
    mutationFn: (client: Client) => api.deleteClient(client.id, csrfToken),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
      onClose();
    },
    onError: (err) => setError(errorMessage(err, text))
  });
  const deleteLocation = useMutation({
    mutationFn: ({ clientId, locationId }: { clientId: string; locationId: string }) => api.deleteLocation(clientId, locationId, csrfToken),
    onSuccess: (_, variables) => {
      void queryClient.invalidateQueries({ queryKey: ["locations", variables.clientId] });
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
      onClose();
    },
    onError: (err) => setError(errorMessage(err, text))
  });
  const rotate = useMutation({
    mutationFn: async (client: Client) => {
      if (rotateToken && rotateKeys && !rotateRooms) {
        await api.rotateClient(client.id, { rotate_subscription_token: true }, csrfToken);
        return api.rotateClient(client.id, {}, csrfToken);
      }
      return api.rotateClient(client.id, {
        ...(rotateToken ? { rotate_subscription_token: true } : {}),
        ...(rotateRooms ? { rotate_rooms: true } : {})
      }, csrfToken);
    },
    onSuccess: (_, client) => {
      void queryClient.invalidateQueries({ queryKey: ["locations", client.id] });
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
      onClose();
    },
    onError: (err) => setError(errorMessage(err, text))
  });

  if (!state) {
    return null;
  }
  if (state.kind === "delete-client") {
    return (
      <Modal title={text.deleteClient} onClose={onClose}>
        <p>{text.confirmDeleteClient}</p>
        {error && <p className="error-line">{error}</p>}
        <div className="button-row">
          <button className="secondary-action" type="button" onClick={onClose}>{text.cancel}</button>
          <button className="danger-action" type="button" onClick={() => deleteClient.mutate(state.client)}>{text.delete}</button>
        </div>
      </Modal>
    );
  }
  if (state.kind === "delete-location") {
    return (
      <Modal title={text.deleteLocation} onClose={onClose}>
        <p>{text.confirmDeleteLocation}</p>
        {error && <p className="error-line">{error}</p>}
        <div className="button-row">
          <button className="secondary-action" type="button" onClick={onClose}>{text.cancel}</button>
          <button className="danger-action" type="button" onClick={() => deleteLocation.mutate({ clientId: state.clientId, locationId: state.location.id })}>{text.delete}</button>
        </div>
      </Modal>
    );
  }
  return (
    <Modal title={text.rotateCredentials} onClose={onClose}>
      <p>{text.confirmRotate}</p>
      <div className="form-grid compact">
        <label className="checkbox-line"><input type="checkbox" checked={rotateToken} onChange={(event) => setRotateToken(event.currentTarget.checked)} /><span>{text.rotateSubscriptionToken}</span></label>
        <label className="checkbox-line"><input type="checkbox" checked={rotateKeys} onChange={(event) => setRotateKeys(event.currentTarget.checked)} /><span>{text.rotateCryptoKeys}</span></label>
        <label className="checkbox-line"><input type="checkbox" checked={rotateRooms} onChange={(event) => { setRotateRooms(event.currentTarget.checked); if (event.currentTarget.checked) setRotateKeys(true); }} /><span>{text.rotateRooms}</span></label>
      </div>
      {error && <p className="error-line">{error}</p>}
      <div className="button-row">
        <button className="secondary-action" type="button" onClick={onClose}>{text.cancel}</button>
        <button className="primary-action" type="button" onClick={() => rotate.mutate(state.client)} disabled={!rotateToken && !rotateKeys && !rotateRooms}>{text.rotateNow}</button>
      </div>
    </Modal>
  );
}

function LogsView({ text }: { text: CopyText }) {
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
    const body = await api.logsText(textParams);
    const blob = new Blob([body], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "olcpanel-logs.txt";
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <section className="panel-wide">
      <form className="filter-grid" onSubmit={submit} aria-label={text.runtimeFilters}>
        <input name="level" placeholder="level" />
        <input name="source" placeholder="source" />
        <input name="client_id" placeholder="client id" />
        <input name="location_id" placeholder="location id" />
        <input name="q" placeholder="search" />
        <input name="limit" placeholder="limit" defaultValue="100" />
        <button className="secondary-action" type="submit">{text.apply}</button>
        <button className="secondary-action" type="button" onClick={() => void downloadText()}><Copy aria-hidden="true" />{text.text}</button>
      </form>
      {logs.error && <p className="error-line">{errorMessage(logs.error, text)}</p>}
      <div className="log-list">
        {logs.isLoading && <p>{text.loadingLogs}</p>}
        {logs.data?.entries.length === 0 && <p>{text.noLogs}</p>}
        {logs.data?.entries.map((entry, index) => <pre key={`${entry.time}-${index}`}>{JSON.stringify(entry, null, 2)}</pre>)}
      </div>
    </section>
  );
}

function APIKeysView({ csrfToken, text }: { csrfToken: string; text: CopyText }) {
  const queryClient = useQueryClient();
  const keys = useQuery({ queryKey: ["api-keys"], queryFn: api.apiKeys });
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const create = useMutation({
    mutationFn: (name: string) => api.createApiKey(name, csrfToken),
    onSuccess: (result) => {
      setToken(result.token);
      setError("");
      void queryClient.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (err) => setError(errorMessage(err, text))
  });
  const revoke = useMutation({
    mutationFn: (id: number) => api.revokeApiKey(id, csrfToken),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["api-keys"] }),
    onError: (err) => setError(errorMessage(err, text))
  });
  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const name = String(new FormData(event.currentTarget).get("name") ?? "").trim();
    create.mutate(name);
    event.currentTarget.reset();
  };
  return (
    <div className="screen-grid">
      <section className="panel">
        <div className="section-title-row"><h2>{text.apiKeys}</h2></div>
        <form className="form-grid compact" onSubmit={submit}>
          <label><span>{text.keyName}</span><input name="name" /></label>
          <button className="primary-action" type="submit" disabled={create.isPending}><FileKey2 aria-hidden="true" />{text.createApiKey}</button>
        </form>
        {token && <LabeledCopyRow label={text.oneTimeToken} value={token} copyLabel={text.oneTimeToken} />}
        {error && <p className="error-line">{error}</p>}
      </section>
      <section className="panel-wide">
        <div className="table-wrap">
          <table>
            <thead><tr><th>{text.keyName}</th><th>{text.created}</th><th>{text.lastUsed}</th><th>{text.revoked}</th><th>{text.revoke}</th></tr></thead>
            <tbody>
              {keys.isLoading && <tr><td colSpan={5}>Loading...</td></tr>}
              {keys.data?.length === 0 && <tr><td colSpan={5}>{text.noApiKeys}</td></tr>}
              {keys.data?.map((key) => <APIKeyRow key={key.id} apiKey={key} text={text} onRevoke={() => revoke.mutate(key.id)} />)}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

function APIKeyRow({ apiKey, text, onRevoke }: { apiKey: APIKey; text: CopyText; onRevoke: () => void }) {
  return (
    <tr>
      <td>{apiKey.name}</td>
      <td>{formatDate(apiKey.created_at)}</td>
      <td>{apiKey.last_used_at ? formatDate(apiKey.last_used_at) : text.never}</td>
      <td>{apiKey.revoked_at ? formatDate(apiKey.revoked_at) : "-"}</td>
      <td><button className="danger-action" type="button" onClick={onRevoke} disabled={Boolean(apiKey.revoked_at)}>{text.revoke}</button></td>
    </tr>
  );
}

function SettingsView({ csrfToken, settings, text }: { csrfToken: string; settings: Settings; text: CopyText }) {
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
        <label><span>{text.uiLanguage}</span><select value={draft.ui_locale} onChange={(event) => setDraft({ ...draft, ui_locale: event.currentTarget.value as Locale })}><option value="en">English</option><option value="ru">Русский</option></select></label>
        <label className="checkbox-line"><input type="checkbox" checked={draft.public_client_endpoint_enabled} onChange={(event) => setDraft({ ...draft, public_client_endpoint_enabled: event.currentTarget.checked })} /><span>{text.publicClientEndpoint}</span></label>
        <label><span>{text.backupPath}</span><input value={draft.backup_path} onChange={(event) => setDraft({ ...draft, backup_path: event.currentTarget.value })} /></label>
        <label><span>{text.quotaLockMode}</span><select value={draft.quota_lock_mode} onChange={(event) => setDraft({ ...draft, quota_lock_mode: event.currentTarget.value as Settings["quota_lock_mode"] })}><option value="stop">stop</option><option value="disable_traffic">disable_traffic</option></select></label>
        {save.error && <p className="error-line">{errorMessage(save.error, text)}</p>}
        <button className="primary-action" type="submit" disabled={save.isPending}><Save aria-hidden="true" />{text.saveSettings}</button>
      </form>
    </section>
  );
}

function BackupsView({ csrfToken, settings, text }: { csrfToken: string; settings?: Settings; text: CopyText }) {
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
      setError(errorMessage(err, text));
    }
  });
  const restore = useMutation({
    mutationFn: (id: number) => api.restoreBackup(id, csrfToken),
    onSuccess: () => {
      setMessage("Backup restored.");
      setError("");
      void queryClient.invalidateQueries({ queryKey: ["backups"] });
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
    },
    onError: (err) => {
      setMessage("");
      setError(errorMessage(err, text));
    }
  });
  const importMutation = useMutation({
    mutationFn: (doc: unknown) => api.importPanel(doc, csrfToken),
    onSuccess: (result) => {
      setMessage(`Imported ${result.clients_created} clients and ${result.locations_created} locations.`);
      setError("");
      void queryClient.invalidateQueries({ queryKey: ["clients"] });
    },
    onError: (err) => {
      setMessage("");
      setError(errorMessage(err, text));
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
      setError(errorMessage(err, text));
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
        <h2>{text.backups}</h2>
        <div className="button-row">
          <button className="secondary-action" type="button" onClick={() => create.mutate()} disabled={create.isPending}><Archive aria-hidden="true" />{text.createBackup}</button>
          <button className="secondary-action" type="button" onClick={() => void exportPanel()}><Download aria-hidden="true" />{text.exportJson}</button>
          <label className="secondary-action file-action"><Upload aria-hidden="true" />{text.importJson}<input type="file" accept="application/json,.json" onChange={(event) => void importPanel(event)} /></label>
        </div>
      </div>
      <dl className="kv-grid"><div><dt>{text.configuredBackupPath}</dt><dd>{settings?.backup_path ?? "Loading..."}</dd></div></dl>
      {message && <p className="success-line">{message}</p>}
      {error && <p className="error-line">{error}</p>}
      {backups.error && <p className="error-line">{errorMessage(backups.error, text)}</p>}
      <div className="table-wrap">
        <table>
          <thead><tr><th>ID</th><th>File</th><th>Status</th><th>Size</th><th>Created</th><th>Action</th></tr></thead>
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
                <td><button className="secondary-action" type="button" onClick={() => restoreBackup(record)} disabled={restore.isPending || record.status !== "completed"} aria-label={`Restore backup ${record.id}`}><RefreshCw aria-hidden="true" />{text.restore}</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Drawer({ title, children, onClose }: { title: string; children: React.ReactNode; onClose: () => void }) {
  return (
    <div className="drawer-layer">
      <div className="drawer-scrim" onClick={onClose} />
      <aside className="drawer" aria-labelledby="drawer-title">
        <div className="detail-header">
          <h2 id="drawer-title">{title}</h2>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close"><X aria-hidden="true" /></button>
        </div>
        {children}
      </aside>
    </div>
  );
}

function Modal({ title, children, onClose }: { title: string; children: React.ReactNode; onClose: () => void }) {
  return (
    <div className="modal-layer">
      <div className="modal-scrim" onClick={onClose} />
      <section className="modal-panel" role="dialog" aria-modal="true" aria-labelledby="modal-title">
        <div className="detail-header">
          <h2 id="modal-title">{title}</h2>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close"><X aria-hidden="true" /></button>
        </div>
        <div className="modal-body">{children}</div>
      </section>
    </div>
  );
}

function StatusBadge({ tone, children }: { tone: "good" | "bad" | "warn" | "muted"; children: React.ReactNode }) {
  return <span className={`status-badge ${tone}`}>{children}</span>;
}

function CopyButton({ value, label }: { value: string; label: string }) {
  return <button className="icon-button" type="button" aria-label={label} title={label} onClick={() => void navigator.clipboard?.writeText(value)}><Copy aria-hidden="true" /></button>;
}

function LoadingPanel({ text }: { text: CopyText }) {
  return <CenteredMessage title="OlcRTC Panel" message={text.loadingClients.replace("clients", "...")} />;
}

function CenteredMessage({ title, message }: { title: string; message: string }) {
  return <main className="auth-shell"><section className="auth-panel"><h1>{title}</h1><p>{message}</p></section></main>;
}

function ReloadSummary({ result, text }: { result: ReloadResult; text: CopyText }) {
  const { summary } = result;
  return <p className="reload-pill">{text.reloadSummary}: {summary.started} started, {summary.restarted} restarted, {summary.stopped} stopped, {summary.skipped} skipped</p>;
}

function nodeHealthLabel(snapshot: MetricsSnapshot | undefined, text: CopyText): string {
  if (!snapshot) {
    return text.nodeIdle;
  }
  if (snapshot.processes.failed > 0) {
    return text.nodeDegraded;
  }
  if (snapshot.processes.running > 0) {
    return text.nodeHealthy;
  }
  return text.nodeIdle;
}

function subscriptionUnavailableReason(client: Client, locations: Location[], loading: boolean, text: CopyText): string {
  if (!client.enabled) {
    return text.subscriptionDisabled;
  }
  if (client.expiry_state === "expired") {
    return text.subscriptionExpired;
  }
  if (client.quota_state === "exceeded") {
    return text.subscriptionQuotaExceeded;
  }
  if (!loading && locations.length === 0) {
    return text.subscriptionNoLocations;
  }
  return "";
}

function clientInputFromForm(data: FormData): ClientInput {
  const expires = String(data.get("expires_at") ?? "").trim();
  return {
    name: String(data.get("name") ?? "").trim(),
    enabled: data.get("enabled") === "on",
    expires_at: expires ? new Date(expires).toISOString() : null,
    quota_bytes: asNumberOrNull(data.get("quota_bytes"))
  };
}

function locationInputFromForm(data: FormData, provider: Provider, transport: Transport, payload: Record<string, unknown>): LocationInput {
  return {
    name: String(data.get("name") ?? "").trim(),
    enabled: data.get("enabled") === "on",
    provider,
    transport,
    room_id: String(data.get("room_id") ?? "").trim(),
    crypto_key: String(data.get("crypto_key") ?? "").trim(),
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

function datetimeLocalValue(value: string | null | undefined): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toISOString().slice(0, 16);
}

function errorMessage(error: unknown, text: CopyText): string {
  if (error instanceof ApiError) {
    return error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return text.unexpectedError;
}
