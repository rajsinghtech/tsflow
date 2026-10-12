// Escape closes the open time range picker wherever focus is, including its
// own range input. A modified Escape is left alone.
export function closesPicker(
	event: Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey'>,
	open: boolean
): boolean {
	return open && event.key === 'Escape' && !event.metaKey && !event.ctrlKey && !event.altKey;
}
