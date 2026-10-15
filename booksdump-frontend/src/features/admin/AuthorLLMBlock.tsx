import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import * as adminApi from '@/api/admin';
import type { AuthorLLMRun, AuthorLLMStatus, AuthorLLMStep } from '@/api/admin';
import { closedErrorCode } from '@/api/admin';

/**
 * The LLM block of the author metadata card. It shows one chain — check
 * quality, check everything, resolve a pilot, resolve everything, check the
 * swaps, swaps pilot, swap everything — with every step the server built; a
 * step is enabled only when the server says the one before it completed under
 * the current configuration. A step the server marks not built is shown,
 * disabled. Under the
 * chain: the run to show (its stage and progress, tokens against its
 * estimate and limit, and the cost when a price is configured), the pauses of
 * the provider and of the participants with their action, and the one-line
 * summary of a completed check with its report download. With the LLM not
 * configured the block is one line, without buttons.
 *
 * Every action starts at once on the server; the block re-reads after it and
 * every few seconds while a run is active.
 */

/** How often the block re-reads while a run is active. */
const POLL_INTERVAL_MS = 5000;

const ACTIVE = new Set(['pending', 'running', 'paused']);

type T = ReturnType<typeof useTranslation>['t'];

const formatNumber = (value: number) => value.toLocaleString();

const formatCost = (value: number) =>
    value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });

// formatShare is a changed share in percent, at most one decimal.
const formatShare = (changed: number, total: number) =>
    (total > 0 ? (changed * 100) / total : 0).toLocaleString(undefined, {
        maximumFractionDigits: 1,
    });

const stepLabels = (t: T): Record<string, string> => ({
    eval_pilot: t('authorLLM.step.eval_pilot', 'Check quality'),
    eval_full: t('authorLLM.step.eval_full', 'Check everything'),
    resolve_pilot: t('authorLLM.step.resolve_pilot', 'Resolve: pilot 500'),
    resolve_full: t('authorLLM.step.resolve_full', 'Resolve everything'),
    swap_eval: t('authorLLM.step.swap_eval', 'Swaps: check'),
    swap_pilot: t('authorLLM.step.swap_pilot', 'Swaps: pilot 500'),
    swap_full: t('authorLLM.step.swap_full', 'Swap everything'),
});

const stageLabels = (t: T): Record<string, string> => ({
    freeze: t('authorLLM.stage.freeze', 'preparing the sets'),
    fingerprint: t('authorLLM.stage.fingerprint', 'model fingerprints'),
    anchor: t('authorLLM.stage.anchor', 'anchor'),
    judge: t('authorLLM.stage.judge', 'judges'),
    candidates: t('authorLLM.stage.candidates', 'candidates'),
    repeat: t('authorLLM.stage.repeat', 'repeats'),
    report: t('authorLLM.stage.report', 'report'),
    enqueue: t('authorLLM.stage.enqueue', 'queueing'),
    resolve: t('authorLLM.stage.resolve', 'resolving'),
});

const runLabel = (t: T, run: AuthorLLMRun): string => {
    if (run.kind === 'eval') {
        if (run.mode === 'dev') return t('authorLLM.run.eval_dev', 'Model settings check');
        if (run.mode === 'full') return t('authorLLM.run.eval_full', 'Full check');
        if (run.mode === 'swap') return t('authorLLM.run.eval_swap', 'Swaps check');
        return t('authorLLM.run.eval_pilot', 'Quality check');
    }
    if (run.kind === 'resolve') {
        if (run.mode === 'full') return t('authorLLM.run.resolve_full', 'Resolve everything');
        return t('authorLLM.run.resolve_pilot', 'Resolve pilot');
    }
    if (run.kind === 'swap') {
        if (run.mode === 'full') return t('authorLLM.run.swap_full', 'Swap everything');
        return t('authorLLM.run.swap_pilot', 'Swaps pilot');
    }
    return t('authorLLM.run.other', 'LLM run');
};

