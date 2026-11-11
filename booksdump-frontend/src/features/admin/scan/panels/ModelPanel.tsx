import React from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/shared/ui/badge';
import AuthorLLMBlock from '@/features/admin/AuthorLLMBlock';
import type { AuthorLLMStatus } from '@/api/admin';
import Fold from '../Fold';
import { isUsableRun } from '../metadataRun';
import type { Pipeline, StageKey } from '../pipeline';
import ProblemStrip from '../ProblemStrip';
import { useScanSource } from '../ScanSources';
import { formatCount, reasonText, StagePanel } from '../scanUi';

/*
 * «Модель»: the LLM review of names and of merges. When the stage stands, its
 * cause comes first with since when and what waits for it; under it each
 * block's live part (its pauses with «Продолжить», the run on its way, the
 * last run), then the steps and the reports folded. The header names the
 * models and the share of the budget spent; the exact budget line is the
 * names block's, once.
 */

interface ModelPanelProps {
    pipeline: Pipeline;
    onOpen: (stage: StageKey) => void;
}

/** Steps done of the steps the server built. */
const stepCount = (status: AuthorLLMStatus | null) => {
    const steps = status?.steps ?? [];
    return { done: steps.filter((s) => s.done).length, total: steps.length };
};

const ModelPanel: React.FC<ModelPanelProps> = ({ pipeline, onOpen }) => {
    const { t } = useTranslation();
    const names = useScanSource('llmNames').data;
    const merge = useScanSource('llmMerge').data;
    const metadata = useScanSource('metadataRun').data?.run ?? null;
    const node = pipeline.nodes.model;
    const stuck = node.state === 'stuck';
    const pending = metadata !== null && isUsableRun(metadata) ? metadata.credits.pending : 0;
    const namesSteps = stepCount(names);
    const mergeSteps = stepCount(merge);
    const budget =
        names !== null && names.budget_tokens > 0
            ? Math.round((names.spent_tokens * 100) / names.budget_tokens)
            : null;

    return (
        <StagePanel
            stage="model"
            state={node.state}
            badge={
                stuck ? (
                    <Badge variant="outline" className="border-destructive/45 text-destructive">
                        {t('scanPanel.stuck')}
                    </Badge>
                ) : undefined
            }
            aside={
                names !== null && (
                    <span className="text-xs text-muted-foreground tabular-nums">
                        {budget === null
                            ? t('scanPanel.model.models', {
                                  count: names.participants?.length ?? 0,
                              })
                            : t('scanPanel.model.meta', {
                                  count: names.participants?.length ?? 0,
                                  percent: budget,
                              })}
                    </span>
                )
            }
            strips={<ProblemStrip pipeline={pipeline} current="model" onOpen={onOpen} />}
            footer={
                <>
                    <Fold
                        flush
                        label={t('scanPanel.model.steps')}
                        meta={t('scanPanel.model.stepsMeta', {
                            names: namesSteps.done,
                            namesTotal: namesSteps.total,
                            merges: mergeSteps.done,
                            mergesTotal: mergeSteps.total,
                        })}
                    >
                        <AuthorLLMBlock parts={['steps']} />
                        <AuthorLLMBlock merge parts={['steps']} />
                    </Fold>
                    <Fold flush label={t('scanPanel.model.reports')}>
                        <AuthorLLMBlock parts={['reports']} />
                        <AuthorLLMBlock merge parts={['reports']} />
                    </Fold>
                </>
            }
        >
            {stuck && (
                <div className="flex flex-col gap-1.5 rounded-[10px] border border-destructive/45 bg-destructive/10 px-3.5 py-3">
                    <p className="text-sm font-semibold tabular-nums first-letter:uppercase">
                        {reasonText(t, node.reason)}
                    </p>
                    {(pending > 0 || pipeline.nodes.ent.state === 'waiting') && (
                        <ul className="flex flex-col text-sm tabular-nums">
                            {pending > 0 && (
                                <li>
                                    {t('scanPanel.model.waitsNames', {
                                        count: pending,
                                        names: formatCount(pending),
                                    })}
                                </li>
                            )}
                            {pipeline.nodes.ent.state === 'waiting' && (
                                <li>{t('scanPanel.model.waitsEntities')}</li>
                            )}
                        </ul>
                    )}
                </div>
            )}
            <div className="grid min-w-0 gap-x-6 gap-y-3 lg:grid-cols-2">
                <AuthorLLMBlock parts={['live']} />
                <AuthorLLMBlock merge parts={['live']} />
            </div>
        </StagePanel>
    );
};

export default ModelPanel;
