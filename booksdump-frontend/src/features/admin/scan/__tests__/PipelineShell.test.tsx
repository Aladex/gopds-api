import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import { MemoryRouter, Route, Routes } from 'react-router';

import BookScanning from '@/features/admin/BookScanning';
import * as adminApi from '@/api/admin';
import type { AuthorEntityStatus, AuthorMerges, AuthorMetadataReport } from '@/api/admin';
import { BookConversionProvider } from '@/context/BookConversionContext';
import { WebSocketProvider } from '@/context/WebSocketContext';
import type { PipelineInput } from '@/features/admin/scan/pipeline';
import en from '@/locales/en/translation.json';
import ru from '@/locales/ru/translation.json';
import { autopilot, entityStatus, healthy, twoProblems } from './pipelineFixtures';

/*
 * The scan tab as a pipeline: the chain on top, the selected stage's panel
 * under it. The API answers from the same fixtures the derivation is tested
 * with, and the real Russian catalogue renders the text.
 */

vi.mock('@/api/admin', async (importOriginal) => {
    const actual = await importOriginal<typeof import('@/api/admin')>();
    return {
        ...actual,
        getScanStatus: vi.fn(),
        listUnscannedArchives: vi.fn(),
        listScannedArchives: vi.fn(),
        listScanErrors: vi.fn(),
        getScanErrorFile: vi.fn(),
        getFixScanStatus: vi.fn(),
        getLatestAuthorMetadataRun: vi.fn(),
        getAuthorMetadataRunReport: vi.fn(),
        getAuthorMetadataRunArchives: vi.fn(),
        getAuthorLLMStatus: vi.fn(),
        getAuthorEntityStatus: vi.fn(),
        getAuthorMerges: vi.fn(),
        listAuthorReviewItems: vi.fn(),
        getAuthorLLMEvalReport: vi.fn(),
        getAuthorLLMAdversarial: vi.fn(),
        getAuthorLLMAudit: vi.fn(),
    };
});

const api = vi.mocked(adminApi);

class SilentSocket {
    onopen: (() => void) | null = null;
    onmessage: (() => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: (() => void) | null = null;
    close() {}
    send() {}
}

const merges = (unanchored = false): AuthorMerges => ({
    decisions: [],
    decisions_total: 0,
    vetoes: [],
    unanchored,
    registrations: [],
});

/** Every endpoint the tab reads answers from one pipeline fixture. */
function serve(input: PipelineInput) {
    const run = input.metadataRun;
    api.getScanStatus.mockResolvedValue(input.scanStatus ?? {});
    api.listUnscannedArchives.mockResolvedValue({ unscanned_archives: [] });
    api.listScannedArchives.mockResolvedValue({
        scanned_archives: input.scanned ?? [],
        total_count: input.scanned?.length ?? 0,
    });
    const newest = (input.scanned ?? []).find((a) => a.errors_count > 0 && a.books_count > 2000);
    api.listScanErrors.mockResolvedValue({
        errors:
            newest === undefined
                ? []
                : [
                      {
                          file_name: '826391.fb2',
                          archive_name: newest.name,
                          error: 'author_metadata: metadata_parse_failed',
                          timestamp: newest.scanned_at,
                      },
                  ],
    });
    api.getFixScanStatus.mockResolvedValue({});
    api.getLatestAuthorMetadataRun.mockResolvedValue({ run });
    api.getAuthorMetadataRunReport.mockResolvedValue({
        report: {
            ...run,
            ready: true,
            not_ready_reasons: [],
            duration_s: 1,
            db_growth_bytes: 0,
            by_class: {},
            by_script: {},
        } as AuthorMetadataReport,
    });
    api.getAuthorMetadataRunArchives.mockResolvedValue({ archives: [] });
    api.getAuthorLLMStatus.mockImplementation(async (base?: string) => ({
        status: base === undefined ? input.llmNames! : input.llmMerge!,
    }));
    api.getAuthorEntityStatus.mockResolvedValue({ status: input.entity! });
    api.getAuthorMerges.mockResolvedValue({ merges: merges(input.mergesUnanchored === true) });
    api.listAuthorReviewItems.mockResolvedValue({ items: [], next_cursor: null });
}

beforeAll(async () => {
    await i18n.use(initReactI18next).init({
        resources: { en: { translation: en }, ru: { translation: ru } },
        lng: 'ru',
        keySeparator: false,
        interpolation: { escapeValue: false },
    });
});

beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal('WebSocket', SilentSocket);
});

afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
});

const renderAt = (path: string) =>
    render(
        <BookConversionProvider>
            <WebSocketProvider isAuthenticated={true}>
                <MemoryRouter initialEntries={[path]}>
                    <Routes>
                        <Route path="/admin/book-scanning" element={<BookScanning />} />
                    </Routes>
                </MemoryRouter>
            </WebSocketProvider>
        </BookConversionProvider>,
    );

