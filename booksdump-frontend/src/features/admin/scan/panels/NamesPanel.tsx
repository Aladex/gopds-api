import React from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/shared/ui/badge';
import { cn } from '@/shared/lib/utils';
import Fold from '../Fold';
import { isUsableRun } from '../metadataRun';
import type { NodeState, Pipeline, StageKey } from '../pipeline';
import ProblemStrip from '../ProblemStrip';
import { useScanSource } from '../ScanSources';
import { formatCount, StagePanel, StateDot } from '../scanUi';

/*
 * «Имена»: the author names of the books, which have no block of their own.
 * Built from the metadata run alone: how the credits split (selected, on
 * review, invalid, waiting for the model) as a stacked bar and four figures,
 * and the names the model left undecided, by reason.
 */

interface NamesPanelProps {
    pipeline: Pipeline;
    onOpen: (stage: StageKey) => void;
}

interface Share {
    key: 'selected' | 'review' | 'invalid' | 'pending';
    value: number;
    state: NodeState;
    bar: string;
}

const NamesPanel: React.FC<NamesPanelProps> = ({ pipeline, onOpen }) => {
    const { t } = useTranslation();
    const source = useScanSource('metadataRun');
    const raw = source.data?.run ?? null;
    const run = raw !== null && isUsableRun(raw) ? raw : null;
    const node = pipeline.nodes.names;
    const credits = run?.credits ?? null;

    const shares: Share[] =
        credits === null
            ? []
            : [
                  { key: 'selected', value: credits.selected, state: 'ok', bar: 'bg-primary' },
                  {
                      key: 'review',
                      value: credits.review,
                      state: 'warning',
                      bar: 'bg-amber-500 dark:bg-amber-400',
                  },
                  { key: 'invalid', value: credits.invalid, state: 'stuck', bar: 'bg-destructive' },
                  {
                      key: 'pending',
                      value: credits.pending,
                      state: 'waiting',
                      bar: 'bg-muted-foreground/50',
                  },
              ];
    const total = shares.reduce((sum, s) => sum + s.value, 0);
    const unresolved = Object.entries(credits?.unresolved ?? {})
        .filter(([, count]) => count > 0)
        .sort(([a, x], [b, y]) => y - x || a.localeCompare(b));
    const unresolvedTotal = unresolved.reduce((sum, [, count]) => sum + count, 0);

    return (
        <StagePanel
            stage="names"
            state={node.state}
            badge={
                credits === null ? undefined : (
                    <Badge variant="secondary" className="tabular-nums">
                        {t('scanPanel.names.badge', { selected: formatCount(credits.selected) })}
                    </Badge>
                )
            }
            strips={<ProblemStrip pipeline={pipeline} current="names" onOpen={onOpen} />}
        >
            {credits === null ? (
                <p className="text-sm text-muted-foreground">{t('loading')}</p>
            ) : (
                <>
                    <div
                        className="flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-muted"
                        title={t('scanPanel.names.barTitle')}
                    >
                        {total > 0 &&
                            shares
                                .filter((s) => s.value > 0)
                                .map((s) => (
                                    <span
                                        key={s.key}
                                        className={cn('block h-full', s.bar)}
                                        style={{ width: `${(s.value * 100) / total}%` }}
                                    />
                                ))}
                    </div>
                    <dl className="flex flex-wrap gap-x-5 gap-y-1 text-sm tabular-nums">
                        {shares.map((s) => (
                            <div key={s.key} className="flex items-center gap-1.5">
                                <StateDot state={s.state} />
                                <dt>{t(`scanPanel.names.${s.key}`)}</dt>
                                <dd className="font-medium">{formatCount(s.value)}</dd>
                            </div>
                        ))}
                    </dl>
                    <Fold
                        label={t('scanPanel.names.unresolved')}
                        meta={t('scanPanel.names.unresolvedMeta', {
                            names: formatCount(unresolvedTotal),
                            count: unresolved.length,
                        })}
                    >
                        {unresolved.length === 0 ? (
                            <p className="text-sm text-muted-foreground">
                                {t('scanPanel.names.unresolvedNone')}
                            </p>
                        ) : (
                            <ul className="flex flex-col">
                                {unresolved.map(([reason, count]) => (
                                    <li
                                        key={reason}
                                        className="flex items-center gap-2.5 border-b border-border/60 py-1.5 text-sm last:border-b-0"
                                    >
                                        <span className="min-w-0 flex-1">
                                            {t(`scanPanel.unresolved.${reason}`, reason)}
                                        </span>
                                        <span className="text-muted-foreground tabular-nums">
                                            {formatCount(count)}
                                        </span>
                                    </li>
                                ))}
                            </ul>
                        )}
                    </Fold>
                </>
            )}
        </StagePanel>
    );
};

export default NamesPanel;