const disabledReason = (t: T, reason?: string): string => {
    switch (reason) {
        case 'llm_participants_invalid':
            return t(
                'authorLLM.notConfiguredModels',
                'LLM is not configured: four different models are needed',
            );
        case 'workers_disabled':
            return t(
                'authorLLM.notConfiguredWorkers',
                'LLM is not configured: author metadata processing is off',
            );
        default:
            return t('authorLLM.notConfigured', 'LLM is not configured');
    }
};

const participantPause = (t: T, reason: string): string => {
    switch (reason) {
        case 'model_mismatch':
            return t(
                'authorLLM.pause.model_mismatch',
                'the model answers differently than at the check — auto-acceptance stopped',
            );
        case 'drift':
            return t(
                'authorLLM.pause.drift',
                'the provider switched the model — Continue re-learns it and asks its answers again',
            );
        case 'relearn_unstable':
            return t(
                'authorLLM.pause.relearn_unstable',
                'the re-learned model kept changing — Continue tries to re-learn it again',
            );
        case 'check_unavailable':
            return t(
                'authorLLM.pause.check_unavailable',
                'the model check kept failing for a day — Continue checks it again',
            );
        case 'auth':
            return t('authorLLM.pause.auth', 'the provider refuses the key');
        case 'bad_request':
            return t('authorLLM.pause.bad_request', 'the provider rejects the requests');
        case 'over_reserve':
            return t(
                'authorLLM.pause.over_reserve',
                'answers are longer than the configured ceiling',
            );
        default:
            return t('authorLLM.pause.other', 'paused');
    }
};

const endpointPause = (t: T, reason: string): string =>
    reason === 'quota_exhausted'
        ? t('authorLLM.endpointQuota', 'LLM: the provider ran out of quota or money')
        : t('authorLLM.endpointPaused', 'LLM: the provider is paused');

const describeError = (t: T, error: unknown): string => {
    switch (closedErrorCode(error)) {
        case 'active_run_exists':
            return t('authorLLM.error.active_run_exists', 'Another LLM run is already active.');
        case 'prerequisite_incomplete':
            return t('authorLLM.error.prerequisite_incomplete', 'Finish the previous step first.');
        case 'run_kind_unavailable':
            return t(
                'authorLLM.error.run_kind_unavailable',
                'This step comes in the next version.',
            );
        case 'llm_not_configured':
            return t('authorLLM.notConfigured', 'LLM is not configured');
        case 'run_config_changed':
            return t(
                'authorLLM.error.run_config_changed',
                'This run belongs to an earlier configuration; it can only be closed.',
            );
        case 'invalid_transition':
            return t(
                'authorLLM.error.invalid_transition',
                'The run has changed; the block was refreshed.',
            );
        case 'report_stale':
            return t(
                'authorLLM.error.report_stale',
                "The run's answers changed while the report was computed; try again.",
            );
        default:
            return t('authorLLM.error.other', 'The action failed.');
    }
};

/** The run's status line while it is active. */
const activeLine = (t: T, run: AuthorLLMRun): string => {
    const stage = stageLabels(t)[run.stage ?? ''] ?? run.stage ?? '';
    if (run.progress && run.progress.total > 0) {
        return t('authorLLM.runProgress', {
            defaultValue: '{{run}}: {{stage}} · {{done}} of {{total}}',
            run: runLabel(t, run),
            stage,
            done: formatNumber(run.progress.done),
            total: formatNumber(run.progress.total),
        });
    }
    return t('authorLLM.runStage', {
        defaultValue: '{{run}}: {{stage}}',
        run: runLabel(t, run),
        stage,
    });
};

const tokensLine = (t: T, run: AuthorLLMRun): string =>
    run.estimate_tokens > 0
        ? t('authorLLM.tokens', {
              defaultValue: 'Tokens: {{spent}} of ~{{estimate}} (paused at {{limit}})',
              spent: formatNumber(run.spent_tokens),
              estimate: formatNumber(run.estimate_tokens),
              limit: formatNumber(run.limit_tokens),
          })
        : t('authorLLM.tokensSpent', {
              defaultValue: 'Tokens: {{spent}}',
              spent: formatNumber(run.spent_tokens),
          });

