export type Locale = "en" | "ru";

export type StateResponse = {
  service: string;
  api_version: string;
  setup_required: boolean;
  bind_address: string;
  authenticated?: boolean;
};

export type SessionResponse = {
  username: string;
  csrf_token: string;
};

export type Settings = {
  ui_locale: Locale;
  public_client_endpoint_enabled: boolean;
  backup_path: string;
  quota_lock_mode: "stop" | "disable_traffic";
};

export type Client = {
  id: string;
  name: string;
  subscription_token: string;
  enabled: boolean;
  expires_at: string | null;
  quota_bytes: number | null;
  quota_used_bytes: number;
  quota_state: "unlimited" | "within_limit" | "exceeded";
  expiry_state: "none" | "active" | "expired";
  locations_count: number;
  created_at: string;
  updated_at: string;
};

export type ClientInput = {
  name: string;
  enabled: boolean;
  expires_at: string | null;
  quota_bytes: number | null;
};

export type Provider = "telemost" | "wbstream" | "jitsi";
export type Transport = "datachannel" | "vp8channel" | "seichannel" | "videochannel";

export type Location = {
  id: string;
  client_id: string;
  name: string;
  enabled: boolean;
  provider: Provider;
  transport: Transport;
  transport_stability: "stable" | "unstable";
  room_id: string;
  crypto_key: string;
  transport_payload: Record<string, unknown>;
  dns: string;
  speed_limit_bps: number | null;
  runtime_status: "running" | "stopped" | "failed" | "pending" | string;
  created_at: string;
  updated_at: string;
};

export type LocationInput = {
  name: string;
  enabled: boolean;
  provider: Provider;
  transport: Transport;
  room_id: string;
  crypto_key: string;
  transport_payload: Record<string, unknown>;
  dns: string;
  speed_limit_bps: number | null;
};

export type MetricsSnapshot = {
  generated_at: string;
  panel: { uptime_seconds: number };
  host: {
    cpu_percent: number | null;
    memory_total_bytes: number | null;
    memory_used_bytes: number | null;
    disk_total_bytes: number | null;
    disk_used_bytes: number | null;
  };
  clients: { total: number; enabled: number; disabled: number; expired: number };
  locations: { total: number; enabled: number; disabled: number };
  processes: { running: number; stopped: number; failed: number; pending: number };
  traffic: { total_bytes: number; rx_bytes: number; tx_bytes: number };
  quotas: { warning: number; exceeded: number };
  per_client: Array<{
    client_id: string;
    name: string;
    traffic_bytes: number;
    rx_bytes: number;
    tx_bytes: number;
    quota_bytes: number | null;
    quota_warning: boolean;
    quota_exceeded: boolean;
    expired: boolean;
    locations: number;
    processes: { running: number; stopped: number; failed: number; pending: number };
  }>;
};

export type LogEntry = {
  time: string;
  level: string;
  source: string;
  client_id?: string;
  location_id?: string;
  message: string;
  attrs?: Record<string, unknown>;
};

export type LogResponse = {
  entries: LogEntry[];
};
