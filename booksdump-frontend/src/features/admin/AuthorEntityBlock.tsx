import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Progress } from '@/shared/ui/progress';
import * as adminApi from '@/api/admin';
import type { AuthorEntityReport, AuthorEntityRun, AuthorEntityStatus } from '@/api/admin';
import { closedErrorCode } from '@/api/admin';

/**
 * The author entity block of the author metadata card (design E, phase E1):
 * one button that starts a build pass, the pass's progress while it runs,
 * and the summary of the newest report. The entities are built in the
 * shadow: nothing a reader sees uses them yet.
 *
 * The start answers at once; the block re-reads after it and every few
 * seconds while a pass is active.
 */

/** How often the block re-reads while a pass is active. */
const POLL_INTERVAL_MS = 5000;

const ACTIVE = new Set(['pending', 'running']);

type T = ReturnType<typeof useTranslation>['t'];

const formatNumber = (value: number) => value.toLocaleString();

const stageLabel = (t: T, run: AuthorEntityRun): string => {
    switch (run.stage) {
        case 'plan':
            return t('authorEntity.stage.plan', 'planning');
        case 'apply':
            return t('authorEntity.stage.apply', 'writing');
        case 'report':
            return t('authorEntity.stage.report', 'report');
        default:
            return t('authorEntity.stage.pending', 'waiting for the worker');
    }
};

const describeError = (t: T, error: unknown): string =>
    closedErrorCode(error) === 'active_run_exists'
        ? t('authorEntity.error.active', 'A pass is already running.')
        : t('authorEntity.error.other', 'The action failed.');

/** The report's summary lines. */
const reportLines = (t: T, r: AuthorEntityReport): string[] => {
    const seconds = Math.round((r.timings.load_ms + r.timings.plan_ms + r.timings.apply_ms) / 1000);
    const perAuthor = (label: string) =>
        r.legacy.entities_per_author.find((s) => s.label === label)?.entities ?? 0;
    return [
        t('authorEntity.report.entities', {
            defaultValue: 'Entities: {{entities}} for {{books}} books ({{credits}} credits)',
            entities: formatNumber(r.entities),
            books: formatNumber(r.books),
            credits: formatNumber(r.credits),
        }),
        t('authorEntity.report.buckets', {
            defaultValue:
                'Short names: {{buckets}}, {{withFull}} of them beside a full name with the patronymic ({{withFullBooks}} books)',
            buckets: formatNumber(r.buckets.buckets),
            withFull: formatNumber(r.buckets.with_full_names),
            withFullBooks: formatNumber(r.buckets.with_full_names_books),
        }),
        t('authorEntity.report.moved', {
            defaultValue:
                'Moved to the full name by evidence: {{moved}} books (title {{title}}, series {{series}}, co-author {{coauthor}}); conflicting {{conflicting}}, samizdat kept {{samizdat}}',
            moved: formatNumber(r.buckets.moved),
            title: formatNumber(r.buckets.moved_by.title ?? 0),
            series: formatNumber(r.buckets.moved_by.series ?? 0),
            coauthor: formatNumber(r.buckets.moved_by.coauthor ?? 0),
            conflicting: formatNumber(r.buckets.conflicting),
            samizdat: formatNumber(r.buckets.samizdat_kept),
        }),
        t('authorEntity.report.other', {
            defaultValue:
                'Patronymic typos merged: {{typos}} · names of initials: {{initials}} · credits without a selected name (no entity yet): {{unselected}}',
            typos: formatNumber(r.typo_merges),
            initials: formatNumber(r.initials_entities),
            unselected: formatNumber(r.unselected_credits),
        }),
        t('authorEntity.report.legacy', {
            defaultValue:
                'Against the old catalog: {{authors}} authors, split into two entities {{two}}, into three or more {{more}}; entities gathering several old authors: {{shared}}',
            authors: formatNumber(r.legacy.authors),
            two: formatNumber(perAuthor('2')),
            more: formatNumber(perAuthor('3+')),
            shared: formatNumber(r.legacy.shared_entities),
        }),
        t('authorEntity.report.pass', {
            defaultValue: 'Pass: {{changes}} rows written, {{skipped}} refused, {{seconds}} s',
            changes: formatNumber(r.changes),
            skipped: formatNumber(r.units_skipped),
            seconds: formatNumber(seconds),
        }),
    ];
};

