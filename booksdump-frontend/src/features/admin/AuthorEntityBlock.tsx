import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Progress } from '@/shared/ui/progress';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/ui/select';
import { Switch } from '@/shared/ui/switch';
import * as adminApi from '@/api/admin';
import type {
    AuthorAutopilotStatus,
    AuthorMergeDecision,
    AuthorMergeVeto,
    AuthorMergeVetoEntity,
    AuthorMerges,
    AuthorEntityReport,
    AuthorEntityRun,
    AuthorEntityStatus,
    AuthorEntityWikidataReport,
    WikidataExtract,
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
 * the parts it has — and the report says what the evidence merged. The
 * extract stays a button: the section says how old it is.
 *
 * Its autopilot section switches the autopilot on and off and says what the
 * last check found — the scan not quiet yet, a run it waits for, the run it
 * started, nothing to do — how the resolve check came out, whether a pass is
 * due, the last run it started and a failed check's backoff. While the
 * autopilot is on the block keeps re-reading, so what it starts on its own
 * shows up.
 *
 * Its merges section (E4b-3b) lists the model's merge decisions with what the
 * newest pass did with them, and the vetoes revokes left: a revoke splits the
 * pair and keeps it apart; a veto is retired, or re-anchored on the two
 * entities that are its sides now when an extractor rewrite lost its books.
 */

/** How often the block re-reads while a pass is active. */
const POLL_INTERVAL_MS = 5000;

/**
 * How often it re-reads while the autopilot is on and nothing runs: a scan
 * settles and a cycle starts on its own, so the block looks for it.
 */
const IDLE_POLL_INTERVAL_MS = 15000;

const ACTIVE = new Set(['pending', 'running']);

type T = ReturnType<typeof useTranslation>['t'];

const formatNumber = (value: number) => value.toLocaleString();

const formatTime = (value: string) => new Date(value).toLocaleString();

const DAY_MS = 24 * 60 * 60 * 1000;

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

/** How old the published extract is: when it was fetched, in days. */
const extractAge = (t: T, published: WikidataExtract): string | null => {
    const at = published.finished_at ?? published.started_at;
    if (!at) return null;
    const days = Math.floor((Date.now() - new Date(at).getTime()) / DAY_MS);
    return days < 1
        ? t('authorEntity.wikidata.ageToday', {
              defaultValue: 'Fetched {{date}}, today',
              date: formatTime(at),
          })
        : t('authorEntity.wikidata.age', {
              defaultValue: 'Fetched {{date}}, {{days}} d ago',
              date: formatTime(at),
              days: formatNumber(days),
          });
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
                <>
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
                    {extractAge(t, published) !== null && (
                        <p className="text-sm text-muted-foreground tabular-nums">
                            {extractAge(t, published)}
                        </p>
                    )}
                </>
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

/** How the last resolve check came out, by its closed note. */
const RESOLVE_NOTES: Record<string, [string, string]> = {
    started: ['authorEntity.autopilot.note.started', 'a resolve run was started'],
    llm_not_configured: ['authorEntity.autopilot.note.llmOff', 'the LLM layer is off'],
    no_resolve_pilot: [
        'authorEntity.autopilot.note.noPilot',
        'the resolve pilot has not run under this configuration',
    ],
    llm_paused: ['authorEntity.autopilot.note.paused', 'a model or the endpoint is paused'],
    budget_exhausted: ['authorEntity.autopilot.note.budget', 'the token budget is spent'],
    no_registered_strata: ['authorEntity.autopilot.note.noStrata', 'no registered stratum'],
    nothing_to_resolve: ['authorEntity.autopilot.note.nothing', 'no name left to ask'],
    evidence_changed: ['authorEntity.autopilot.note.evidence', 'the checks changed meanwhile'],
    llm_run_active: ['authorEntity.autopilot.note.runActive', 'another LLM run started meanwhile'],
};

/** What the last check found, as one line. */
const stateLine = (t: T, a: AuthorAutopilotStatus): string => {
    const id = a.state_run_id ?? '';
    switch (a.state) {
        case 'not_quiet':
            return t('authorEntity.autopilot.state.notQuiet', {
                defaultValue: 'Waiting for the scan to be quiet, until {{date}}.',
                date: a.quiet_until ? formatTime(a.quiet_until) : '',
            });
        case 'normalizing':
            return t('authorEntity.autopilot.state.normalizing', {
                defaultValue: 'Local normalization is at work ({{jobs}} names waiting).',
                jobs: formatNumber(a.waiting_jobs),
            });
        case 'llm_run_active':
            return t('authorEntity.autopilot.state.llmRun', {
                defaultValue: 'Waiting for LLM run {{id}} to end.',
                id,
            });
        case 'entity_build_active':
            return t('authorEntity.autopilot.state.build', {
                defaultValue: 'Waiting for entity pass {{id}} to end.',
                id,
            });
        case 'resolve_started':
            return t('authorEntity.autopilot.state.resolveStarted', {
                defaultValue: 'Started resolve run {{id}} for {{inputs}} names.',
                id,
                inputs: formatNumber(a.resolve_inputs),
            });
        case 'build_started':
            return t('authorEntity.autopilot.state.buildStarted', {
                defaultValue: 'Started entity pass {{id}}.',
                id,
            });
        case 'idle':
            return t('authorEntity.autopilot.state.idle', 'Nothing to do.');
        default:
            return t('authorEntity.autopilot.state.unknown', 'Not checked yet.');
    }
};

/** The lines of the autopilot section below the switch. */
const autopilotLines = (t: T, a: AuthorAutopilotStatus): string[] => {
    const lines = [stateLine(t, a)];
    const note = RESOLVE_NOTES[a.resolve_note ?? ''];
    if (note && a.resolve_note !== 'started') {
        lines.push(
            t('authorEntity.autopilot.resolve', {
                defaultValue: 'Resolve: {{note}}.',
                note: t(note[0], note[1]),
            }),
        );
    }
    if (a.build_pending && a.state !== 'build_started' && a.state !== 'entity_build_active') {
        lines.push(
            t(
                'authorEntity.autopilot.buildPending',
                'The selections changed since the last pass: a pass is due.',
            ),
        );
    }
    if (a.skipped_units > 0 && a.state !== 'build_started' && a.state !== 'entity_build_active') {
        lines.push(
            t('authorEntity.autopilot.skipped', {
                defaultValue:
                    'The last pass skipped {{units}} units whose rows were held; another pass is due {{date}}.',
                units: formatNumber(a.skipped_units),
                date: a.skipped_retry_at ? formatTime(a.skipped_retry_at) : '',
            }),
        );
    }
    if (a.last_action && a.last_action_at) {
        lines.push(
            a.last_action === 'resolve'
                ? t('authorEntity.autopilot.lastResolve', {
                      defaultValue: 'Last started: resolve run {{id}}, {{date}}.',
                      id: a.last_action_run_id ?? '',
                      date: formatTime(a.last_action_at),
                  })
                : t('authorEntity.autopilot.lastBuild', {
                      defaultValue: 'Last started: entity pass {{id}}, {{date}}.',
                      id: a.last_action_run_id ?? '',
                      date: formatTime(a.last_action_at),
                  }),
        );
    }
    return lines;
};

/** The autopilot section: the switch and what the autopilot sees and does. */
const AutopilotSection: React.FC<{
    autopilot: AuthorAutopilotStatus;
    onChanged: () => Promise<void>;
}> = ({ autopilot, onChanged }) => {
    const { t } = useTranslation();
    const [busy, setBusy] = useState(false);
    const [errorText, setErrorText] = useState<string | null>(null);

    const toggle = async (enabled: boolean) => {
        setBusy(true);
        setErrorText(null);
        try {
            await adminApi.setAuthorAutopilot(enabled);
        } catch {
            setErrorText(t('authorEntity.error.other', 'The action failed.'));
        } finally {
            await onChanged();
            setBusy(false);
        }
    };

    return (
        <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between gap-3">
                <label htmlFor="author-autopilot" className="text-sm font-medium">
                    {t('authorEntity.autopilot.title', 'Autopilot after a scan')}
                </label>
                <Switch
                    id="author-autopilot"
                    disabled={busy}
                    checked={autopilot.enabled}
                    onCheckedChange={toggle}
                />
            </div>
            <p className="text-sm text-muted-foreground">
                {t('authorEntity.autopilot.hint', {
                    defaultValue:
                        'Every minute, once no book was added for {{minutes}} min and local normalization is done, and while no LLM run and no entity pass is active (whoever started it): the never-asked names of the registered strata go to a resolve run, otherwise, if the selections changed, the entities are built. It never starts a check (eval) and never registers a stratum. A pause of {{minutes}} min within a long scan counts as quiet; the books after it are taken up by a later check.',
                    minutes: formatNumber(Math.round(autopilot.quiet_seconds / 60)),
                })}
            </p>
            {errorText !== null && (
                <Alert variant="destructive">
                    <AlertDescription>{errorText}</AlertDescription>
                </Alert>
            )}
            {!autopilot.enabled ? (
                <p className="text-sm">
                    {t(
                        'authorEntity.autopilot.off',
                        'Off: new books wait for the buttons; a run it started goes on.',
                    )}
                </p>
            ) : (
                autopilotLines(t, autopilot).map((line) => (
                    <p key={line} className="text-sm tabular-nums">
                        {line}
                    </p>
                ))
            )}
            {autopilot.failures > 0 && (
                <p className="text-sm tabular-nums text-destructive">
                    {t('authorEntity.autopilot.failed', {
                        defaultValue:
                            'The last check failed {{failures}} times in a row ({{error}}); next attempt {{date}}.',
                        failures: autopilot.failures,
                        error: autopilot.last_error_class ?? '',
                        date: autopilot.next_attempt_at
                            ? formatTime(autopilot.next_attempt_at)
                            : '',
                    })}
                </p>
            )}
        </div>
    );
};

/** A candidate kind, by its closed code; an unknown code shows as it is. */
const MERGE_KINDS: Record<string, [string, string]> = {
    swap: ['authorEntity.merges.kind.swap', 'swapped name parts'],
    initials: ['authorEntity.merges.kind.initials', 'initials'],
    translit: ['authorEntity.merges.kind.translit', 'transliteration'],
    bucket: ['authorEntity.merges.kind.bucket', 'a book of a short name'],
};

const mergeKind = (t: T, kind: string): string => {
    const label = MERGE_KINDS[kind];
    return label ? t(label[0], label[1]) : kind;
};

/** A decision's state in the newest pass, as one phrase. */
const decisionState = (t: T, d: AuthorMergeDecision): string => {
    const reason = d.reason ?? '';
    switch (d.state) {
        case 'applied':
            return t('authorEntity.merges.state.applied', 'merged');
        case 'lapsed':
            return t('authorEntity.merges.state.lapsed', {
                defaultValue: 'out of date ({{reason}})',
                reason,
            });
        case 'conflict':
            return t('authorEntity.merges.state.conflict', {
                defaultValue: 'not merged: a conflict ({{reason}})',
                reason,
            });
        default:
            return t('authorEntity.merges.state.pending', 'waiting for a pass');
    }
};

/** A veto's state in the newest pass, as one phrase. */
const vetoState = (t: T, v: AuthorMergeVeto): string => {
    switch (v.state) {
        case 'anchored':
            return t('authorEntity.merges.veto.anchored', 'anchored');
        case 'unanchored':
            return t('authorEntity.merges.veto.unanchored', 'lost its books');
        case 'gone':
            return t('authorEntity.merges.veto.gone', 'a side left the catalog');
        default:
            return t('authorEntity.merges.veto.pending', 'waiting for a pass');
    }
};

const describeMergeError = (t: T, error: unknown): string => {
    switch (closedErrorCode(error)) {
        case 'decision_not_live':
            return t('authorEntity.merges.error.decision', 'The merge is already undone.');
        case 'veto_not_live':
            return t('authorEntity.merges.error.veto', 'The veto is already retired.');
        case 'veto_side_unrecorded':
            return t(
                'authorEntity.merges.error.unrecorded',
                'The books of a side are not recorded: the merge cannot be undone into a veto.',
            );
        case 'veto_sides_invalid':
            return t(
                'authorEntity.merges.error.sides',
                'The chosen entities are not the two sides of the veto.',
            );
        default:
            return t('authorEntity.error.other', 'The action failed.');
    }
};

/** The choice of one side of a re-anchor. */
const SideChoice: React.FC<{
    label: string;
    entities: AuthorMergeVetoEntity[];
    value: string;
    onChange: (id: string) => void;
}> = ({ label, entities, value, onChange }) => (
    <div className="flex flex-col gap-1">
        <span className="text-sm text-muted-foreground">{label}</span>
        <Select value={value} onValueChange={onChange}>
            <SelectTrigger aria-label={label}>
                <SelectValue />
            </SelectTrigger>
            <SelectContent>
                {entities.map((e) => (
                    <SelectItem key={e.id} value={e.id}>
                        {e.display_name}
                    </SelectItem>
                ))}
            </SelectContent>
        </Select>
    </div>
);

/** One veto: its state, its retire and its re-anchor. */
const VetoItem: React.FC<{
    veto: AuthorMergeVeto;
    busy: boolean;
    act: (action: () => Promise<unknown>) => Promise<void>;
}> = ({ veto, busy, act }) => {
    const { t } = useTranslation();
    const [reanchoring, setReanchoring] = useState(false);
    const [sideA, setSideA] = useState(veto.side_a[0]?.id ?? '');
    const [sideB, setSideB] = useState(veto.side_b[0]?.id ?? '');
    const retire = () => {
        if (
            window.confirm(
                t(
                    'authorEntity.merges.retireConfirm',
                    'Retire the veto? The pair may be merged again.',
                ),
            )
        ) {
            void act(() => adminApi.retireMergeVeto(veto.id));
        }
    };
    return (
        <li className="flex flex-col gap-1">
            <p className="text-sm tabular-nums">
                {t('authorEntity.merges.veto.line', {
                    defaultValue:
                        'Veto {{id}}: {{state}} · books held {{held}}, lost {{lost}}, deleted {{gone}}; fingerprints added {{widened}}',
                    id: veto.id,
                    state: vetoState(t, veto),
                    held: formatNumber(veto.held_books),
                    lost: formatNumber(veto.lost_books),
                    gone: formatNumber(veto.gone_books),
                    widened: formatNumber(veto.widened_fingerprints),
                })}
            </p>
            <div className="flex flex-wrap gap-2">
                <Button variant="outline" size="sm" disabled={busy} onClick={retire}>
                    {t('authorEntity.merges.retire', 'Retire veto')}
                </Button>
                {veto.side_a.length > 0 && veto.side_b.length > 0 && (
                    <Button
                        variant="outline"
                        size="sm"
                        disabled={busy}
                        onClick={() => setReanchoring((open) => !open)}
                    >
                        {t('authorEntity.merges.reanchor', 'Re-anchor veto')}
                    </Button>
                )}
            </div>
            {reanchoring && (
                <div className="flex flex-col gap-2">
                    <SideChoice
                        label={t('authorEntity.merges.sideA', 'Side A now')}
                        entities={veto.side_a}
                        value={sideA}
                        onChange={setSideA}
                    />
                    <SideChoice
                        label={t('authorEntity.merges.sideB', 'Side B now')}
                        entities={veto.side_b}
                        value={sideB}
                        onChange={setSideB}
                    />
                    <div>
                        <Button
                            size="sm"
                            disabled={busy || sideA === '' || sideB === ''}
                            onClick={() =>
                                act(() => adminApi.reanchorMergeVeto(veto.id, sideA, sideB))
                            }
                        >
                            {t('authorEntity.merges.reanchorConfirm', 'Re-anchor on these two')}
                        </Button>
                    </div>
                </div>
            )}
        </li>
    );
};

/** The merges section: the model's decisions and the vetoes of revokes. */
const MergesSection: React.FC<{ onChanged: () => Promise<void> }> = ({ onChanged }) => {
    const { t } = useTranslation();
    const [merges, setMerges] = useState<AuthorMerges | null>(null);
    const [busy, setBusy] = useState(false);
    const [errorText, setErrorText] = useState<string | null>(null);

    const load = useCallback(async () => {
        try {
            const response = await adminApi.getAuthorMerges();
            setMerges(response.merges);
        } catch {
            setErrorText(t('authorEntity.merges.loadFailed', 'Could not read the merges.'));
        }
    }, [t]);

    useEffect(() => {
        load();
    }, [load]);

    const act = async (action: () => Promise<unknown>) => {
        setBusy(true);
        setErrorText(null);
        try {
            await action();
        } catch (error) {
            setErrorText(describeMergeError(t, error));
        } finally {
            await load();
            await onChanged();
            setBusy(false);
        }
    };

    const revoke = (d: AuthorMergeDecision) => {
        if (
            window.confirm(
                t('authorEntity.merges.revokeConfirm', {
                    defaultValue:
                        'Undo the merge of {{a}} and {{b}}? The pair stays apart until the veto is retired.',
                    a: d.side_a.display_name,
                    b: d.side_b.display_name,
                }),
            )
        ) {
            void act(() => adminApi.revokeMergeDecision(d.id));
        }
    };

    return (
        <div className="flex flex-col gap-2">
            <h5 className="text-sm font-medium">
                {t('authorEntity.merges.title', 'Merges by the model')}
            </h5>
            <p className="text-sm text-muted-foreground">
                {t(
                    'authorEntity.merges.hint',
                    'Two records the model found to be one person. Undoing a merge splits the pair at the next pass and leaves a veto: no pass joins them again until it is retired.',
                )}
            </p>
            {errorText !== null && (
                <Alert variant="destructive">
                    <AlertDescription>{errorText}</AlertDescription>
                </Alert>
            )}
            {merges?.unanchored && (
                <Alert>
                    <AlertDescription>
                        {t(
                            'authorEntity.merges.unanchored',
                            'A veto lost its books (re-extracted with other data): no question goes to the model until it is re-anchored or retired.',
                        )}
                    </AlertDescription>
                </Alert>
            )}
            {merges !== null && merges.decisions_total === 0 && (
                <p className="text-sm text-muted-foreground">
                    {t('authorEntity.merges.none', 'No merge decision yet.')}
                </p>
            )}
            {merges !== null && merges.decisions.length > 0 && (
                <ul className="flex flex-col gap-2">
                    {merges.decisions.map((d) => (
                        <li
                            key={d.id}
                            className="flex flex-wrap items-center justify-between gap-2"
                        >
                            <span className="text-sm tabular-nums">
                                {t('authorEntity.merges.decision', {
                                    defaultValue:
                                        '{{id}}: {{a}} ({{booksA}}) and {{b}} ({{booksB}}) · {{kind}} · {{state}}',
                                    id: d.id,
                                    a: d.side_a.display_name,
                                    booksA: formatNumber(d.side_a.books),
                                    b: d.side_b.display_name,
                                    booksB: formatNumber(d.side_b.books),
                                    kind: mergeKind(t, d.kind),
                                    state: decisionState(t, d),
                                })}
                            </span>
                            <Button
                                variant="outline"
                                size="sm"
                                disabled={busy}
                                onClick={() => revoke(d)}
                            >
                                {t('authorEntity.merges.revoke', 'Undo merge')}
                            </Button>
                        </li>
                    ))}
                </ul>
            )}
            {merges !== null && merges.decisions_total > merges.decisions.length && (
                <p className="text-sm text-muted-foreground tabular-nums">
                    {t('authorEntity.merges.more', {
                        defaultValue: 'The newest {{shown}} of {{total}}.',
                        shown: formatNumber(merges.decisions.length),
                        total: formatNumber(merges.decisions_total),
                    })}
                </p>
            )}
            {merges !== null && merges.vetoes.length > 0 && (
                <ul className="flex flex-col gap-2">
                    {merges.vetoes.map((v) => (
                        <VetoItem key={v.id} veto={v} busy={busy} act={act} />
                    ))}
                </ul>
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
    const watching = status?.autopilot?.enabled === true;
    useEffect(() => {
        if (!active && !watching) return undefined;
        const timer = window.setInterval(load, active ? POLL_INTERVAL_MS : IDLE_POLL_INTERVAL_MS);
        return () => window.clearInterval(timer);
    }, [active, watching, load]);

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

            {status?.autopilot && (
                <AutopilotSection autopilot={status.autopilot} onChanged={load} />
            )}

            {status !== null && (
                <WikidataSection wikidata={status.wikidata ?? null} onChanged={load} />
            )}

            {status !== null && <MergesSection onChanged={load} />}

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
