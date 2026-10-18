import React from 'react';

/**
 * Telegram's paper plane, as an outline on lucide's grid rather than the filled
 * brand circle: in a row of line icons a solid disc would shout over the rest.
 * The artwork lives in this one file; replacing it changes nothing else.
 */
const TelegramIcon: React.FC<React.SVGProps<SVGSVGElement>> = (props) => (
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
        <path d="M21.5 3.5 2.8 10.7c-.9.4-.9 1.2 0 1.5l4.7 1.5 1.8 5.6c.2.6.9.8 1.4.4l2.6-2.2 4.9 3.6c.6.4 1.4.1 1.6-.6L22.8 4.9c.2-1-.5-1.7-1.3-1.4Z" />
        <path d="m7.5 13.7 10-6.2-7.5 7.2-.6 4.3" />
    </svg>
);

export default TelegramIcon;
