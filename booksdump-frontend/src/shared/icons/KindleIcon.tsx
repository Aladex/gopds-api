import React from 'react';

/**
 * An e-reader: a screen with lines of text and the chin below it.
 *
 * Drawn on lucide's grid — 24 units, a 2-unit round stroke in currentColor — so
 * it sits beside the lucide icons in the card's action row without looking
 * borrowed. The artwork lives in this one file; replacing it changes nothing
 * else.
 */
const KindleIcon: React.FC<React.SVGProps<SVGSVGElement>> = (props) => (
    <svg
        xmlns="http://www.w3.org/2000/svg"
        width="24"
        height="24"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        {...props}
    >
        <rect x="5" y="2" width="14" height="20" rx="2" />
        <path d="M8.5 6h7M8.5 9.5h7M8.5 13h4.5" />
        <path d="M11 18.5h2" />
    </svg>
);

export default KindleIcon;
