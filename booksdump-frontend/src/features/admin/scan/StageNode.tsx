import React from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';
import type { StageNode as StageNodeData } from './pipeline';
import { nodeFigure, nodeLabel, reasonText, StateDot, stageName, WARNING_TEXT } from './scanUi';

/*
 * One stage of the chain: its dot and name, its big figure, its reason line and
 * a 3 px progress edge while something on it runs. The graph and the phone
 * grid (later the carousel cards) draw the same node.
 *
 * The button carries the state as data-state and the selection as
 * aria-current, and its accessible name is the stage with its reason, so a
 * screen reader hears why a stage is red without reading the colour.
 */

interface StageNodeProps {
    node: StageNodeData;
    selected: boolean;
    onSelect: () => void;
    /** graph: the desktop chain; card: the phone grid and carousel. */
    variant: 'graph' | 'card';
    /** «4 / 6» on a carousel card. */
    position?: string;
    /** human: the review branch, laid out as one row. */
    row?: boolean;
    className?: string;
}

const StageNode: React.FC<StageNodeProps> = ({
    node,
    selected,
    onSelect,
    variant,
    position,
    row = false,
    className,
}) => {
    const { t } = useTranslation();
    const name = stageName(t, node.key);
    const loading = node.reason.key === 'loading';
    // The graph's node is narrow and keeps a moment to its day; a phone card
    // is wide enough for the whole cause, its time included, and wraps it.
    const card = variant === 'card';
    const reason = reasonText(t, node.reason, !card);
    const big = nodeFigure(t, node);
    return (
        <button
            type="button"
            data-stage-node={node.key}
            data-state={node.state}
            data-loading={loading ? '' : undefined}
            aria-current={selected ? 'true' : undefined}
            aria-label={nodeLabel(t, node)}
            onClick={onSelect}
            className={cn(
                'relative flex min-w-0 rounded-[10px] border border-border/60 bg-card text-left transition-colors outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-focus',
                row
                    ? 'min-h-11 flex-row items-center gap-3 px-3.5 py-1.5'
                    : 'flex-col px-3 pt-2 pb-3',
                variant === 'card' && !row && 'min-h-20',
                node.state === 'stuck' && 'border-destructive/50 bg-destructive/10',
                node.state === 'warning' && 'border-amber-500/50 bg-amber-500/10',
                selected && 'border-transparent ring-2 ring-foreground',
                className,
            )}
        >
            <span
                className={cn(
                    'flex items-center gap-1.5 text-xs font-semibold',
                    row ? 'text-[13px] text-foreground' : 'text-muted-foreground',
                )}
            >
                <StateDot state={node.state} />
                {name}
                {position !== undefined && (
                    <span className="ml-auto font-normal tabular-nums">{position}</span>
                )}
            </span>
            {row ? (
                <>
                    <span className="min-w-0 flex-1 truncate text-[12.5px] text-muted-foreground">
                        {reason}
                    </span>
                    <span className="text-lg font-semibold tabular-nums">{big}</span>
                </>
            ) : (
                <>
                    <span className="mt-px text-lg leading-tight font-semibold whitespace-nowrap tabular-nums">
                        {big}
                    </span>
                    <span
                        className={cn(
                            'text-[12.5px] tabular-nums',
                            card ? 'break-words' : 'truncate',
                            node.state === 'stuck' && 'font-medium text-destructive',
                            node.state === 'warning' && cn('font-medium', WARNING_TEXT),
                            (node.state === 'ok' || node.state === 'waiting') &&
                                'text-muted-foreground',
                        )}
                    >
                        {reason}
                    </span>
                </>
            )}
            {node.progress !== undefined && (
                <span
                    aria-hidden="true"
                    className="absolute inset-x-3 bottom-1.5 h-[3px] overflow-hidden rounded-full bg-muted"
                >
                    <span
                        className="block h-full rounded-full bg-primary"
                        style={{ width: `${Math.min(100, Math.max(0, node.progress))}%` }}
                    />
                </span>
            )}
        </button>
    );
};

export default StageNode;
