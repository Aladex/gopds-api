import React from 'react';
import i18n from 'i18next';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';

import { Progress } from '@/shared/ui/progress';
import { cn } from '@/shared/lib/utils';
import type { NodeState, Reason, StageKey } from './pipeline';

/*
 * The small pieces every part of the scan tab shares: the warning amber, the
 * labelled bar, the state dot, the stage panel's frame and the reason line.
 */

/**
 * The palette has no warning colour, so Reset — destructive but reversible —
 * borrows the amber the rescan dialog already uses. Delete keeps the one real
 * destructive colour, which is the point of having only one.
 */
export const WARNING_BUTTON = 'border-amber-500/60 text-amber-600 dark:text-amber-400';

/** The same amber as text, for a warning node's reason and a warning strip. */
export const WARNING_TEXT = 'text-amber-600 dark:text-amber-400';

/** A labelled figure with its bar, used by both the scan and fix-scan blocks. */
export const ProgressRow: React.FC<{ label: string; percent: number }> = ({ label, percent }) => (
    <div className="flex flex-col gap-1">
        <div className="flex items-center justify-between">
            <span className="text-sm text-muted-foreground">{label}</span>
            <span className="text-sm font-semibold tabular-nums">{Math.round(percent)}%</span>
        </div>
        <Progress value={Math.min(100, percent)} aria-label={label} />
    </div>
);

/** The dot that says a stage's state at a glance; waiting is a hollow amber ring. */
export const StateDot: React.FC<{ state: NodeState; className?: string }> = ({
    state,
    className,
}) => (
    <span
        aria-hidden="true"
        className={cn(
            'inline-block size-2 shrink-0 rounded-full',
            state === 'ok' && 'bg-primary',
            state === 'warning' && 'bg-amber-500 dark:bg-amber-400',
            state === 'stuck' && 'bg-destructive',
            state === 'waiting' && 'border-[1.5px] border-amber-500 dark:border-amber-400',
            className,
        )}
    />
);

/** A count as the page writes numbers. */
export const formatCount = (value: number): string => value.toLocaleString(i18n.language);

/** A day, as dd.MM in the reader's language. */
export const formatDay = (iso: string): string => {
    const at = new Date(iso);
    return Number.isNaN(at.getTime())
        ? ''
        : at.toLocaleDateString(i18n.language, { day: '2-digit', month: '2-digit' });
};

/** A moment, as dd.MM, HH:mm in the reader's language. */
export const formatMoment = (iso: string): string => {
    const at = new Date(iso);
    return Number.isNaN(at.getTime())
        ? ''
        : at.toLocaleString(i18n.language, {
              day: '2-digit',
              month: '2-digit',
              hour: '2-digit',
              minute: '2-digit',
          });
};

/** Values that are a day; the rest that are dates are moments. */
const DAY_VALUES: ReadonlySet<string> = new Set(['date']);
const MOMENT_VALUES: ReadonlySet<string> = new Set(['since', 'next', 'retry']);
/** Numbers i18next needs as numbers: the plural count and the run ids. */
const RAW_VALUES: ReadonlySet<string> = new Set(['count', 'id']);

/** A stage's name. */
export const stageName = (t: TFunction, key: StageKey | string): string =>
    t(`scanPanel.stage.${key}`);

/**
 * A reason line in words: the derivation's key and values, its dates written
 * as days or moments, a stage value as the stage's name, and a pause time as
 * «с …» after the line. compact writes a moment as its day, for a node.
 */
export function reasonText(t: TFunction, reason: Reason, compact = false): string {
    const values: Record<string, string | number> = {};
    for (const [name, value] of Object.entries(reason.values ?? {})) {
        if (name === 'stage') {
            values[name] = stageName(t, String(value));
        } else if (DAY_VALUES.has(name)) {
            values[name] = formatDay(String(value));
        } else if (MOMENT_VALUES.has(name)) {
            // A node is narrow: there a moment is only its day.
            values[name] =
                value === ''
                    ? ''
                    : compact
                      ? formatDay(String(value))
                      : formatMoment(String(value));
        } else if (typeof value === 'number' && !RAW_VALUES.has(name)) {
            values[name] = formatCount(value);
        } else {
            values[name] = value;
        }
    }
    const line = t(`scanPanel.reason.${reason.key}`, values);
    return typeof values.since === 'string' && values.since !== ''
        ? `${line} · ${t('scanPanel.since', { date: values.since })}`
        : line;
}

interface StagePanelProps {
    stage: StageKey;
    state: NodeState;
    /** The badge next to the title. */
    badge?: React.ReactNode;
    /** The right side of the header: a button or a muted line. */
    aside?: React.ReactNode;
    /** The strips under the header. */
    strips?: React.ReactNode;
    /** Full-width folds under the body. */
    footer?: React.ReactNode;
    children: React.ReactNode;
}

/** A stage's panel: a card region named after the stage, its header, its strips, its body. */
export const StagePanel: React.FC<StagePanelProps> = ({
    stage,
    state,
    badge,
    aside,
    strips,
    footer,
    children,
}) => {
    const { t } = useTranslation();
    const titleID = `scan-panel-${stage}`;
    return (
        <section
            aria-labelledby={titleID}
            data-stage-panel={stage}
            className="flex min-w-0 flex-col rounded-xl border bg-card text-card-foreground shadow-sm"
        >
            <div className="flex flex-wrap items-center gap-2.5 border-b px-4 pt-3.5 pb-3 sm:px-5">
                <StateDot state={state} />
                <h3 id={titleID} className="text-base font-medium">
                    {stageName(t, stage)}
                </h3>
                {badge}
                <span className="flex-1" />
                {aside}
            </div>
            {strips}
            <div className="flex min-w-0 flex-col gap-4 px-4 pt-4 pb-5 sm:px-5">{children}</div>
            {footer}
        </section>
    );
};
