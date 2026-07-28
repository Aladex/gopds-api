import * as React from 'react';

import { cn } from '@/shared/lib/utils';

/**
 * Expandable animates between a fixed-height peek and the full content.
 *
 * It animates only what the reader changes. Arriving already open, or already
 * collapsed, is not a transition — the collapsed height is CSS, in the
 * element's own line heights, so the first paint is correct without measuring
 * anything.
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

        /**
         * The collapsed height, in the element's own line heights.
         *
         * Expressed in CSS rather than measured, so it is already right on the
         * first paint. Measuring means rendering once unclamped and correcting
         * afterwards, which is a frame of full-height content followed by a
         * collapse the reader never asked for.
         */
        const collapsedHeight = `${peekLines}lh`;

        const [phase, setPhase] = React.useState<Phase>(open ? 'open' : 'collapsed');
        const [maxHeight, setMaxHeight] = React.useState<string | undefined>(
            open ? undefined : collapsedHeight,
        );

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

        /**
         * Whether `open` has actually changed since mounting.
         *
         * A panel arrives in the state it was asked for, and animating into it
         * would mean showing the reader the other one first — every card in a
         * freshly loaded list unfolding and folding itself back up.
         */
        const settled = React.useRef(false);

        React.useEffect(() => {
            const node = innerRef.current;
            if (!node) return;

            if (!settled.current) {
                settled.current = true;
                return;
            }

            if (prefersReducedMotion()) {
                setPhase(open ? 'open' : 'collapsed');
                setMaxHeight(open ? undefined : collapsedHeight);
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
            setMaxHeight(collapsedHeight);
        }, [open, collapsedHeight]);

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
