import React from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/shared/ui/button';
import { cn } from '@/shared/lib/utils';
import { arrowStyle, CHAIN, type Pipeline, type StageKey } from './pipeline';
import { reasonText, StateDot, stageName, WARNING_TEXT } from './scanUi';
import StageNode from './StageNode';

/*
 * The chain card. On a wide screen (graph) the five stages stand in one row
 * joined by arrows, the arrow into the stage the flow stops at dashed red and
 * every arrow after it dashed grey, with the human review branching off under
 * it. Until the phone carousel lands (grid) the same nodes wrap in two columns
 * with the review full-width under them.
 *
 * The header names every problem as a pill, in the derivation's order (stuck
 * first), and holds «Сканировать новые».
 */

interface PipelineGraphProps {
    pipeline: Pipeline;
    /** null while the page waits for its sources to pick the default stage. */
    selected: StageKey | null;
    onSelect: (stage: StageKey) => void;
    layout: 'graph' | 'grid';
    onScan: () => void;
    scanDisabled: boolean;
}

const Arrow: React.FC<{ style: 'flow' | 'stuck' | 'idle' }> = ({ style }) => (
    <svg
        aria-hidden="true"
        data-arrow={style}
        viewBox="0 0 20 10"
        className={cn(
            'pointer-events-none absolute top-1/2 -right-6 h-2.5 w-5 -translate-y-1/2',
            style === 'stuck' ? 'text-destructive' : 'text-input',
        )}
    >
        <line
            x1="0"
            y1="5"
            x2="14"
            y2="5"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeDasharray={style === 'flow' ? undefined : '4 3'}
        />
        <path d="M14 1.5 L20 5 L14 8.5 Z" fill="currentColor" />
    </svg>
);

const PipelineGraph: React.FC<PipelineGraphProps> = ({
    pipeline,
    selected,
    onSelect,
    layout,
    onScan,
    scanDisabled,
}) => {
    const { t } = useTranslation();
    const { nodes, problems } = pipeline;
    const graph = layout === 'graph';

    return (
        <section
            aria-label={t('scanPanel.title')}
            className={cn(
                'flex flex-col rounded-xl border bg-card text-card-foreground shadow-sm',
                graph ? 'gap-3 px-5 pt-3.5 pb-4' : 'gap-3 p-4',
            )}
        >
            <div className="flex min-h-8 flex-wrap items-center gap-2.5">
                <h3 className="text-base font-medium">{t('scanPanel.title')}</h3>
                {problems.length === 0 ? (
                    <span className="text-sm text-muted-foreground">{t('scanPanel.allGood')}</span>
                ) : (
                    // Bounded by the card: on a phone a long pause reason
                    // wraps inside its pill rather than running past the edge.
                    <span className="flex max-w-full min-w-0 flex-wrap items-center gap-1.5 text-[13px]">
                        <span className="text-destructive">
                            {t('scanPanel.problems', { count: problems.length })}
                        </span>
                        {problems.map((key) => {
                            const state = nodes[key].state;
                            return (
                                <button
                                    key={key}
                                    type="button"
                                    data-problem={key}
                                    onClick={() => onSelect(key)}
                                    className={cn(
                                        'inline-flex min-h-10 max-w-full min-w-0 items-center gap-1.5 rounded-2xl px-2.5 py-1 text-left text-[13px] font-medium outline-none hover:underline hover:underline-offset-3 focus-visible:ring-[3px] focus-visible:ring-focus sm:min-h-7',
                                        state === 'stuck'
                                            ? 'bg-destructive/10 text-destructive'
                                            : cn('bg-amber-500/10', WARNING_TEXT),
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
                    </span>
                )}
                <span className="flex-1" />
                <Button variant="outline" size="sm" disabled={scanDisabled} onClick={onScan}>
                    {t('scanPanel.scanNew')}
                </Button>
            </div>

            <div
                className={cn(
                    'grid',
                    graph ? 'grid-cols-5 gap-x-7 gap-y-3.5' : 'grid-cols-2 gap-2.5',
                )}
            >
                {CHAIN.map((key, index) => (
                    <div key={key} className="relative min-w-0">
                        <StageNode
                            node={nodes[key]}
                            selected={selected === key}
                            onSelect={() => onSelect(key)}
                            variant={graph ? 'graph' : 'card'}
                            className="h-full w-full"
                        />
                        {graph && index < CHAIN.length - 1 && (
                            <Arrow style={arrowStyle(index, pipeline.stoppedAt)} />
                        )}
                    </div>
                ))}
                {graph && (
                    <p className="col-span-2 self-center text-right text-[12.5px] text-muted-foreground">
                        {t('scanPanel.branchNote')}
                    </p>
                )}
                <div className="relative col-span-2 min-w-0">
                    {graph && (
                        <span
                            aria-hidden="true"
                            className="absolute -top-3.5 left-1/2 h-3.5 w-[1.5px] bg-input"
                        />
                    )}
                    <StageNode
                        node={nodes.human}
                        selected={selected === 'human'}
                        onSelect={() => onSelect('human')}
                        variant={graph ? 'graph' : 'card'}
                        row
                        className="w-full"
                    />
                </div>
            </div>
        </section>
    );
};

export default PipelineGraph;
