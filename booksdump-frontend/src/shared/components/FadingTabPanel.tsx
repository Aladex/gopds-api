import React from 'react';

import { TabsContent } from '@/shared/ui/tabs';
import { cn } from '@/shared/lib/utils';

/** How long each half of the handover takes. */
export const PANEL_FADE = 140;

/**
 * The two halves of the handover, as inline styles.
 *
 * Each state needs its own delay, which is a per-state declaration Tailwind has
 * no utility for. The incoming panel's delay and the outgoing panel's duration
 * are the same number on purpose: that is what makes them take turns.
 *
 * The rise is animated as `translate`, not `transform`. That is the property
 * Tailwind's translate-y-* utilities set, and naming the other one animates
 * nothing at all — the panel simply appears a few pixels low and snaps up.
 */
export const panelStyle = (active: boolean): React.CSSProperties =>
    active
        ? {
              transition: `opacity ${PANEL_FADE}ms ease-out ${PANEL_FADE}ms, translate ${PANEL_FADE}ms ease-out ${PANEL_FADE}ms`,
          }
        : {
              transition: `opacity ${PANEL_FADE}ms ease-in, translate ${PANEL_FADE}ms ease-in, visibility 0s linear ${PANEL_FADE}ms`,
          };

const PANEL_CLASSES = cn(
    // Every panel shares one grid cell, so the sheet is as tall as the tallest
    // of them and stops resizing as tabs are switched. The Tabs above must
    // therefore be a grid with its list in the first row.
    'col-start-1 row-start-2',
    // What hides an inactive panel is the data-state rule below. Some versions
    // of Radix also put a `hidden` attribute on it, which would take it out of
    // the layout and give back the resizing the shared cell exists to stop, so
    // this stands ready to overrule that. The current one does not set it.
    '[&[hidden]]:block',
    'data-[state=inactive]:invisible data-[state=inactive]:translate-y-1',
    'data-[state=inactive]:opacity-0',
    'motion-reduce:transition-none',
);

type FadingTabPanelProps = React.ComponentProps<typeof TabsContent> & {
    /** Whether this is the panel being shown. */
    active: boolean;
};

/**
 * A tab panel that hands over rather than swapping.
 *
 * Panels sharing one cell cannot cross-fade without one form being drawn over
 * another, so these take turns: the outgoing one fades out, and only once it is
 * gone does the incoming one begin. An instant swap shows every seam between
 * two layouts; this shows none, because there is never more than one on screen.
 *
 * `visibility` is what makes the handover exact. Opacity alone would leave the
 * faded-out panel able to take a click, so it has to be hidden too — but hiding
 * it the moment the tab changes would cut its fade off at the first frame.
 * Transitioning it with a zero duration and the fade's delay flips it the
 * instant the fade ends instead.
 *
 * It keys off Radix's own data-state rather than a second copy of which tab is
 * open, so a panel cannot disagree with the primitive about it for a frame.
 */
const FadingTabPanel: React.FC<FadingTabPanelProps> = ({ active, className, style, ...props }) => (
    <TabsContent
        forceMount
        inert={!active}
        className={cn(PANEL_CLASSES, className)}
        style={{ ...panelStyle(active), ...style }}
        {...props}
    />
);

export default FadingTabPanel;
