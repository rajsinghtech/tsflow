import { handleLayoutMessage, type LayoutRequest } from './cheap-layout';

const scope = self as unknown as {
	onmessage: ((event: MessageEvent<LayoutRequest>) => void) | null;
	postMessage: (message: unknown) => void;
};

scope.onmessage = (event) => {
	scope.postMessage(handleLayoutMessage(event.data));
};
