import type { AuthorMetadataRun } from '@/api/admin';

/*
 * The author metadata run's facts that both the card and the scan pipeline
 * read. They live apart from the card so the pipeline's sources can use them
 * without importing a component.
 */

/** The statuses of a run that is still on its way. */
export const ACTIVE_METADATA: ReadonlySet<string> = new Set(['pending', 'running', 'paused']);

const KNOWN_STATUSES: ReadonlySet<string> = new Set([
    ...ACTIVE_METADATA,
    'completed',
    'failed_systemic',
]);

const KNOWN_MODES: ReadonlySet<string> = new Set(['smoke', 'pilot_archive', 'full']);

/** Only a run whose id JSON parsing has not rounded, with a known status and mode, may be acted on. */
export const isUsableRun = (run: AuthorMetadataRun): boolean =>
    Number.isSafeInteger(run.id) &&
    run.id > 0 &&
    KNOWN_STATUSES.has(run.status) &&
    KNOWN_MODES.has(run.mode);

/** The extraction statuses that count as a book the pass could not read. */
const EXTRACTION_FAILURES = [
    'entry_missing',
    'invalid_fb2',
    'unsupported_encoding',
    'metadata_parse_failed',
    'archive_missing',
    'archive_unreadable',
] as const;

/** Books in archives the pass found missing or unreadable. */
export const archiveFailures = (run: AuthorMetadataRun): number =>
    (run.stages.extraction.by_status.archive_missing ?? 0) +
    (run.stages.extraction.by_status.archive_unreadable ?? 0);

/** Books the run could not read, in either stage. */
export function runErrors(run: AuthorMetadataRun): number {
    const byStatus = run.stages.extraction.by_status;
    return (
        EXTRACTION_FAILURES.reduce((sum, status) => sum + (byStatus[status] ?? 0), 0) +
        run.stages.local.failed
    );
}
