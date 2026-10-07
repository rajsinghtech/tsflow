import { describe, expect, it, vi } from "vitest";

const get = vi.fn();
vi.mock("./api-service", () => ({
  api: { get: (...args: unknown[]) => get(...args) },
}));

const { tailscaleService } = await import("./tailscale-service");

describe("tailscaleService.getDevices", () => {
  it("turns null address lists into empty arrays", async () => {
    // An older backend sent null for a device with no addresses, and the
    // traffic graph then failed on `device.addresses` before rendering.
    get.mockResolvedValueOnce({
      devices: [
        { id: "n1", name: "bare.example.ts.net", addresses: null },
        { id: "n2", name: "web.example.ts.net", addresses: ["100.64.0.2"] },
      ],
    });
    const devices = await tailscaleService.getDevices();
    expect(devices.map((device) => device.addresses)).toEqual([
      [],
      ["100.64.0.2"],
    ]);
  });

  it("returns an empty list for a null device list", async () => {
    get.mockResolvedValueOnce({ devices: null });
    expect(await tailscaleService.getDevices()).toEqual([]);
  });
});
