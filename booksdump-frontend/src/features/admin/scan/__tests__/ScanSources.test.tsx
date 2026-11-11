import React from 'react';
import { act, render, renderHook, waitFor } from '@testing-library/react';

import * as adminApi from '@/api/admin';
import type { AuthorEntityStatus } from '@/api/admin';
import { ScanSourcesProvider, useScanSource } from '@/features/admin/scan/ScanSources';
import { autopilot, entityStatus, llmStatus, metadataRun } from './pipelineFixtures';

vi.mock('@/api/admin', async (importOriginal) => {
    const actual = await importOriginal<typeof import('@/api/admin')>();
    return {
        ...actual,
        getLatestAuthorMetadataRun: vi.fn(),
        getAuthorMetadataRunReport: vi.fn(),
        getAuthorMetadataRunArchives: vi.fn(),
        getAuthorLLMStatus: vi.fn(),
        getAuthorEntityStatus: vi.fn(),
        getAuthorMerges: vi.fn(),
    };
});

const api = vi.mocked(adminApi);

const wrapper = ({ children }: { children: React.ReactNode }) => (
    <ScanSourcesProvider>{children}</ScanSourcesProvider>
);

/** Two consumers of one key in one tree, as the chain and a panel will be. */
const twice =
    <K extends Parameters<typeof useScanSource>[0]>(key: K) =>
    () => ({
        a: useScanSource(key),
        b: useScanSource(key),
    });

beforeEach(() => {
    vi.clearAllMocks();
});

afterEach(() => {
    vi.useRealTimers();
});