const AuthorEntityBlock: React.FC = () => {
    const { t } = useTranslation();
    const [status, setStatus] = useState<AuthorEntityStatus | null>(null);
    const [loadFailed, setLoadFailed] = useState(false);
    const [busy, setBusy] = useState(false);
    const [errorText, setErrorText] = useState<string | null>(null);
    const generation = useRef(0);

    const load = useCallback(async () => {
        const current = ++generation.current;
        try {
            const response = await adminApi.getAuthorEntityStatus();
            if (current === generation.current) {
                setStatus(response.status);
                setLoadFailed(false);
            }
        } catch {
            if (current === generation.current) {
                setLoadFailed(true);
            }
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const run = status?.run ?? null;
    const active = run !== null && ACTIVE.has(run.status);
    useEffect(() => {
        if (!active) return undefined;
        const timer = window.setInterval(load, POLL_INTERVAL_MS);
        return () => window.clearInterval(timer);
    }, [active, load]);

    const start = async () => {
        setBusy(true);
        setErrorText(null);
        try {
            await adminApi.startAuthorEntityBuild();
        } catch (error) {
            setErrorText(describeError(t, error));
        } finally {
            await load();
            setBusy(false);
        }
    };

    const report = status?.report ?? null;
    const done = run?.units_done ?? 0;
    const total = run?.units_total ?? 0;

    return (
        <section className="flex flex-col gap-2 border-t pt-3">
            <h4 className="text-sm font-medium">
                {t('authorEntity.title', 'Author entities (shadow)')}
            </h4>
            <p className="text-sm text-muted-foreground">
                {t(
                    'authorEntity.hint',
                    'One record per real author, built from the selected names. Readers do not see it yet.',
                )}
            </p>

            {loadFailed && (
                <Alert variant="destructive">
                    <AlertDescription>
                        {t('authorEntity.loadFailed', 'Could not read the entity build state.')}
                    </AlertDescription>
                </Alert>
            )}
            {errorText !== null && (
                <Alert variant="destructive">
                    <AlertDescription>{errorText}</AlertDescription>
                </Alert>
            )}

            {run !== null && active && (
                <div className="flex flex-col gap-1">
                    <p className="text-sm tabular-nums">
                        {total > 0
                            ? t('authorEntity.progress', {
                                  defaultValue: 'Pass {{id}}: {{stage}} · {{done}} of {{total}}',
                                  id: run.id,
                                  stage: stageLabel(t, run),
                                  done: formatNumber(done),
                                  total: formatNumber(total),
                              })
                            : t('authorEntity.stageOnly', {
                                  defaultValue: 'Pass {{id}}: {{stage}}',
                                  id: run.id,
                                  stage: stageLabel(t, run),
                              })}
                    </p>
                    {total > 0 && <Progress value={(done * 100) / total} />}
                </div>
            )}
            {run !== null && run.status === 'failed' && (
                <p className="text-sm text-destructive">
                    {t('authorEntity.failed', 'The last pass failed.')}
                </p>
            )}

            {status !== null && (
                <div>
                    <Button variant="outline" disabled={busy || active} onClick={start}>
                        {t('authorEntity.build', 'Build author entities')}
                    </Button>
                </div>
            )}

            {report !== null && (
                <div className="flex flex-col gap-1">
                    {reportLines(t, report).map((line) => (
                        <p key={line} className="text-sm tabular-nums">
                            {line}
                        </p>
                    ))}
                    <details className="text-sm">
                        <summary className="cursor-pointer text-muted-foreground">
                            {t('authorEntity.details', 'Sizes and the largest entities')}
                        </summary>
                        <ul className="mt-1 flex flex-col gap-0.5 tabular-nums">
                            {report.sizes.map((s) => (
                                <li key={s.label}>
                                    {t('authorEntity.size', {
                                        defaultValue: '{{label}} books: {{entities}} entities',
                                        label: s.label,
                                        entities: formatNumber(s.entities),
                                    })}
                                </li>
                            ))}
                        </ul>
                        <ul className="mt-1 flex flex-col gap-0.5">
                            {report.top.map((e) => (
                                <li key={e.id} className="tabular-nums">
                                    <span className="font-mono text-xs break-all">{e.id}</span>
                                    {` · ${formatNumber(e.books)}`}
                                </li>
                            ))}
                        </ul>
                    </details>
                </div>
            )}
        </section>
    );
};

export default AuthorEntityBlock;