const AuthorLLMBlock: React.FC = () => {
    const { t } = useTranslation();
    const [status, setStatus] = useState<AuthorLLMStatus | null>(null);
    const [loadFailed, setLoadFailed] = useState(false);
    const [busy, setBusy] = useState(false);
    const [errorText, setErrorText] = useState<string | null>(null);
    const generation = useRef(0);

    const load = useCallback(async () => {
        const current = ++generation.current;
        try {
            const response = await adminApi.getAuthorLLMStatus();
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

    const act = async (action: () => Promise<unknown>) => {
        setBusy(true);
        setErrorText(null);
        try {
            await action();
        } catch (error) {
            setErrorText(describeError(t, error));
        } finally {
            await load();
            setBusy(false);
        }
    };

    const downloadReport = (id: number, version?: number) =>
        act(async () => {
            const blob =
                version === undefined
                    ? await adminApi.getAuthorLLMEvalReport(id)
                    : await adminApi.getAuthorLLMEvalReport(id, version);
            const url = window.URL.createObjectURL(blob);
            const link = document.createElement('a');
            link.href = url;
            link.download = `author-llm-${run?.kind === 'resolve' || run?.kind === 'swap' ? run.kind : 'eval'}-${id}${
                version === undefined ? '' : `-v${version}`
            }.json`;
            document.body.appendChild(link);
            link.click();
            link.remove();
            window.URL.revokeObjectURL(url);
        });

    const title = <h4 className="text-sm font-medium">{t('authorLLM.title', 'LLM review')}</h4>;

    if (status === null) {
        return (
            <section className="flex flex-col gap-2 border-t pt-3">
                {title}
                {loadFailed ? (
                    <Alert variant="destructive">
                        <AlertDescription>
                            {t('authorLLM.loadFailed', 'Could not read the LLM state.')}
                        </AlertDescription>
                    </Alert>
                ) : (
                    <p className="text-sm text-muted-foreground">{t('loading', 'Loading...')}</p>
                )}
            </section>
        );
    }

    if (!status.enabled) {
        return (
            <section className="flex flex-col gap-2 border-t pt-3">
                {title}
                <p className="text-sm text-muted-foreground">{disabledReason(t, status.reason)}</p>
            </section>
        );
    }

    const steps: AuthorLLMStep[] = status.steps ?? [];
    const labels = stepLabels(t);
    const unavailable = steps.some((s) => !s.available);
    const report = run?.status === 'completed' ? run.report : undefined;
    const resolved = run?.status === 'completed' ? run.resolve : undefined;

    return (
        <section className="flex flex-col gap-2 border-t pt-3">
            {title}

            {loadFailed && (
                <Alert variant="destructive">
                    <AlertDescription>
                        {t('authorLLM.loadFailed', 'Could not read the LLM state.')}
                    </AlertDescription>
                </Alert>
            )}
            {errorText !== null && (
                <Alert variant="destructive">
                    <AlertDescription>{errorText}</AlertDescription>
                </Alert>
            )}

            {status.endpoint_paused_reason && (
                <div className="flex flex-wrap items-center gap-2">
                    <p className="text-sm text-destructive">
                        {endpointPause(t, status.endpoint_paused_reason)}
                    </p>
                    <Button
                        size="sm"
                        variant="outline"
                        disabled={busy}
                        onClick={() => act(() => adminApi.resumeAuthorLLMEndpoint())}
                    >
                        {t('authorLLM.continue', 'Continue')}
                    </Button>
                </div>
            )}
            {(status.participants ?? [])
                .filter((p) => p.paused_reason)
                .map((p) => (
                    <div key={p.slot} className="flex flex-wrap items-center gap-2">
                        <p className="text-sm text-destructive">
                            {`${p.model}: ${participantPause(t, p.paused_reason ?? '')}`}
                        </p>
                        <Button
                            size="sm"
                            variant="outline"
                            disabled={busy}
                            onClick={() => act(() => adminApi.resumeAuthorLLMParticipant(p.slot))}
                        >
                            {t('authorLLM.continue', 'Continue')}
                        </Button>
                    </div>
                ))}

            {run !== null && active && (
                <div className="flex flex-col gap-1">
                    <p className="text-sm tabular-nums">{activeLine(t, run)}</p>
                    {run.status === 'paused' && (
                        <p className="text-sm text-muted-foreground">
                            {run.paused_reason === 'budget'
                                ? t(
                                      'authorLLM.pausedBudget',
                                      'Paused: the run reached its token limit',
                                  )
                                : t('authorLLM.paused', 'Paused')}
                        </p>
                    )}
                    <p className="text-sm tabular-nums text-muted-foreground">
                        {tokensLine(t, run)}
                        {run.cost !== undefined && status.price_unit
                            ? ` · ≈ ${formatCost(run.cost)} ${status.price_unit}`
                            : null}
                    </p>
                    {!run.current_config && (
                        <p className="text-sm text-muted-foreground">
                            {t(
                                'authorLLM.otherConfig',
                                'This run belongs to an earlier configuration and cannot go on; close it to start a new one.',
                            )}
                        </p>
                    )}
                    <div className="flex flex-wrap gap-2">
                        {/* A run of an older configuration is never resumed or
                            paused: no worker of the current one moves it. */}
                        {run.current_config && (
                            <Button
                                variant="outline"
                                disabled={busy}
                                onClick={() =>
                                    act(() =>
                                        run.status === 'paused'
                                            ? adminApi.resumeAuthorLLMRun(run.id)
                                            : adminApi.pauseAuthorLLMRun(run.id),
                                    )
                                }
                            >
                                {run.status === 'paused'
                                    ? t('authorLLM.resume', 'Resume')
                                    : t('authorLLM.pause', 'Pause')}
                            </Button>
                        )}
                        {(!run.current_config || run.status === 'paused') && (
                            <Button
                                variant="outline"
                                disabled={busy}
                                onClick={() => act(() => adminApi.closeAuthorLLMRun(run.id))}
                            >
                                {t('authorLLM.close', 'Close run')}
                            </Button>
                        )}
                    </div>
                </div>
            )}

            {run !== null && report && (
                <div className="flex flex-col gap-1">
                    <p className="text-sm">
                        {t('authorLLM.reportSummary', {
                            defaultValue:
                                '{{run}}: ✓ {{passed}} of {{total}} groups passed · the excerpt helps in {{helps}}',
                            run: runLabel(t, run),
                            passed: report.strata_passed,
                            total: report.strata_total,
                            helps: report.excerpt_helps,
                        })}
                    </p>
                    {(report.repeats ?? []).map((r) => (
                        <p
                            key={r.participant}
                            className="text-sm tabular-nums text-muted-foreground"
                        >
                            {t('authorLLM.repeatChanged', {
                                defaultValue: '{{participant}} · repeats: {{share}}% changed',
                                participant:
                                    status.participants?.find((p) => p.slot === r.participant)
                                        ?.model ?? r.participant,
                                share: formatShare(r.changed, r.total),
                            })}
                        </p>
                    ))}
                    {!report.registrable && (
                        <p className="text-sm text-muted-foreground">
                            {t(
                                'authorLLM.notRegistrable',
                                'Not enough evidence to start resolving.',
                            )}
                        </p>
                    )}
                    <p className="text-sm tabular-nums text-muted-foreground">
                        {tokensLine(t, run)}
                        {run.cost !== undefined && status.price_unit
                            ? ` · ≈ ${formatCost(run.cost)} ${status.price_unit}`
                            : null}
                    </p>
                    {(report.versions ?? []).length > 1 &&
                        (report.versions ?? []).map((v) => (
                            <div
                                key={v.version}
                                className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground"
                            >
                                <span className="tabular-nums">
                                    {t('authorLLM.reportVersion', {
                                        defaultValue: 'Version {{version}} · {{verdict}}',
                                        version: v.version,
                                        verdict: v.registrable
                                            ? t('authorLLM.versionRegistrable', 'registers groups')
                                            : t(
                                                  'authorLLM.versionNotRegistrable',
                                                  'registers nothing',
                                              ),
                                    })}
                                </span>
                                {v.version !== report.version && (
                                    <Button
                                        variant="link"
                                        size="sm"
                                        className="h-auto p-0"
                                        disabled={busy}
                                        onClick={() => downloadReport(run.id, v.version)}
                                    >
                                        {t('authorLLM.reportButton', 'Report')}
                                    </Button>
                                )}
                            </div>
                        ))}
                    <div className="flex flex-wrap gap-2">
                        <Button
                            variant="outline"
                            disabled={busy}
                            onClick={() => downloadReport(run.id)}
                        >
                            {t('authorLLM.reportButton', 'Report')}
                        </Button>
                        {run.kind === 'eval' && run.current_config && (
                            <Button
                                variant="outline"
                                disabled={busy}
                                onClick={() =>
                                    act(() => adminApi.recomputeAuthorLLMEvalReport(run.id))
                                }
                            >
                                {t('authorLLM.recomputeButton', 'Recompute report')}
                            </Button>
                        )}
                    </div>
                </div>
            )}

            {run !== null && resolved && (
                <div className="flex flex-col gap-1">
                    <p className="text-sm">
                        {t('authorLLM.resolveSummary', {
                            defaultValue: '{{run}}: ✓ {{applied}} of {{total}} applied',
                            run: runLabel(t, run),
                            applied: formatNumber(resolved.applied),
                            total: formatNumber(resolved.total),
                        })}
                    </p>
                    <p className="text-sm tabular-nums text-muted-foreground">
                        {tokensLine(t, run)}
                        {run.cost !== undefined && status.price_unit
                            ? ` · ≈ ${formatCost(run.cost)} ${status.price_unit}`
                            : null}
                    </p>
                    <div className="flex flex-wrap gap-2">
                        <Button
                            variant="outline"
                            disabled={busy}
                            onClick={() => downloadReport(run.id)}
                        >
                            {t('authorLLM.reportButton', 'Report')}
                        </Button>
                    </div>
                </div>
            )}

            {run !== null && run.status === 'closed' && (
                <p className="text-sm text-muted-foreground">
                    {t('authorLLM.closed', {
                        defaultValue: '{{run}}: closed',
                        run: runLabel(t, run),
                    })}
                </p>
            )}

            {run !== null && run.status === 'failed_systemic' && (
                <p className="text-sm text-destructive">
                    {t('authorLLM.failed', {
                        defaultValue: '{{run}} stopped with an error ({{class}})',
                        run: runLabel(t, run),
                        class: run.last_error_class ?? '',
                    })}
                </p>
            )}

            <ol className="flex flex-wrap gap-2">
                {steps.map((s) => (
                    <li key={s.key}>
                        <Button
                            variant={s.enabled ? 'default' : 'outline'}
                            disabled={busy || !s.enabled}
                            onClick={() => act(() => adminApi.startAuthorLLMRun(s.kind, s.mode))}
                        >
                            {s.done ? `✓ ${labels[s.key]}` : labels[s.key]}
                        </Button>
                    </li>
                ))}
            </ol>
            {unavailable && (
                <p className="text-sm text-muted-foreground">
                    {t('authorLLM.notAvailable', 'This step comes in the next version.')}
                </p>
            )}
            <p className="text-xs tabular-nums text-muted-foreground">
                {t('authorLLM.budget', {
                    defaultValue: 'All runs together: {{spent}} of {{budget}} tokens',
                    spent: formatNumber(status.spent_tokens),
                    budget: formatNumber(status.budget_tokens),
                })}
            </p>
        </section>
    );
};

export default AuthorLLMBlock;
