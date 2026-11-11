import React from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/shared/ui/button';
import { cn } from '@/shared/lib/utils';
import type { Pipeline, StageKey } from './pipeline';
import { reasonText, StateDot, stageName } from './scanUi';

/*
 * The strips at the top of a stage panel. A stage that waits for the stage the
 * flow stopped at says so and links there; the review queue says what a
 * stopped model holds back; every other problem gets «Ещё одна проблема», so a
 * second problem is never only on the chain.
 */

interface ProblemStripProps {
    pipeline: Pipeline;
    current: StageKey;
    onOpen: (stage: StageKey) => void;
}

interface Strip {
    stage: StageKey;
    text: string;
}

const ProblemStrip: React.FC<ProblemStripProps> = ({ pipeline, current, onOpen }) => {
    const { t } = useTranslation();
    const { nodes, problems, stoppedAt } = pipeline;
    const strips: Strip[] = [];
    let named: StageKey | null = null;

    if (stoppedAt !== null && stoppedAt !== current) {
        if (current === 'human' && stoppedAt === 'model') {
            strips.push({
                stage: stoppedAt,
                text: t('scanPanel.strip.humanWaits', { stage: stageName(t, stoppedAt) }),
            });
            named = stoppedAt;
        } else if (nodes[current].state === 'waiting') {
            strips.push({
                stage: stoppedAt,
                text: t('scanPanel.strip.waits', {
                    stage: stageName(t, stoppedAt),
                    reason: reasonText(t, nodes[stoppedAt].reason),
                }),
            });
            named = stoppedAt;
        }
    }
    for (const key of problems) {
        if (key !== current && key !== named) {
            strips.push({
                stage: key,
                text: t('scanPanel.strip.other', {
                    stage: stageName(t, key),
                    reason: reasonText(t, nodes[key].reason),
                }),
            });
        }
    }

    if (strips.length === 0) {
        return null;
    }
    return (
        <div className="flex flex-col">
            {strips.map(({ stage, text }) => {
                const stuck = nodes[stage].state === 'stuck';
                return (
                    <div
                        key={stage}
                        data-strip={stage}
                        className={cn(
                            'flex flex-wrap items-center gap-x-2.5 gap-y-1 border-b px-4 py-2 text-[13px] sm:px-5',
                            stuck ? 'bg-destructive/10' : 'bg-amber-500/10',
                        )}
                    >
                        <StateDot state={nodes[stage].state} />
                        <span className="min-w-48 flex-1 tabular-nums">{text}</span>
                        <Button
                            variant="link"
                            size="sm"
                            className="h-10 px-0 text-foreground underline sm:h-8"
                            onClick={() => onOpen(stage)}
                        >
                            {t('scanPanel.strip.open', { stage: stageName(t, stage) })}
                        </Button>
                    </div>
                );
            })}
        </div>
    );
};

export default ProblemStrip;
