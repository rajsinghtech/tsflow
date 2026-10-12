import { describe, expect, it } from "vitest";
import { isNarrowViewport, NARROW_QUERY } from "./viewport";

describe("isNarrowViewport", () => {
  it("follows the md breakpoint query", () => {
    const seen: string[] = [];
    const win = (matches: boolean) => ({
      matchMedia: (query: string) => {
        seen.push(query);
        return { matches };
      },
    });
    expect(isNarrowViewport(win(true))).toBe(true);
    expect(isNarrowViewport(win(false))).toBe(false);
    expect(seen).toEqual([NARROW_QUERY, NARROW_QUERY]);
  });

  it("treats a missing window or matchMedia as wide", () => {
    expect(isNarrowViewport(undefined)).toBe(false);
    expect(isNarrowViewport({})).toBe(false);
  });
});
