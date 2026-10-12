// Phones get panels that start closed. Matches Tailwind's md breakpoint.
export const NARROW_QUERY = "(max-width: 767px)";

export function isNarrowViewport(
  win: { matchMedia?: (query: string) => { matches: boolean } } | undefined,
): boolean {
  return win?.matchMedia?.(NARROW_QUERY).matches === true;
}
