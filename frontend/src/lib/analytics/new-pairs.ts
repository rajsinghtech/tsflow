export interface NewPairCoverage {
	lookbackStart?: string;
	dataStart?: string | null;
	lookbackComplete?: boolean;
}

// lookbackNotice explains a lookback that reaches back before the oldest
// stored data: a pair seen only before then cannot be ruled out, so the list
// may include pairs that are not new. Null when the lookback is covered, or
// when the server did not report coverage.
export function lookbackNotice(coverage: NewPairCoverage | undefined, formatDate: (d: Date) => string): string | null {
	if (!coverage || coverage.lookbackComplete !== false) return null;
	const dataStart = coverage.dataStart ? new Date(coverage.dataStart) : null;
	if (!dataStart || Number.isNaN(dataStart.getTime())) {
		return 'No stored data covers the lookback, so every pair in the window is listed.';
	}
	return `Stored data starts ${formatDate(dataStart)}, inside the lookback. A pair last seen before then shows up here as new.`;
}