describe('useScanSource', () => {
    it('two consumers share one request', async () => {
        const NOW = new Date();
        api.getAuthorEntityStatus.mockResolvedValue({ status: entityStatus(NOW) });
        const { result } = renderHook(twice('entity'), { wrapper });
        await waitFor(() => expect(result.current.a.data).not.toBeNull());
        expect(result.current.b.data).toBe(result.current.a.data);
        expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1);
    });

    it('without a provider each consumer loads itself', async () => {
        const NOW = new Date();
        api.getAuthorEntityStatus.mockResolvedValue({ status: entityStatus(NOW) });
        const { result } = renderHook(twice('entity'));
        await waitFor(() => expect(result.current.b.data).not.toBeNull());
        expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(2);
    });

    it('entity polls at 15 s while the autopilot is on and not at all when off', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const NOW = new Date();
        api.getAuthorEntityStatus.mockResolvedValue({ status: entityStatus(NOW) });
        const on = renderHook(twice('entity'), { wrapper });
        await waitFor(() => expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1));
        await act(async () => {
            await vi.advanceTimersByTimeAsync(14_000);
        });
        expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1);
        await act(async () => {
            await vi.advanceTimersByTimeAsync(1_500);
        });
        expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(2);
        on.unmount();

        api.getAuthorEntityStatus.mockClear();
        api.getAuthorEntityStatus.mockResolvedValue({
            status: entityStatus(NOW, { autopilot: autopilot(NOW, { enabled: false }) }),
        });
        renderHook(() => useScanSource('entity'), { wrapper });
        await waitFor(() => expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1));
        await act(async () => {
            await vi.advanceTimersByTimeAsync(120_000);
        });
        expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1);
    });

    it('entity polls at 5 s while a pass runs', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const NOW = new Date();
        const base = entityStatus(NOW, { autopilot: autopilot(NOW, { enabled: false }) });
        const running: AuthorEntityStatus = { ...base, run: { ...base.run!, status: 'running' } };
        api.getAuthorEntityStatus.mockResolvedValue({ status: running });
        renderHook(() => useScanSource('entity'), { wrapper });
        await waitFor(() => expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1));
        await act(async () => {
            await vi.advanceTimersByTimeAsync(5_500);
        });
        expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(2);
    });

    it('llm polls only while a run is active', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const idle = llmStatus();
        const active = llmStatus({ run: { ...idle.run!, status: 'running' } });
        api.getAuthorLLMStatus
            .mockResolvedValueOnce({ status: active })
            .mockResolvedValue({ status: idle });
        renderHook(twice('llmNames'), { wrapper });
        await waitFor(() => expect(api.getAuthorLLMStatus).toHaveBeenCalledTimes(1));
        await act(async () => {
            await vi.advanceTimersByTimeAsync(5_500);
        });
        expect(api.getAuthorLLMStatus).toHaveBeenCalledTimes(2);
        await act(async () => {
            await vi.advanceTimersByTimeAsync(60_000);
        });
        expect(api.getAuthorLLMStatus).toHaveBeenCalledTimes(2);
    });

    it('names and merges read their own endpoints', async () => {
        api.getAuthorLLMStatus.mockResolvedValue({ status: llmStatus() });
        renderHook(() => ({ n: useScanSource('llmNames'), m: useScanSource('llmMerge') }), {
            wrapper,
        });
        await waitFor(() => expect(api.getAuthorLLMStatus).toHaveBeenCalledTimes(2));
        expect(api.getAuthorLLMStatus.mock.calls.map((c) => c[0])).toEqual([
            undefined,
            adminApi.llmMergeBase,
        ]);
    });

    it('the metadata run follows an active pass and reads its report once completed', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const NOW = new Date();
        const done = metadataRun(NOW);
        api.getLatestAuthorMetadataRun
            .mockResolvedValueOnce({ run: metadataRun(NOW, { status: 'running' }) })
            .mockResolvedValue({ run: done });
        api.getAuthorMetadataRunReport.mockResolvedValue({
            report: {
                ...done,
                ready: true,
                not_ready_reasons: [],
                duration_s: 0,
                db_growth_bytes: 0,
                by_class: {},
                by_script: {},
            },
        });
        const { result } = renderHook(twice('metadataRun'), { wrapper });
        await waitFor(() => expect(result.current.a.data?.run?.status).toBe('running'));
        expect(api.getAuthorMetadataRunReport).not.toHaveBeenCalled();
        await act(async () => {
            await vi.advanceTimersByTimeAsync(5_500);
        });
        await waitFor(() => expect(result.current.a.data?.report?.id).toBe(done.id));
        expect(api.getLatestAuthorMetadataRun).toHaveBeenCalledTimes(2);
        expect(api.getAuthorMetadataRunArchives).not.toHaveBeenCalled();
        await act(async () => {
            await vi.advanceTimersByTimeAsync(60_000);
        });
        expect(api.getLatestAuthorMetadataRun).toHaveBeenCalledTimes(2);
    });

    it('a failed read keeps the last data and says it failed', async () => {
        const NOW = new Date();
        api.getAuthorEntityStatus
            .mockResolvedValueOnce({ status: entityStatus(NOW) })
            .mockRejectedValueOnce(new Error('down'));
        const { result } = renderHook(() => useScanSource('entity'), { wrapper });
        await waitFor(() => expect(result.current.data).not.toBeNull());
        await act(async () => {
            await result.current.reload();
        });
        expect(result.current.failed).toBe(true);
        expect(result.current.data?.run?.id).toBe(100);
    });

    it('a late answer does not overwrite a newer one', async () => {
        const NOW = new Date();
        let releaseFirst: (v: { status: AuthorEntityStatus }) => void = () => {};
        const base = entityStatus(NOW);
        api.getAuthorEntityStatus
            .mockImplementationOnce(() => new Promise((resolve) => (releaseFirst = resolve)))
            .mockResolvedValueOnce({ status: { ...base, run: { ...base.run!, id: 200 } } });
        const { result } = renderHook(() => useScanSource('entity'), { wrapper });
        await act(async () => {
            await result.current.reload();
        });
        expect(result.current.data?.run?.id).toBe(200);
        await act(async () => {
            releaseFirst({ status: { ...base, run: { ...base.run!, id: 1 } } });
        });
        expect(result.current.data?.run?.id).toBe(200);
    });

    it('unmounting the last consumer stops the poll, and mounting again reads afresh', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const NOW = new Date();
        api.getAuthorEntityStatus.mockResolvedValue({ status: entityStatus(NOW) });
        const Consumer = () => {
            useScanSource('entity');
            return null;
        };
        const tree = (show: boolean) => (
            <ScanSourcesProvider>{show && <Consumer />}</ScanSourcesProvider>
        );
        const { rerender, unmount } = render(tree(true));
        await waitFor(() => expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1));
        rerender(tree(false));
        await act(async () => {
            await vi.advanceTimersByTimeAsync(60_000);
        });
        expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1);
        rerender(tree(true));
        await waitFor(() => expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(2));
        unmount();
    });
});
