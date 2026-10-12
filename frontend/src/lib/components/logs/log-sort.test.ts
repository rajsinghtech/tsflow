import { describe, expect, it } from "vitest";
import { ariaSort } from "./log-sort";

describe("ariaSort", () => {
  it("marks only the sorted column, with its direction", () => {
    expect(ariaSort("txBytes", "txBytes", "desc")).toBe("descending");
    expect(ariaSort("txBytes", "txBytes", "asc")).toBe("ascending");
    expect(ariaSort("rxBytes", "txBytes", "desc")).toBe("none");
  });
});