const node = (key: string) => {
    const found = document.querySelector<HTMLElement>(`[data-stage-node="${key}"]`);
    if (found === null) {
        throw new Error(`no node ${key}`);
    }
    return found;
};

/** The node of a stage once its source has answered. */
const settledNode = async (key: string) =>
    waitFor(() => {
        const found = node(key);
        expect(found).not.toHaveAttribute('data-loading');
        return found;
    });

const panel = (name: string) => screen.findByRole('region', { name });

it('healthy opens the review queue', async () => {
    serve(healthy(new Date()));
    renderAt('/admin/book-scanning');

    expect(await panel('Проверка человеком')).toBeInTheDocument();
    await waitFor(() => expect(api.listAuthorReviewItems).toHaveBeenCalled());
    expect(await settledNode('human')).toHaveAttribute('aria-current', 'true');
    expect(node('model')).not.toHaveAttribute('aria-current');
});

it('a stuck model opens the model panel with the other problem named', async () => {
    serve(twoProblems(new Date()));
    renderAt('/admin/book-scanning');

    const model = await panel('Модель');
    expect(within(model).getAllByRole('button', { name: 'Продолжить' }).length).toBeGreaterThan(0);
    expect(within(model).getByText(/Ещё одна проблема/)).toBeInTheDocument();

    fireEvent.click(within(model).getByRole('button', { name: 'Открыть «Архивы»' }));

    const archives = await panel('Архивы');
    expect(within(archives).getByText(/Ещё одна проблема/)).toBeInTheDocument();
    expect(within(archives).getByRole('button', { name: 'Открыть «Модель»' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Модель' })).toBeNull();
});

it('header lists both problems as pills', async () => {
    serve(twoProblems(new Date()));
    renderAt('/admin/book-scanning');

    await panel('Модель');
    const chain = screen.getByRole('region', { name: 'Конвейер' });
    const pills = within(chain)
        .getAllByRole('button')
        .filter((button) => button.hasAttribute('data-problem'));
    expect(pills.map((pill) => pill.getAttribute('data-problem'))).toEqual(['model', 'arch']);

    fireEvent.click(pills[1]);
    expect(await panel('Архивы')).toBeInTheDocument();
});

it('a chosen stage survives a new problem', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const now = new Date();
    serve(healthy(now));
    renderAt('/admin/book-scanning');

    fireEvent.click(await settledNode('names'));
    expect(await panel('Имена')).toBeInTheDocument();
    expect(node('names')).toHaveAttribute('aria-current', 'true');

    api.getAuthorEntityStatus.mockResolvedValue({
        status: entityStatus(now, {
            autopilot: autopilot(now, { failures: 2, last_error_class: 'db' }),
        }) as AuthorEntityStatus,
    });
    // The idle autopilot's entity poll.
    await act(async () => {
        await vi.advanceTimersByTimeAsync(15_000);
    });

    await waitFor(() => expect(node('ent')).toHaveAttribute('data-state', 'stuck'));
    expect(node('names')).toHaveAttribute('aria-current', 'true');
    expect(screen.getByRole('region', { name: 'Имена' })).toBeInTheDocument();
});

it('?tab=authors still lands on the queue', async () => {
    serve(twoProblems(new Date()));
    renderAt('/admin/book-scanning?tab=authors');

    expect(await panel('Проверка человеком')).toBeInTheDocument();
    await waitFor(() => expect(api.listAuthorReviewItems).toHaveBeenCalled());
    expect(screen.queryByRole('region', { name: 'Модель' })).toBeNull();
});

it('?stage=ent shows the entities panel', async () => {
    serve(healthy(new Date()));
    renderAt('/admin/book-scanning?stage=ent');

    expect(await panel('Сущности')).toBeInTheDocument();
    expect(await settledNode('ent')).toHaveAttribute('aria-current', 'true');
});

it('the shadow wording is gone', async () => {
    serve(healthy(new Date()));
    renderAt('/admin/book-scanning?stage=ent');

    const entities = await panel('Сущности');
    await within(entities).findByText(ru['authorEntity.autopilot.title']);
    expect(within(entities).queryByText(/тень|пока не видят/)).toBeNull();
});

it('policy paragraphs are folded', async () => {
    serve(healthy(new Date()));
    renderAt('/admin/book-scanning?stage=ent');

    const entities = await panel('Сущности');
    const autopilotHint = await within(entities).findByText((text) =>
        text.startsWith(ru['authorEntity.autopilot.hint'].split('{{')[0]),
    );
    const mergeHint = within(entities).getByText(ru['authorEntity.autopilot.merge.hint']);
    for (const hint of [autopilotHint, mergeHint]) {
        const fold = hint.closest('details');
        expect(fold).not.toBeNull();
        expect(fold).not.toHaveAttribute('open');
    }
});

it('fix metadata lives under Инструменты in Архивы', async () => {
    serve(healthy(new Date()));
    const view = renderAt('/admin/book-scanning');

    await panel('Проверка человеком');
    expect(screen.queryByRole('button', { name: ru.fixScanStart, hidden: true })).toBeNull();
    view.unmount();

    renderAt('/admin/book-scanning?stage=arch');
    const archives = await panel('Архивы');
    const fixes = screen.getAllByRole('button', { name: ru.fixScanStart, hidden: true });
    expect(fixes).toHaveLength(1);
    expect(archives).toContainElement(fixes[0]);
    const fold = fixes[0].closest('details');
    expect(fold?.querySelector('summary')).toHaveTextContent('Инструменты');
});

it('the budget line renders once', async () => {
    serve(healthy(new Date()));
    renderAt('/admin/book-scanning?stage=model');

    const model = await panel('Модель');
    const budget = ru['authorLLM.budget'].split('{{')[0];
    await waitFor(() =>
        expect(within(model).getAllByText((text) => text.startsWith(budget))).toHaveLength(1),
    );
});

it('no panel but the selected one is mounted', async () => {
    serve(healthy(new Date()));
    renderAt('/admin/book-scanning?stage=names');

    expect(await panel('Имена')).toBeInTheDocument();
    await settledNode('ent');
    expect(api.getAuthorMerges).toHaveBeenCalledTimes(1);
    expect(api.getAuthorLLMEvalReport).not.toHaveBeenCalled();
    expect(api.getAuthorLLMAdversarial).not.toHaveBeenCalled();
    expect(api.getAuthorLLMAudit).not.toHaveBeenCalled();
    expect(api.listAuthorReviewItems).not.toHaveBeenCalled();
    for (const other of ['Архивы', 'Книги', 'Модель', 'Сущности', 'Проверка человеком']) {
        expect(screen.queryByRole('region', { name: other })).toBeNull();
    }
});

/** The list item holding a file's download button. */
const errorRow = (scope: HTMLElement, file: string) => {
    const download = within(scope).getByRole('button', { name: `Скачать: ${file}` });
    const row = download.closest('li');
    if (row === null) {
        throw new Error(`no row for ${file}`);
    }
    return { download, row };
};

/** The row says everything without a wide screen: no part of it is hidden. */
const expectReadable = (row: HTMLElement, text: string) => {
    const holder = within(row).getByText(text, { exact: false });
    expect(holder.closest('.hidden, .truncate')).toBeNull();
};

it('an error of the newest archive shows even when its scan counted none', async () => {
    const now = new Date();
    const input = healthy(now);
    serve(input);
    const newest = input.scanned!.find((a) => a.name === 'fb2-824014-826219.zip')!;
    expect(newest.errors_count).toBe(0);
    api.listScanErrors.mockResolvedValue({
        errors: [
            {
                file_name: 'review-newest.fb2',
                archive_name: newest.name,
                error: 'author_metadata: author_metadata_unavailable',
                timestamp: now.toISOString(),
            },
        ],
    });
    api.getScanErrorFile.mockResolvedValue(new Blob(['x']));
    renderAt('/admin/book-scanning?stage=arch');

    const archives = await panel('Архивы');
    const { download, row } = await waitFor(() => errorRow(archives, 'review-newest.fb2'));
    expectReadable(row, 'author_metadata: author_metadata_unavailable');
    fireEvent.click(download);
    await waitFor(() =>
        expect(api.getScanErrorFile).toHaveBeenCalledWith(newest.name, 'review-newest.fb2'),
    );
});

it('an error row names its archive, its message and its time', async () => {
    const now = new Date();
    const at = new Date(now.getTime() - 3 * 60 * 60 * 1000);
    const moment = at.toLocaleString('ru', {
        day: '2-digit',
        month: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
    });
    serve(healthy(now));
    api.listScanErrors.mockResolvedValue({
        errors: [
            {
                file_name: '500000.fb2',
                archive_name: 'fb2-119691-132107.zip',
                error: 'author_metadata: invalid_fb2',
                timestamp: at.toISOString(),
            },
            {
                file_name: '500001.fb2',
                archive_name: 'fb2-200000-210000.zip',
                error: 'open 500001.fb2: permission denied',
                timestamp: at.toISOString(),
            },
        ],
    });
    renderAt('/admin/book-scanning?stage=books');

    const books = await panel('Книги');
    const parsed = await waitFor(() => errorRow(books, '500000.fb2').row);
    expectReadable(parsed, 'fb2-119691-132107.zip');
    expectReadable(parsed, 'author_metadata: invalid_fb2');
    expectReadable(parsed, moment);

    // «Другие ошибки» says nothing about the cause: the raw message does.
    const other = errorRow(books, '500001.fb2').row;
    expect(other.closest('details')?.querySelector('summary')).toHaveTextContent('Другие ошибки');
    expectReadable(other, 'fb2-200000-210000.zip');
    expectReadable(other, 'open 500001.fb2: permission denied');
    expectReadable(other, moment);
});
