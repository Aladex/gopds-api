import React from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';
import { passageFor, type Moment } from '@/features/auth/passages';

/** How fast the verse drifts, in pixels per second. */
const SPEED = 25;

/** Line height in pixels, which is what turns a line count into a distance. */
const LINE_HEIGHT = 44;

/**
 * A page of Russian verse, drifting slowly behind the screens a reader meets
 * before they are signed in.
 *
 * Only these three screens. The book list was considered and refused: it is
 * already dense with covers, chips, dates and annotations, and a text pattern
 * underneath competes with content rather than supporting it — it would also
 * give back part of the contrast the light theme was measured into.
 *
 * The language follows the interface, and the two are not translations of each
 * other: a reader who has chosen English gets an English poem about the same
 * thing, not Pushkin in translation.
 *
 * On a screen wide enough it runs down the left rather than behind the card:
 * a form read through a poem is harder to read than a form beside one, and the
 * screen gains a composition instead of a pile.
 *
 * The pace is the whole design. At twenty-five pixels a second a full pass
 * takes upwards of a minute, which is slow enough that peripheral vision stops
 * reading it as movement and starts reading it as a surface. Faster would be
 * credits; this is a page that happens to be turning.
 *
 * It is decoration and nothing else: `aria-hidden`, unselectable, and unable to
 * take a pointer. A screen reader announcing sixty lines of Pushkin before the
 * password field would be a cruel joke.
 */
type ClassicBackdropProps = {
    /** Which of the three moments this screen is. */
    moment: Moment;
    className?: string;
};

const ClassicBackdrop: React.FC<ClassicBackdropProps> = ({ moment, className }) => {
    const { i18n } = useTranslation();
    const passage = passageFor(moment, i18n.language);
    // The distance one full pass covers, and therefore how long it takes at a
    // fixed speed. Two copies of the verse ride one after the other, so the
    // moment the first has gone the second is already in place — a single copy
    // would leave a gap the height of the screen before it came round again.
    const height = passage.lines.length * LINE_HEIGHT;
    const duration = height / SPEED;

    const verse = (
        <div aria-hidden>
            {passage.lines.map((line, index) => (
                // Lines repeat within a poem, so the index is what tells them
                // apart. Nothing here is ever reordered.
                <p key={index} style={{ height: LINE_HEIGHT }} className="m-0 whitespace-pre">
                    {line}
                </p>
            ))}
        </div>
    );

    return (
        <div
            aria-hidden
            className={cn(
                'pointer-events-none absolute inset-0 overflow-hidden select-none',
                className,
            )}
        >
            <div
                className={cn(
                    'absolute top-0',
                    // Centred on a phone, where there is only one column and the
                    // card sits in it. Given a screen, the verse moves off to
                    // the left and stops being something the form is read
                    // through — the card keeps the middle, the picture keeps the
                    // bottom right, and each has its own ground.
                    'inset-x-0 text-center',
                    'sm:inset-x-auto sm:left-[6vw] sm:w-[34rem] sm:max-w-[42vw] sm:text-left',
                    // The line box stays 44px at every width because the loop is
                    // measured in those units; only the letters get smaller, so
                    // a phone stops clipping the longest line at both edges
                    // without the seam drifting out of place.
                    'font-handwritten leading-[44px]',
                    'text-[19px] sm:text-[34px]',
                    // Faint enough to be a surface, not so faint it cannot be
                    // recognised — and set per theme, because one opacity does
                    // not read the same on both. The light theme needs more ink
                    // than the arithmetic suggests: 13% measured a tidy 1.31:1
                    // and could not be read at all, so it carries 22% where the
                    // dark theme carries 18%. Both were settled by looking at a
                    // screen, with the ratios written down rather than obeyed —
                    // the same alpha on a pale page and a black one does not
                    // land in the same place.
                    'text-foreground/22 dark:text-foreground/18',
                    'animate-verse-drift motion-reduce:animate-none',
                )}
                style={{
                    animationDuration: `${duration}s`,
                    // The pair is twice the verse, and the animation moves it by
                    // exactly one copy — so the seam never lands anywhere a
                    // reader can catch it.
                    ['--verse-height' as string]: `${height}px`,
                }}
            >
                {verse}
                {verse}
            </div>
        </div>
    );
};

export default ClassicBackdrop;
