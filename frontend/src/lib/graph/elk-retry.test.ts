import { afterEach, describe, expect, it, vi } from "vitest";
import type { ElkNode } from "elkjs/lib/elk.bundled.js";
import {
  fastRetryGraph,
  layoutWithBudgetAndRetry,
  layoutWithRetry,
} from "./elk-retry";

function graph(placement: string, routing: string): ElkNode {
  return {
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.layered.nodePlacement.strategy": placement,
      "elk.edgeRouting": routing,
    },
    children: [{ id: "a", width: 10, height: 10 }],
    edges: [],
  };
}

const overflow = () =>
  Promise.reject(new RangeError("Maximum call stack size exceeded"));
const echo = async (input: ElkNode) => input;

afterEach(() => vi.restoreAllMocks());

describe("fastRetryGraph", () => {
  it("swaps network simplex and splines for the large-graph settings", () => {
    const input = graph("NETWORK_SIMPLEX", "SPLINES");
    const out = fastRetryGraph(input);
    expect(out?.layoutOptions).toEqual({
      "elk.algorithm": "layered",
      "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
      "elk.edgeRouting": "POLYLINE",
    });
    expect(out?.children).toBe(input.children);
    expect(input.layoutOptions?.["elk.layered.nodePlacement.strategy"]).toBe(
      "NETWORK_SIMPLEX",
    );
  });

  it("returns null when the graph already uses the large-graph settings", () => {
    expect(fastRetryGraph(graph("BRANDES_KOEPF", "POLYLINE"))).toBeNull();
    expect(fastRetryGraph({ id: "root" })).toBeNull();
  });

  it("only touches the setting that was slow", () => {
    const out = fastRetryGraph(graph("BRANDES_KOEPF", "SPLINES"));
    expect(out?.layoutOptions?.["elk.layered.nodePlacement.strategy"]).toBe(
      "BRANDES_KOEPF",
    );
    expect(out?.layoutOptions?.["elk.edgeRouting"]).toBe("POLYLINE");
  });
});

describe("layoutWithRetry", () => {
  it("uses the worker result when it works", async () => {
    const worker = { layout: vi.fn(echo) };
    const mainThread = vi.fn();
    const input = graph("NETWORK_SIMPLEX", "SPLINES");
    const out = await layoutWithRetry(input, worker, mainThread);
    expect(out).toEqual({ result: input, original: true });
    expect(worker.layout).toHaveBeenCalledOnce();
    expect(mainThread).not.toHaveBeenCalled();
  });

  it("retries a stack overflow in the worker with the large-graph settings", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const worker = {
      layout: vi.fn().mockImplementationOnce(overflow).mockImplementation(echo),
    };
    const mainThread = vi.fn();
    const out = await layoutWithRetry(
      graph("NETWORK_SIMPLEX", "SPLINES"),
      worker,
      mainThread,
    );
    expect(out.original).toBe(false);
    expect(
      out.result.layoutOptions?.["elk.layered.nodePlacement.strategy"],
    ).toBe("BRANDES_KOEPF");
    expect(worker.layout).toHaveBeenCalledTimes(2);
    expect(mainThread).not.toHaveBeenCalled();
  });

  it("never runs network simplex on the main thread after the worker fails", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(console, "error").mockImplementation(() => {});
    const worker = { layout: vi.fn(overflow) };
    const main = { layout: vi.fn(echo) };
    const out = await layoutWithRetry(
      graph("NETWORK_SIMPLEX", "SPLINES"),
      worker,
      () => main,
    );
    expect(out.original).toBe(false);
    const used = main.layout.mock.calls[0][0] as ElkNode;
    expect(used.layoutOptions?.["elk.layered.nodePlacement.strategy"]).toBe(
      "BRANDES_KOEPF",
    );
    expect(used.layoutOptions?.["elk.edgeRouting"]).toBe("POLYLINE");
  });

  it("keeps the old main-thread fallback for graphs already on the large-graph settings", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const worker = { layout: vi.fn(overflow) };
    const main = { layout: vi.fn(echo) };
    const input = graph("BRANDES_KOEPF", "POLYLINE");
    const out = await layoutWithRetry(input, worker, () => main);
    expect(out).toEqual({ result: input, original: true });
    expect(worker.layout).toHaveBeenCalledOnce();
  });

  it("passes the error on when there is no main-thread fallback", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const worker = { layout: vi.fn(overflow) };
    await expect(
      layoutWithRetry(graph("NETWORK_SIMPLEX", "SPLINES"), worker),
    ).rejects.toThrow(RangeError);
    expect(worker.layout).toHaveBeenCalledTimes(2);
    await expect(
      layoutWithRetry(graph("BRANDES_KOEPF", "POLYLINE"), worker),
    ).rejects.toThrow(RangeError);
    expect(worker.layout).toHaveBeenCalledTimes(3);
  });

  it("passes the error on when the main thread fails too", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(console, "error").mockImplementation(() => {});
    const worker = { layout: vi.fn(overflow) };
    const main = { layout: vi.fn(overflow) };
    await expect(
      layoutWithRetry(graph("NETWORK_SIMPLEX", "SPLINES"), worker, () => main),
    ).rejects.toThrow("Maximum call stack size exceeded");
  });
});

describe("layoutWithBudgetAndRetry", () => {
  const quality = graph("NETWORK_SIMPLEX", "SPLINES");

  it("keeps a quality layout that finishes in time", async () => {
    const out = await layoutWithBudgetAndRetry(quality, async (g) => ({
      result: g,
      quality: true,
    }));
    expect(out.quality).toBe(true);
    expect(
      out.result.layoutOptions?.["elk.layered.nodePlacement.strategy"],
    ).toBe("NETWORK_SIMPLEX");
  });

  it("reports a layout that ran out of time as not quality", async () => {
    const out = await layoutWithBudgetAndRetry(quality, async (g) => ({
      result: g,
      quality: false,
    }));
    expect(out.quality).toBe(false);
  });

  it("retries a crashed quality layout with the fast settings and reports it", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const budgeted = vi.fn(async (g: ElkNode) => {
      if (
        g.layoutOptions?.["elk.layered.nodePlacement.strategy"] ===
        "NETWORK_SIMPLEX"
      ) {
        throw new RangeError("Maximum call stack size exceeded");
      }
      return { result: g, quality: false };
    });
    const out = await layoutWithBudgetAndRetry(quality, budgeted);
    expect(budgeted).toHaveBeenCalledTimes(2);
    expect(out.quality).toBe(false);
    expect(
      out.result.layoutOptions?.["elk.layered.nodePlacement.strategy"],
    ).toBe("BRANDES_KOEPF");
  });

  it("uses the main thread when the worker cannot lay out even the fast graph", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(console, "error").mockImplementation(() => {});
    const out = await layoutWithBudgetAndRetry(quality, overflow, () => ({
      layout: echo,
    }));
    expect(out.quality).toBe(false);
    expect(out.result.layoutOptions?.["elk.edgeRouting"]).toBe("POLYLINE");
  });

  it("leaves a large-graph layout as not quality", async () => {
    const fast = graph("BRANDES_KOEPF", "POLYLINE");
    const out = await layoutWithBudgetAndRetry(fast, async (g) => ({
      result: g,
      quality: false,
    }));
    expect(out.quality).toBe(false);
  });
});
