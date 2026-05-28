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
