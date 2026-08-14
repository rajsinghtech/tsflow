// Format bytes to human-readable string using SI units (1000-based)
// Network throughput conventionally uses powers of 1000, not 1024
export function formatBytes(bytes: number): string {
	if (bytes === 0) return '0 B';
	if (!Number.isFinite(bytes) || bytes < 0) return '0 B';

	const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
	const k = 1000;
	const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), units.length - 1);

	return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${units[i]}`;
}

// Format bytes per second rate (e.g., "1.2 MB/s")
export function formatBytesRate(bytesPerSecond: number): string {
	if (bytesPerSecond === 0) return '0 B/s';
	if (!Number.isFinite(bytesPerSecond) || bytesPerSecond < 0) return '0 B/s';
	return formatBytes(bytesPerSecond) + '/s';
}

// Format a throughput rate in bits per second (e.g., "1.2 Mbps"), the
// conventional unit for network throughput as opposed to data volume
export function formatBitsRate(bytesPerSecond: number): string {
	if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return '0 bps';

	const bits = bytesPerSecond * 8;
	const units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps'];
	const k = 1000;
	const i = Math.min(Math.floor(Math.log(bits) / Math.log(k)), units.length - 1);

	return `${parseFloat((bits / Math.pow(k, i)).toFixed(1))} ${units[i]}`;
}

// Format a duration in seconds to a compact human-readable string
export function formatDuration(seconds: number): string {
	if (seconds < 1) return '<1s';
	if (seconds < 60) return `${Math.round(seconds)}s`;
	if (seconds < 3600) {
		const m = Math.floor(seconds / 60);
		const s = Math.round(seconds % 60);
		return s > 0 ? `${m}m ${s}s` : `${m}m`;
	}
	if (seconds < 86400) {
		const h = Math.floor(seconds / 3600);
		const m = Math.round((seconds % 3600) / 60);
		return m > 0 ? `${h}h ${m}m` : `${h}h`;
	}
	const d = Math.floor(seconds / 86400);
	const h = Math.round((seconds % 86400) / 3600);
	return h > 0 ? `${d}d ${h}h` : `${d}d`;
}

// Format a timestamp as relative time (e.g., "5s ago", "2h ago")
export function formatRelativeTime(date: Date | string): string {
	const d = typeof date === 'string' ? new Date(date) : date;
	const diffMs = Date.now() - d.getTime();

	if (diffMs < 0) return 'just now';

	const seconds = Math.floor(diffMs / 1000);
	if (seconds < 5) return 'just now';
	if (seconds < 60) return `${seconds}s ago`;

	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m ago`;

	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h ago`;

	const days = Math.floor(hours / 24);
	return `${days}d ago`;
}

// Format date for display
export function formatDate(date: Date | string): string {
	const d = typeof date === 'string' ? new Date(date) : date;
	return d.toLocaleString();
}

// Format time-only for compact displays (log entries, etc.)
export function formatTime(date: Date | string): string {
	const d = typeof date === 'string' ? new Date(date) : date;
	return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

// Format a number with compact notation (e.g., 1.2K, 3.4M)
export function formatCompactNumber(n: number): string {
	if (n < 1000) return n.toString();
	if (n < 1_000_000) return `${(n / 1000).toFixed(1)}K`;
	if (n < 1_000_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
	return `${(n / 1_000_000_000).toFixed(1)}B`;
}
