// Throughput for one bandwidth bucket. Edge buckets cover only the overlap
// between the nominal bucket and the query window, so dividing by the full
// bucket length understates the rate.
export function coveredBucketSeconds(bucketSeconds: number, coveredSeconds?: number): number {
	if (coveredSeconds !== undefined && coveredSeconds > 0) {
		return coveredSeconds;
	}
	return Math.max(bucketSeconds, 1);
}

export function bytesPerSecond(bytes: number, bucketSeconds: number, coveredSeconds?: number): number {
	return bytes / coveredBucketSeconds(bucketSeconds, coveredSeconds);
}
