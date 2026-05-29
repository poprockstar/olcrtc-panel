import type { Provider, Transport } from "./types";

export const providers: Provider[] = ["telemost", "wbstream", "jitsi"];
export const transports: Transport[] = ["datachannel", "vp8channel", "seichannel", "videochannel"];

const providerTransports: Record<Provider, Transport[]> = {
  telemost: ["vp8channel", "videochannel"],
  wbstream: ["datachannel", "vp8channel", "seichannel", "videochannel"],
  jitsi: ["datachannel", "vp8channel", "seichannel", "videochannel"]
};

export function providerSupportsTransport(provider: Provider, transport: Transport): boolean {
  return providerTransports[provider].includes(transport);
}

export function transportsForProvider(provider: Provider): Transport[] {
  return providerTransports[provider];
}

export function parseSubscriptionUris(plainText: string): string[] {
  return plainText
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.startsWith("olcrtc://"));
}

export function formatBytes(value: number | null | undefined): string {
  if (value == null) {
    return "unlimited";
  }
  if (value === 0) {
    return "0 B";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`;
}

export function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) {
    return `${days}d ${hours}h`;
  }
  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  return `${minutes}m`;
}

export function asNumberOrNull(value: FormDataEntryValue | null): number | null {
  const text = String(value ?? "").trim();
  if (text === "") {
    return null;
  }
  const number = Number(text);
  return Number.isFinite(number) ? number : null;
}

export type TransportFormValues = Record<string, string>;

const payloadDefaults: Record<Transport, TransportFormValues> = {
  datachannel: {},
  vp8channel: { fps: "60", batch_size: "64" },
  seichannel: { fps: "60", batch_size: "64", fragment_size: "900", ack_timeout_ms: "2000" },
  videochannel: {
    codec: "qrcode",
    width: "1080",
    height: "1080",
    fps: "60",
    bitrate: "5000k",
    hw: "none",
    qr_recovery: "low",
    qr_size: "0",
    tile_module: "",
    tile_rs: ""
  }
};

const numericPayloadFields = new Set([
  "fps",
  "batch_size",
  "fragment_size",
  "ack_timeout_ms",
  "width",
  "height",
  "qr_size",
  "tile_module",
  "tile_rs"
]);

export function transportDefaults(transport: Transport): TransportFormValues {
  return { ...payloadDefaults[transport] };
}

export function formFromTransportPayload(transport: Transport, payload: Record<string, unknown>): TransportFormValues {
  const values = transportDefaults(transport);
  for (const [key, value] of Object.entries(payload)) {
    if (key in values && value != null) {
      values[key] = String(value);
    }
  }
  return values;
}

export function payloadFromTransportForm(transport: Transport, values: TransportFormValues): Record<string, unknown> {
  if (transport === "datachannel") {
    return {};
  }
  const payload: Record<string, unknown> = {};
  for (const key of Object.keys(payloadDefaults[transport])) {
    const raw = String(values[key] ?? "").trim();
    if (raw === "" && (key === "tile_module" || key === "tile_rs")) {
      continue;
    }
    if (numericPayloadFields.has(key)) {
      payload[key] = Number(raw);
    } else {
      payload[key] = raw;
    }
  }
  return payload;
}

export function hasAdvancedTransportPayload(transport: Transport, payload: Record<string, unknown>): boolean {
  const allowed = new Set(Object.keys(payloadDefaults[transport]));
  return Object.keys(payload).some((key) => !allowed.has(key));
}

export function validateAdvancedTransportJson(text: string): { ok: true; value: Record<string, unknown> } | { ok: false; error: string } {
  const trimmed = text.trim();
  if (!trimmed) {
    return { ok: true, value: {} };
  }
  try {
    const parsed = JSON.parse(trimmed);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return { ok: false, error: "Transport payload JSON must be a JSON object." };
    }
    return { ok: true, value: parsed as Record<string, unknown> };
  } catch {
    return { ok: false, error: "Transport payload JSON must be valid JSON." };
  }
}
