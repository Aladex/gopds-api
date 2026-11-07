/** The label the books language goes by in the bar, as room allows. */
export type BarLabel = 'long' | 'short' | 'none';

/** Clear space to keep between the language and the donate pill. */
export const BAR_LABEL_GAP = 24;

type BarLabelFit = {
    /** What the bar has left once everything but the label is in it. */
    free: number;
    long: number;
    short: number;
};

/**
 * chooseBarLabel says how much label the bar can afford.
 *
 * `free` does not depend on which label is showing, so the answer cannot move
 * the thing it was worked out from. A bar not yet measured gets the whole label:
 * jsdom has no widths, and a first paint is corrected before it shows.
 */
export function chooseBarLabel({ free, long, short }: BarLabelFit): BarLabel {
    if (free <= 0 || free >= long + BAR_LABEL_GAP) {
        return free <= 0 && long > 0 ? 'none' : 'long';
    }
    return free >= short + BAR_LABEL_GAP ? 'short' : 'none';
}
