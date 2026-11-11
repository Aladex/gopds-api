import React, { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { X } from 'lucide-react';

import { useSnapCarousel } from '@/shared/hooks/useSnapCarousel';
import { cn } from '@/shared/lib/utils';
import { arrowStyle, CHAIN, type Pipeline, type StageKey, type StageNode } from './pipeline';
import { formatCount, nodeLabel, reasonText, StateDot, stageName, WARNING_TEXT } from './scanUi';
import StageNodeCard from './StageNode';

/*
 * The chain on a phone. On top a row of dots, one per stage, joined the way
 * the graph's arrows are (dashed red into the stage the flow stopped at,
 * dashed grey after it), with the review queue's count as a pill. The row is
 * the carousel's indicator: the selected dot is ringed and named, and a tap
 * on any dot goes there. Under it one line per problem, so a second problem
 * reads without a swipe. Under those, the stage cards on a snapping track.
 *
 * The track takes only sideways pans: a vertical one, and a vertical wheel,
 * scroll the page, and the first card starts 16 px in so a swipe from the
 * edge stays the browser's back gesture. The card the track comes to rest on
 * selects its stage without a history entry; a selection made anywhere else
 * (a dot, a problem line, a panel's «Открыть») moves the track to its card.
 */

const STAGES: readonly StageKey[] = [...CHAIN, 'human'];

/** A dot's layout width; its 40 px target reaches 4 px into the lines on each side. */
const DOT_PX = 32;

interface PipelineCarouselProps {
    pipeline: Pipeline;
    /** null while the page waits for its sources to pick the default stage. */
    selected: StageKey | null;
    /** replace: a swipe chose it, so it takes no history entry of its own. */
    onSelect: (stage: StageKey, options?: { replace: boolean }) => void;
}

/** The mark inside a dot: filled when fine, «!» on a warning, ✕ where it stopped. */
const DotMark: React.FC<{ node: StageNode }> = ({ node }) => {
    if (node.state === 'stuck') {
        return (
            <span className="inline-flex size-[18px] items-center justify-center rounded-full bg-destructive text-destructive-foreground dark:bg-destructive-solid">
                <X className="size-3" strokeWidth={3} />
            </span>
        );
    }
    if (node.state === 'warning') {
        return (
            <span className="inline-flex size-[18px] items-center justify-center rounded-full bg-amber-600 text-[11px] leading-none font-bold text-white dark:bg-amber-500 dark:text-black">
                !
            </span>
        );
    }
    if (node.state === 'waiting') {
        return (
            <span className="inline-block size-3.5 rounded-full border-2 border-amber-500 bg-card dark:border-amber-400" />
        );
    }
    return (
        <span
            className={cn(
                'inline-block size-3.5 rounded-full',
                node.reason.key === 'loading' ? 'bg-muted-foreground/40' : 'bg-primary',
            )}
        />
    );
};

const PipelineCarousel: React.FC<PipelineCarouselProps> = ({ pipeline, selected, onSelect }) => {
    const { t } = useTranslation();
    const { nodes, problems, stoppedAt } = pipeline;
    const index = selected === null ? -1 : STAGES.indexOf(selected);

    const { trackRef, active, dragging, go, trackProps } = useSnapCarousel({
        count: STAGES.length,
        align: 'start',
        onActiveChange: (rest) => {
            const stage = STAGES[rest];
            if (stage !== undefined && stage !== selected) onSelect(stage, { replace: true });
        },
    });

    // The track follows the selection. The first stage known opens in place;
    // after that a new one glides to its card, unless the track already
    // rests there because a swipe chose it.
    const shown = useRef<number | null>(null);
    useEffect(() => {
        if (index < 0 || shown.current === index) return;
        const first = shown.current === null;
        shown.current = index;
        if (!first && index === active) return;
        go(index, first ? 'auto' : 'smooth');
    }, [index, active, go]);

    const chainIndex = selected === null ? -1 : CHAIN.indexOf(selected);
    const fraction = chainIndex < 0 ? 0 : chainIndex / (CHAIN.length - 1);

    return (
        <section
            aria-label={t('scanPanel.title')}
            className="flex min-w-0 flex-col overflow-hidden rounded-xl border bg-card pt-1.5 pb-3.5 text-card-foreground shadow-sm"
        >
            <div
                role="group"
                aria-label={t('scanPanel.carousel.dots')}
                className="flex items-start gap-1.5 px-3 pb-5"
            >
                <div className="relative flex h-10 min-w-0 flex-1 items-center">
                    {CHAIN.map((key, at) => {
                        const node = nodes[key];
                        const style = at > 0 ? arrowStyle(at - 1, stoppedAt) : null;
                        return (
                            <React.Fragment key={key}>
                                {style !== null && (
                                    <span
                                        aria-hidden="true"
                                        data-line={style}
                                        className={cn(
                                            'h-0 min-w-0 flex-1 border-t-[1.5px] border-input',
                                            style === 'stuck' && 'border-dashed border-destructive',
                                            style === 'idle' && 'border-dashed',
                                        )}
                                    />
                                )}
                                <button
                                    type="button"
                                    data-dot={key}
                                    data-state={node.state}
                                    aria-label={nodeLabel(t, node)}
                                    aria-current={selected === key ? 'true' : undefined}
                                    onClick={() => onSelect(key)}
                                    className={cn(
                                        // 32 px in the row, 40 px under a finger.
                                        '-mx-1 inline-flex size-10 shrink-0 items-center justify-center rounded-full outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
                                        selected === key &&
                                            'shadow-[inset_0_0_0_2px_var(--foreground)]',
                                    )}
                                >
                                    <DotMark node={node} />
                                </button>
                            </React.Fragment>
                        );
                    })}
                    {chainIndex >= 0 && selected !== null && (
                        // Under its dot: the first one's name starts at its
                        // left edge, the last one's ends at its right, and the
                        // ones between are centred, so none runs off the card.
                        <span
                            data-dot-name
                            className="pointer-events-none absolute top-full mt-0.5 text-[12.5px] leading-4 font-semibold whitespace-nowrap"
                            style={{
                                left: `calc((100% - ${DOT_PX}px) * ${fraction})`,
                                transform: `translateX(calc(${-100 * fraction}% + ${DOT_PX * fraction}px))`,
                            }}
                        >
                            {stageName(t, selected)}
                        </span>
                    )}
                </div>
                <div className="relative shrink-0">
                    <button
                        type="button"
                        data-dot="human"
                        data-state={nodes.human.state}
                        aria-label={nodeLabel(t, nodes.human)}
                        aria-current={selected === 'human' ? 'true' : undefined}
                        onClick={() => onSelect('human')}
                        className={cn(
                            'inline-flex h-10 items-center gap-1.5 rounded-full border border-border px-3 text-sm font-semibold tabular-nums outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
                            selected === 'human' && 'border-transparent ring-2 ring-foreground',
                        )}
                    >
                        <StateDot state={nodes.human.state} />
                        {nodes.human.figure === null ? '—' : formatCount(nodes.human.figure)}
                    </button>
                    {selected === 'human' && (
                        <span
                            data-dot-name
                            className="pointer-events-none absolute top-full right-0 mt-0.5 text-[12.5px] leading-4 font-semibold whitespace-nowrap"
                        >
                            {stageName(t, 'human')}
                        </span>
                    )}
                </div>
            </div>

            {problems.length > 0 && (
                <div className="flex flex-col px-2 pb-1.5">
                    {problems.map((key) => {
                        const state = nodes[key].state;
                        return (
                            <button
                                key={key}
                                type="button"
                                data-problem={key}
                                onClick={() => onSelect(key)}
                                className={cn(
                                    'flex min-h-11 min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left text-[13px] font-medium outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-focus',
                                    state === 'stuck' ? 'text-destructive' : WARNING_TEXT,
                                )}
                            >
                                <StateDot state={state} />
                                <span className="min-w-0 break-words">
                                    {t('scanPanel.pill', {
                                        stage: stageName(t, key),
                                        reason: reasonText(t, nodes[key].reason),
                                    })}
                                </span>
                            </button>
                        );
                    })}
                </div>
            )}

            <div
                ref={trackRef}
                role="group"
                tabIndex={0}
                aria-label={t('scanPanel.carousel.track')}
                {...trackProps}
                className={cn(
                    'flex gap-2.5 overflow-x-auto overflow-y-hidden overscroll-x-contain px-4 py-1 scroll-px-4',
                    'touch-pan-x touch-pan-y [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
                    'rounded-md focus-visible:outline-3 focus-visible:-outline-offset-3 focus-visible:outline-focus',
                    dragging
                        ? 'cursor-grabbing snap-none select-none'
                        : 'cursor-grab snap-x snap-mandatory',
                )}
            >
                {STAGES.map((key, at) => (
                    <div
                        key={key}
                        data-slide={at}
                        className="flex min-w-0 flex-[0_0_80%] snap-start"
                    >
                        <StageNodeCard
                            node={nodes[key]}
                            selected={selected === key}
                            onSelect={() => onSelect(key)}
                            variant="card"
                            position={`${at + 1} / ${STAGES.length}`}
                            className="w-full"
                        />
                    </div>
                ))}
            </div>
        </section>
    );
};

export default PipelineCarousel;
