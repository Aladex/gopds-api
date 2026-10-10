import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';

import BookScanning from '@/features/admin/BookScanning';
import * as adminApi from '@/api/admin';
import { BookConversionProvider } from '@/context/BookConversionContext';
import { WebSocketProvider } from '@/context/WebSocketContext';

// The scanning section carries the author metadata card and an «Авторы» tab
// with the review queue; the tab lives in the address (?tab=authors).

vi.mock('react-i18next', () => ({
    useTranslation: () => ({
        t: (key: string, opts?: unknown) =>
            typeof opts === 'string'
                ? opts
                : opts && typeof opts === 'object' && 'defaultValue' in opts
                  ? String((opts as { defaultValue: unknown }).defaultValue)
                  : key,
    }),
}));

vi.mock('@/api/admin', async (importOriginal) => {
    const actual = await importOriginal<typeof import('@/api/admin')>();
    return {
        ...actual,
        getScanStatus: vi.fn(),
        listUnscannedArchives: vi.fn(),
        listScannedArchives: vi.fn(),
        listScanErrors: vi.fn(),
        getFixScanStatus: vi.fn(),
        getLatestAuthorMetadataRun: vi.fn(),
        getAuthorMetadataRunReport: vi.fn(),
        listAuthorReviewItems: vi.fn(),
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

beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal('WebSocket', SilentSocket);
    api.getScanStatus.mockResolvedValue({});
    api.listUnscannedArchives.mockResolvedValue({ archives: [], total_count: 0 });
    api.listScannedArchives.mockResolvedValue({ scanned_archives: [], total_count: 0 });
    api.listScanErrors.mockResolvedValue({ errors: [] });
    api.getFixScanStatus.mockResolvedValue({});
    api.getLatestAuthorMetadataRun.mockResolvedValue({ run: null });
    api.listAuthorReviewItems.mockResolvedValue({ items: [], next_cursor: null });
});

afterEach(() => {
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

it('shows the author metadata card in the scanning section', async () => {
    renderAt('/admin/book-scanning');
    expect(await screen.findByText('Author metadata')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Walk the catalogue' })).toBeInTheDocument();
});

it('opens the authors tab from the address and loads the review queue only there', async () => {
    renderAt('/admin/book-scanning');
    await screen.findByText('Author metadata');
    expect(api.listAuthorReviewItems).not.toHaveBeenCalled();

    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Authors' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Authors' }));
    await waitFor(() => expect(api.listAuthorReviewItems).toHaveBeenCalled());
});

it('lands in the authors tab when the address names it', async () => {
    renderAt('/admin/book-scanning?tab=authors');
    await waitFor(() => expect(api.listAuthorReviewItems).toHaveBeenCalled());
    expect(screen.getByRole('tab', { name: 'Authors' })).toHaveAttribute('aria-selected', 'true');
});

// Review B8: a fix scan's per-book author failures land in the shared errors
// list on the server; the list reloads when the fix scan completes.
it('reloads the scan errors when a fix scan completes', async () => {
    const sockets: CapturingSocket[] = [];
    class CapturingSocket extends SilentSocket {
        constructor() {
            super();
            sockets.push(this);
        }
    }
    vi.stubGlobal('WebSocket', CapturingSocket);
    renderAt('/admin/book-scanning');
    await waitFor(() => expect(api.listScanErrors).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(sockets.length).toBeGreaterThan(0));

    const socket = sockets[sockets.length - 1] as unknown as {
        onmessage: ((event: { data: string }) => void) | null;
    };
    socket.onmessage?.({
        data: JSON.stringify({
            type: 'fix_scan_completed',
            topic: 'fix_scan',
            data: { total_books: 3, updated_books: 3, error_count: 3, elapsed_seconds: 1 },
        }),
    });

    await waitFor(() => expect(api.listScanErrors).toHaveBeenCalledTimes(2));
});

// Round 2 (B4): the hub replays nothing after a disconnect, so a completion
// that fired while the socket was down never arrives. Reconnecting re-reads
// the snapshot instead.
it('refreshes the scanned archives when the socket reconnects', async () => {
    const sockets: CapturingSocket[] = [];
    class CapturingSocket extends SilentSocket {
        constructor() {
            super();
            sockets.push(this);
        }
    }
    vi.stubGlobal('WebSocket', CapturingSocket);
    renderAt('/admin/book-scanning');
    await waitFor(() => expect(api.listScannedArchives).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(sockets.length).toBeGreaterThan(0));

    const socket = () =>
        sockets[sockets.length - 1] as unknown as {
            onopen: (() => void) | null;
            onclose: (() => void) | null;
        };

    socket().onopen?.();
    // The connection drops; the final archive completes unseen.
    socket().onclose?.();
    await waitFor(() => expect(sockets.length).toBeGreaterThan(1), { timeout: 3000 });
    // Back online: the snapshot is re-read without any completion event.
    socket().onopen?.();

    await waitFor(() =>
        expect(api.listScannedArchives.mock.calls.length).toBeGreaterThanOrEqual(2),
    );
});

// Round 2 (B4): a reset from another tab announces itself; the list updates
// on a healthy socket too.
it('refreshes the scanned archives when an archive is reset elsewhere', async () => {
    const sockets: CapturingSocket[] = [];
    class CapturingSocket extends SilentSocket {
        constructor() {
            super();
            sockets.push(this);
        }
    }
    vi.stubGlobal('WebSocket', CapturingSocket);
    renderAt('/admin/book-scanning');
    await waitFor(() => expect(api.listScannedArchives).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(sockets.length).toBeGreaterThan(0));

    const socket = sockets[sockets.length - 1] as unknown as {
        onmessage: ((event: { data: string }) => void) | null;
    };
    socket.onmessage?.({
        data: JSON.stringify({
            type: 'archive_reset',
            topic: 'scan',
            data: { archive_name: 'gone.zip' },
        }),
    });

    await waitFor(() => expect(api.listScannedArchives).toHaveBeenCalledTimes(2));
});
