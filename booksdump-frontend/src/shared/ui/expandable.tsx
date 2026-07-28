import * as React from 'react';

import { cn } from '@/shared/lib/utils';

/**
 * Expandable animates between a fixed-height peek and the full content.
 *
 * The obvious approaches do not animate at all: `display` and
 * `-webkit-line-clamp` are not interpolatable properties, and `height: auto`
 * has no numeric target to transition towards. So the height is measured and
 * driven explicitly:
 *
 *   opening — from the peek height to the measured scrollHeight, then released
 *             to `none` once the transition ends, so later reflows (a font
 *             loading, the window narrowing) are not clipped;
 *   closing — pinned back to the measured height first and read back to force
 *             the layout, then animated down to the peek height. Both halves
 *             matter: without the pin, and without making the browser take it
 *             before the peek height arrives, the panel jumps straight from
 *             `none` to the peek height in a single frame.
 *
 * Readers who asked for less motion get the same states without the transition.
 */

type Phase = 'collapsed' | 'opening' | 'open' | 'closing';

export interface ExpandableProps extends React.HTMLAttributes<HTMLDivElement> {
    open: boolean;
    /** How many lines of the content stay visible while collapsed. */
    peekLines?: number;
    /** Fade the bottom edge of the peek instead of cutting text mid-line. */
    fade?: boolean;
    children: React.ReactNode;
}

function prefersReducedMotion(): boolean {
    return typeof window !== 'undefined'
        && typeof window.matchMedia === 'function'
        && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

export const Expandable = React.forwardRef<HTMLDivElement, ExpandableProps>(
    ({ open, peekLines = 2, fade = true, className, children, ...rest }, forwardedRef) => {
        const innerRef = React.useRef<HTMLDivElement | null>(null);
        const [phase, setPhase] = React.useState<Phase>(open ? 'open' : 'collapsed');
        const [maxHeight, setMaxHeight] = React.useState<string | undefined>(undefined);

        const setRefs = React.useCallback(
            (node: HTMLDivElement | null) => {
                innerRef.current = node;
                if (typeof forwardedRef === 'function') {
                    forwardedRef(node);
                } else if (forwardedRef) {
                    (forwardedRef as React.MutableRefObject<HTMLDivElement | null>).current = node;
                }
            },
            [forwardedRef],
        );

        /** peekHeight is the collapsed height, derived from the real line height. */
        const peekHeight = React.useCallback(() => {
            const node = innerRef.current;
            if (!node) return 0;
            const lineHeight = parseFloat(getComputedStyle(node).lineHeight);
            const line = Number.isFinite(lineHeight) ? lineHeight : 21;
            return line * peekLines;
        }, [peekLines]);

        React.useEffect(() => {
            const node = innerRef.current;
            if (!node) return;

            if (prefersReducedMotion()) {
                setPhase(open ? 'open' : 'collapsed');
                setMaxHeight(open ? undefined : `${peekHeight()}px`);
                return;
            }

            if (open) {
                setPhase('opening');
                setMaxHeight(`${node.scrollHeight}px`);
                return;
            }

            // Pin the current height so there is something to animate down
            // from. The pin is written to the node and read back, rather than
            // set as state: React would commit it and the peek height in the
            // same frame, the browser would only ever compute the second, and
            // the panel would snap shut with no transition at all.
            node.style.maxHeight = `${node.scrollHeight}px`;
            void node.offsetHeight;

            setPhase('closing');
            setMaxHeight(`${peekHeight()}px`);
        }, [open, peekHeight]);

        // First paint: collapse without animating from nothing.
        React.useEffect(() => {
            if (!open && maxHeight === undefined && innerRef.current) {
                setMaxHeight(`${peekHeight()}px`);
            }
            // eslint-disable-next-line react-hooks/exhaustive-deps
        }, []);

        const handleTransitionEnd = (event: React.TransitionEvent<HTMLDivElement>) => {
            if (event.propertyName !== 'max-height') return;
            if (phase === 'opening') {
                setPhase('open');
                setMaxHeight(undefined);
            } else if (phase === 'closing') {
                setPhase('collapsed');
            }
        };

        const clipped = phase === 'collapsed' || phase === 'closing';

        return (
            <div
                ref={setRefs}
                onTransitionEnd={handleTransitionEnd}
                data-state={open ? 'open' : 'collapsed'}
                style={{ maxHeight }}
                className={cn(
                    'relative overflow-hidden transition-[max-height] duration-300 ease-out motion-reduce:transition-none',
                    fade
                        && clipped
                        && 'after:pointer-events-none after:absolute after:inset-x-0 after:bottom-0 after:h-6 after:bg-gradient-to-b after:from-transparent after:to-card',
                    className,
                )}
                {...rest}
            >
                {children}
            </div>
        );
    },
);

Expandable.displayName = 'Expandable';
