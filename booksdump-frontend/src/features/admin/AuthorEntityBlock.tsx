import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Progress } from '@/shared/ui/progress';
import * as adminApi from '@/api/admin';
import type {
    AuthorEntityReport,
    AuthorEntityRun,
    AuthorEntityStatus,
    AuthorEntityWikidataReport,
    WikidataStatus,
} from '@/api/admin';
import { closedErrorCode } from '@/api/admin';

/**
 * The author entity block of the author metadata card (design E, phase E1):
 * one button that starts a build pass, the pass's progress while it runs,
 * and the summary of the newest report. The entities are built in the
 * shadow: nothing a reader sees uses them yet.
 *
 * The start answers at once; the block re-reads after it and every few
 * seconds while a pass is active.
 *
 * Its Wikidata section (phase E4w) fetches the reference the build reads as
 * evidence — from QLever, in the background, a failed fetch going on from
 * the parts it has — and the report says what the evidence merged.
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

const describeFetchError = (t: T, error: unknown): string =>
    closedErrorCode(error) === 'active_run_exists'
        ? t('authorEntity.wikidata.error.active', 'A fetch is already running.')
        : t('authorEntity.error.other', 'The action failed.');

/** The Wikidata lines of a report. */
const wikidataLines = (t: T, w: AuthorEntityWikidataReport): string[] => {
    const kind = (k: string) => w.merges[k] ?? 0;
    const excluded = (k: string) => w.excluded?.[k] ?? 0;
    const total = Object.values(w.merges).reduce((sum, v) => sum + v, 0);
    return [
        t('authorEntity.wikidata.report.merges', {
            defaultValue:
                "Wikidata {{extract}}: {{total}} merged — the same name {{same}}, another script {{cross}}; entities named by one writer's main label {{named}}, writers with two or more {{groups}}, entities given their writer {{references}} (short names beside a full one left without {{withheld}})",
            extract: w.extract,
            total: formatNumber(total),
            same: formatNumber(kind('same_name')),
            cross: formatNumber(kind('cross_script')),
            named: formatNumber(w.named ?? 0),
            groups: formatNumber(w.groups),
            references: formatNumber(w.references ?? 0),
            withheld: formatNumber(w.references_withheld ?? 0),
        }),
        t('authorEntity.wikidata.report.apart', {
            defaultValue:
                'Kept apart: the writer has pseudonyms {{pseudonym}} · initials {{initials}} · a short name beside its full name {{short}} · a namesake tag {{namesake}} · two names in one script {{scripts}} · on one book {{shared}}',
            pseudonym: formatNumber(excluded('pseudonym')),
            initials: formatNumber(excluded('initials')),
            short: formatNumber(excluded('short_name')),
            namesake: formatNumber(excluded('namesake')),
            scripts: formatNumber(w.script_conflicts ?? 0),
            shared: formatNumber(w.shared_book),
        }),
    ];
};

/** The Wikidata section: the published extract, the newest fetch, the start. */
const WikidataSection: React.FC<{
    wikidata: WikidataStatus | null;
    onChanged: () => Promise<void>;
}> = ({ wikidata, onChanged }) => {
    const { t } = useTranslation();
    const [busy, setBusy] = useState(false);
    const [errorText, setErrorText] = useState<string | null>(null);
    const latest = wikidata?.latest ?? null;
    const published = wikidata?.published ?? null;
    const fetching = latest?.status === 'fetching';
    const failed = latest?.status === 'failed';

    const start = async () => {
        setBusy(true);
        setErrorText(null);
        try {
            await adminApi.startWikidataFetch();
        } catch (error) {
            setErrorText(describeFetchError(t, error));
        } finally {
            await onChanged();
            setBusy(false);
        }
    };

    return (
        <div className="flex flex-col gap-2">
            <h5 className="text-sm font-medium">{t('authorEntity.wikidata.title', 'Wikidata')}</h5>
            <p className="text-sm text-muted-foreground">
                {t(
                    'authorEntity.wikidata.hint',
                    'A reference of writers the build reads as evidence: spellings and scripts of one author. Fetched from QLever by the server; fetching the same data again changes nothing.',
                )}
            </p>
            {errorText !== null && (
                <Alert variant="destructive">
                    <AlertDescription>{errorText}</AlertDescription>
                </Alert>
            )}
            {published !== null ? (
                <p className="text-sm tabular-nums">
                    {t('authorEntity.wikidata.published', {
                        defaultValue:
                            'Extract {{extract}}: {{people}} writers, {{names}} names, {{works}} notable works',
                        extract: published.content_id ?? '',
                        people: formatNumber(published.people),
                        names: formatNumber(published.names),
                        works: formatNumber(published.works),
                    })}
                </p>
            ) : (
                <p className="text-sm text-muted-foreground">
                    {t(
                        'authorEntity.wikidata.none',
                        'No extract yet: the build goes without Wikidata.',
                    )}
                </p>
            )}
            {latest !== null && fetching && (
                <div className="flex flex-col gap-1">
                    <p className="text-sm tabular-nums">
                        {t('authorEntity.wikidata.progress', {
                            defaultValue: 'Fetch {{id}}: {{done}} of {{total}} parts',
                            id: latest.id,
                            done: formatNumber(latest.chunks_done),
                            total: formatNumber(latest.chunks_total),
                        })}
                    </p>
                    <Progress
                        value={(latest.chunks_done * 100) / Math.max(latest.chunks_total, 1)}
                    />
                </div>
            )}
            {latest !== null && failed && (
                <p className="text-sm text-destructive">
                    {t('authorEntity.wikidata.failed', {
                        defaultValue:
                            'The fetch stopped: {{error}}. The {{done}} parts fetched are kept.',
                        error: latest.last_error ?? '',
                        done: formatNumber(latest.chunks_done),
                    })}
                </p>
            )}
            {latest !== null && latest.status === 'duplicate' && (
                <p className="text-sm text-muted-foreground">
                    {t(
                        'authorEntity.wikidata.duplicate',
                        'The last fetch is the same as the extract: nothing changed.',
                    )}
                </p>
            )}
            {wikidata !== null && (
                <div>
                    <Button variant="outline" disabled={busy || fetching} onClick={start}>
                        {failed
                            ? t('authorEntity.wikidata.resume', 'Continue the fetch')
                            : t('authorEntity.wikidata.fetch', 'Fetch Wikidata')}
                    </Button>
                </div>
            )}
        </div>
    );
};

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
    const active =
        (run !== null && ACTIVE.has(run.status)) || status?.wikidata?.latest?.status === 'fetching';
    const building = run !== null && ACTIVE.has(run.status);
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

            {run !== null && building && (
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
                    <Button variant="outline" disabled={busy || building} onClick={start}>
                        {t('authorEntity.build', 'Build author entities')}
                    </Button>
                </div>
            )}

            {status !== null && (
                <WikidataSection wikidata={status.wikidata ?? null} onChanged={load} />
            )}

            {report !== null && (
                <div className="flex flex-col gap-1">
                    {reportLines(t, report).map((line) => (
                        <p key={line} className="text-sm tabular-nums">
                            {line}
                        </p>
                    ))}
                    {report.wikidata !== undefined &&
                        report.wikidata.extract !== '' &&
                        wikidataLines(t, report.wikidata).map((line) => (
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
