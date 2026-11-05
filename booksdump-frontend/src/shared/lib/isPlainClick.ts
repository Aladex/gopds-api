import type { MouseEvent } from 'react';

/** Whether a click on a link means "go here", not "open it somewhere else". */
export const isPlainClick = (event: MouseEvent): boolean =>
    !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && event.button === 0;
