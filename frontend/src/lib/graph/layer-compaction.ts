// Brandes-Köpf placement keeps long vertical runs aligned, which spreads a
// hub-shaped graph far wider than its widest layer needs. The canvas draws its
// own curves between node boxes and ignores ELK's edge routes, so large
// layouts are squeezed horizontally after ELK returns: each band of nodes
// keeps its left-to-right order (and so its crossings), shrinks toward the
// width of the widest band packed at `gap`, and never overlaps a neighbor.

export interface Box {
	x: number;
	y: number;
	width: number;
	height: number;
}

// Nodes whose vertical extents overlap share a band. Boxes in different
// bands cannot collide, so each band is packed on its own.
function bands(boxes: Box[]): number[][] {
	const order = boxes.map((_, index) => index).sort((a, b) => boxes[a].y - boxes[b].y);
	const out: number[][] = [];
	let bottom = -Infinity;
	for (const index of order) {
		const box = boxes[index];
		if (out.length === 0 || box.y >= bottom) {
			out.push([index]);
			bottom = box.y + box.height;
		} else {
			out[out.length - 1].push(index);
			bottom = Math.max(bottom, box.y + box.height);
		}
	}
	return out;
}

// Returns new x positions. Layouts already within `slack` of the packed width
// are returned unchanged.
export function compactLayers(boxes: Box[], gap: number, slack = 1.1): number[] {
	const xs = boxes.map((box) => box.x);
	if (boxes.length < 2) return xs;
	const left = Math.min(...boxes.map((box) => box.x));
	const right = Math.max(...boxes.map((box) => box.x + box.width));
	const groups = bands(boxes).map((band) => band.sort((a, b) => boxes[a].x - boxes[b].x));
	const target = Math.max(
		...groups.map((band) => band.reduce((sum, index) => sum + boxes[index].width, 0) + gap * (band.length - 1))
	);
	const span = right - left;
	if (span <= target * slack) return xs;
	const scale = target / span;
	for (const band of groups) {
		// Scale toward the left edge, then push right to clear each neighbor.
		let edge = -Infinity;
		for (const index of band) {
			const x = Math.max(left + (boxes[index].x - left) * scale, edge);
			xs[index] = x;
			edge = x + boxes[index].width + gap;
		}
		// Pull back inside the target width from the right.
		let limit = left + target;
		for (let i = band.length - 1; i >= 0; i--) {
			const index = band[i];
			const x = Math.min(xs[index], limit - boxes[index].width);
			xs[index] = x;
			limit = x - gap;
		}
	}
	return xs;
}

// Returns new y positions so the layout is at most `maxHeight` tall where
// possible. Polyline routing leaves extra room between layers for bends the
// canvas never draws. Gaps between bands shrink in proportion, never below
// `layerGap` (or their current size if already smaller), and nodes keep their
// offsets inside a band.
export function compactBands(boxes: Box[], layerGap: number, maxHeight: number): number[] {
	const ys = boxes.map((box) => box.y);
	const groups = bands(boxes).map((band) => ({
		band,
		top: Math.min(...band.map((index) => boxes[index].y)),
		bottom: Math.max(...band.map((index) => boxes[index].y + boxes[index].height))
	}));
	if (groups.length < 2) return ys;
	const gaps = groups.slice(1).map((group, i) => Math.max(0, group.top - groups[i].bottom));
	const height = groups[groups.length - 1].bottom - groups[0].top;
	if (height <= maxHeight) return ys;
	const totalGap = gaps.reduce((sum, gap) => sum + gap, 0);
	if (totalGap <= 0) return ys;
	const factor = Math.max(0, (maxHeight - (height - totalGap)) / totalGap);
	let shift = 0;
	for (let i = 0; i < groups.length; i++) {
		if (i > 0) {
			const gap = gaps[i - 1];
			const kept = Math.max(Math.min(gap, layerGap), gap * factor);
			shift += gap - kept;
		}
		for (const index of groups[i].band) ys[index] = boxes[index].y - shift;
	}
	return ys;
}
