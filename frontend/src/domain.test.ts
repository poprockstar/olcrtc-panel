import { describe, expect, test } from "vitest";
import {
  formFromTransportPayload,
  payloadFromTransportForm,
  transportDefaults,
  validateAdvancedTransportJson
} from "./domain";

describe("transport payload presets", () => {
  test("builds exact payloads for each transport preset", () => {
    expect(payloadFromTransportForm("datachannel", {})).toEqual({});
    expect(payloadFromTransportForm("vp8channel", { fps: "30", batch_size: "32" })).toEqual({ fps: 30, batch_size: 32 });
    expect(payloadFromTransportForm("seichannel", { fps: "24", batch_size: "16", fragment_size: "512", ack_timeout_ms: "1500" })).toEqual({
      fps: 24,
      batch_size: 16,
      fragment_size: 512,
      ack_timeout_ms: 1500
    });
    expect(payloadFromTransportForm("videochannel", {
      codec: "tile",
      width: "1280",
      height: "720",
      fps: "25",
      bitrate: "3500k",
      hw: "nvenc",
      qr_recovery: "high",
      qr_size: "5",
      tile_module: "12",
      tile_rs: "4"
    })).toEqual({
      codec: "tile",
      width: 1280,
      height: 720,
      fps: 25,
      bitrate: "3500k",
      hw: "nvenc",
      qr_recovery: "high",
      qr_size: 5,
      tile_module: 12,
      tile_rs: 4
    });
  });

  test("hydrates known payloads into preset forms and falls back to defaults", () => {
    expect(transportDefaults("seichannel")).toMatchObject({ fps: "60", batch_size: "64" });
    expect(formFromTransportPayload("vp8channel", { fps: 45 })).toMatchObject({ fps: "45", batch_size: "64" });
  });

  test("validates advanced JSON as an object", () => {
    expect(validateAdvancedTransportJson('{"fps":30}')).toEqual({ ok: true, value: { fps: 30 } });
    expect(validateAdvancedTransportJson("[1,2]")).toMatchObject({ ok: false });
    expect(validateAdvancedTransportJson("{bad")).toMatchObject({ ok: false });
  });
});
