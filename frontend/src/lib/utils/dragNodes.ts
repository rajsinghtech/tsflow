/**
 * Node dragging utilities for Sigma.js graphs
 * Enables intuitive drag-and-drop functionality for graph nodes
 */

import type Sigma from 'sigma';
import type Graph from 'graphology';

export interface DragState {
	isDragging: boolean;
	draggedNode: string | null;
	startX: number;
	startY: number;
}

export function setupNodeDragging(
	sigma: Sigma,
	graph: Graph,
	onDragStart?: (node: string) => void,
	onDrag?: (node: string) => void,
	onDragEnd?: (node: string) => void
): () => void {
	const dragState: DragState = {
		isDragging: false,
		draggedNode: null,
		startX: 0,
		startY: 0,
	};

	// Handle mouse down on node
	const handleDownNode = (event: { node: string }) => {
		dragState.isDragging = true;
		dragState.draggedNode = event.node;

		// Disable camera interaction during drag
		sigma.getCamera().disable();

		// Call callback
		onDragStart?.(event.node);

		// Change cursor
		const container = sigma.getContainer();
		container.style.cursor = 'grabbing';
	};

	// Handle mouse move
	const handleMouseMove = (coords: any) => {
		if (!dragState.isDragging || !dragState.draggedNode) return;

		// Convert viewport coordinates to graph coordinates
		const camera = sigma.getCamera();
		const graphCoords = sigma.viewportToGraph(coords);

		// Update node position
		graph.setNodeAttribute(dragState.draggedNode, 'x', graphCoords.x);
		graph.setNodeAttribute(dragState.draggedNode, 'y', graphCoords.y);

		// Call drag callback to update overlays in real-time
		onDrag?.(dragState.draggedNode);
	};

	// Handle mouse up/release
	const handleMouseUp = () => {
		if (dragState.isDragging && dragState.draggedNode) {
			const node = dragState.draggedNode;

			// Re-enable camera interaction
			sigma.getCamera().enable();

			// Call callback
			onDragEnd?.(node);

			// Reset cursor
			const container = sigma.getContainer();
			container.style.cursor = 'default';
		}

		dragState.isDragging = false;
		dragState.draggedNode = null;
	};

	// Handle mouse leaving the container
	const handleMouseLeave = () => {
		if (dragState.isDragging) {
			handleMouseUp();
		}
	};

	// Attach event listeners
	sigma.on('downNode', handleDownNode);
	sigma.getMouseCaptor().on('mousemove', handleMouseMove);
	sigma.getMouseCaptor().on('mouseup', handleMouseUp);
	sigma.getMouseCaptor().on('mouseleave', handleMouseLeave);

	// Return cleanup function
	return () => {
		sigma.off('downNode', handleDownNode);
		sigma.getMouseCaptor().off('mousemove', handleMouseMove);
		sigma.getMouseCaptor().off('mouseup', handleMouseUp);
		sigma.getMouseCaptor().off('mouseleave', handleMouseLeave);
	};
}
