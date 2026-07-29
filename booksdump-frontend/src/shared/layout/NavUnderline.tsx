import React from 'react';

import { cn } from '@/shared/lib/utils';
import type { UnderlineBox } from '@/shared/hooks/useTravellingUnderline';

type NavUnderlineProps = {
    /** Where the bar belongs, or null when nothing is current. */
    box: UnderlineBox | null;
    /** False until the first measurement, so the bar is placed rather than slid. */
    placed: boolean;
    /** Tailwind colour for the bar; the two navigations sit on different surfaces. */
    className?: string;
};

/**
 * The bar that marks the current section, drawn once per navigation and moved.
 *
 * Decorative on purpose: which link is current is already said by aria-current
 * on the link itself, so repeating it here would only give a screen reader the
 * same fact twice.
 */
const NavUnderline: React.FC<NavUnderlineProps> = ({ box, placed, className }) => (
    <span
        aria-hidden
        className={cn(
            'pointer-events-none absolute h-0.5',
            placed &&
                'transition-[left,top,width,opacity] duration-300 ease-out motion-reduce:transition-none',
            box ? 'opacity-100' : 'opacity-0',
            className,
        )}
        style={{
            left: box?.left ?? 0,
            top: box?.top ?? 0,
            width: box?.width ?? 0,
        }}
    />
);

export default NavUnderline;
