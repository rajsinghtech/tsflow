import { describe, expect, it } from "vitest";
import type { NetworkNode } from "#lib/types";
import type { RenderModel } from "./aggregate";
import { modelToFlow } from "./elk-place";

describe("modelToFlow aria names", () => {
  it("names grouped cards and their edges by label and hostname", () => {
    const plex = { id: "nPlex0001CNTRL", displayName: "plex" } as NetworkNode;
    const model: RenderModel = {
      nodes: [
        {
          id: "group:tag:k8s",
          kind: "group",
          label: "tag:k8s",
          sublabel: "",
          totalBytes: 10,
          connections: 1,
          memberCount: 40,
          memberIds: [],
        },
        {
          id: plex.id,
          kind: "device",
          label: "plex",
          sublabel: "",
          totalBytes: 10,
          connections: 1,
          memberCount: 1,
          memberIds: [plex.id],
          deviceId: plex.id,
        },
      ],
      edges: [
        {
          id: "e1",
          source: "group:tag:k8s",
          target: plex.id,
          totalBytes: 3_000_000,
          txBytes: 2_000_000,
          rxBytes: 1_000_000,
          trafficType: "virtual",
        },
      ],
      deviceCount: 41,
      groupCount: 1,
    };
    const flow = modelToFlow(model, new Map([[plex.id, plex]]));
    expect(flow.nodes.map((node) => node.ariaLabel)).toEqual([
      "tag:k8s, 40 devices",
      "plex",
    ]);
    expect(flow.edges[0].ariaLabel).toBe("tag:k8s to plex, 3 MB");
  });
});
