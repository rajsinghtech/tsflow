// aria-sort for a Network Logs column header.
export type SortDirection = "asc" | "desc";

export function ariaSort<F extends string>(
  field: F,
  current: F,
  dir: SortDirection,
): "ascending" | "descending" | "none" {
  if (field !== current) return "none";
  return dir === "asc" ? "ascending" : "descending";
}
