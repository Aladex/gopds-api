import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import RescanPreviewDialog from '@/features/catalogue/RescanPreviewDialog';
import * as adminApi from '@/api/admin';
import { ApiError } from '@/api/errors';
import type { RescanPreview } from '@/features/catalogue/hooks/useRescan';

// A rescan overwrites metadata a librarian may have corrected by hand, so the
// dialog's whole job is to make the decision reversible before it is taken:
// show both versions, let the moderator drop the fields they do not want, and
// send exactly those. The tests are about the request that decision produces
// and about the states — loading, failed, decided — that would otherwise leave
// the moderator staring at an empty box.

vi.mock('@/api/admin', () => ({
    rescanBook: vi.fn(),
    approveRescan: vi.fn(),
    getRescanCoverPreview: vi.fn(),
}));

const translate = (key: string, options?: unknown) => {
    if (typeof options === 'string') return options;
    if (options && typeof options === 'object') {
        const values = Object.values(options as Record<string, unknown>);
        if (values.length > 0) return `${key} ${values.join(' ')}`;
    }
    return key;
};
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const rescanBook = vi.mocked(adminApi.rescanBook);
const approveRescan = vi.mocked(adminApi.approveRescan);
const getRescanCoverPreview = vi.mocked(adminApi.getRescanCoverPreview);

function makePreview(over: Partial<RescanPreview> = {}): RescanPreview {
    return {
        book_id: 12,
        pending_rescan_id: 34,
        old: {
            title: 'Дозор',
            lang: 'ru',
            annotation: 'Старая аннотация',
            docdate: '1998',
            authors: [{ id: 1, name: 'Лукьяненко Сергей' }],
            series: null,
            tags: [],
            has_cover: false,
        },
        new: {
            title: 'Ночной Дозор',
            lang: 'en',
            annotation: 'Новая аннотация',
            docdate: '1998',
            authors: [{ id: 1, name: 'Лукьяненко Сергей' }],
            series: { id: 3, title: 'Дозоры', index: '1' },
            tags: ['фэнтези'],
            has_cover: true,
        },
        diff: ['title', 'lang', 'cover'],
        ...over,
    };
}

function setup() {
    const onClose = vi.fn();
    const onRescanCompleted = vi.fn();
    const view = render(
        <RescanPreviewDialog
            open
            bookId={12}
            onClose={onClose}
            onRescanCompleted={onRescanCompleted}
        />,
    );
    return { ...view, onClose, onRescanCompleted };
}

const approvalBody = () => approveRescan.mock.calls[0][1] as Record<string, unknown>;

beforeEach(() => {
    rescanBook.mockReset();
    approveRescan.mockReset();
    getRescanCoverPreview.mockReset();
    rescanBook.mockResolvedValue({ result: makePreview() });
    approveRescan.mockResolvedValue({ result: { success: true, message: 'ok', book_id: 12, action: 'approve' } });
    getRescanCoverPreview.mockResolvedValue(new Blob(['jpeg'], { type: 'image/jpeg' }));
    // jsdom has no object URLs, and the cover preview lives on one.
    URL.createObjectURL = vi.fn(() => 'blob:cover');
    URL.revokeObjectURL = vi.fn();
});

describe('RescanPreviewDialog', () => {
    it('asks for the preview when it opens and shows both versions', async () => {
        setup();

        await waitFor(() => expect(rescanBook).toHaveBeenCalledWith(12));
        expect(await screen.findByText('Дозор')).toBeInTheDocument();
        expect(screen.getByText('Ночной Дозор')).toBeInTheDocument();
        // A missing series reads as a stated absence, not a blank.
        expect(screen.getByText('noSeries')).toBeInTheDocument();
        expect(screen.getByText('rescanNoTags')).toBeInTheDocument();
    });

    it('marks only the fields the rescan would actually change', async () => {
        setup();

        await screen.findByText('Ночной Дозор');
        expect(screen.getAllByText('rescanChanged')).toHaveLength(3);
    });

    it('waits with a spinner rather than an empty box', async () => {
        let release: (value: { result?: RescanPreview }) => void = () => {};
        rescanBook.mockReturnValue(new Promise((resolve) => { release = resolve; }));
        setup();

        expect(screen.getByRole('status')).toBeInTheDocument();

        release({ result: makePreview() });
        expect(await screen.findByText('Ночной Дозор')).toBeInTheDocument();
        expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });

    it('reports a preview the backend refused and offers no decision', async () => {
        rescanBook.mockResolvedValue({ error: 'Файл книги не найден' });
        setup();

        expect(await screen.findByText('Файл книги не найден')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'rescanApprove' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'rescanReject' })).toBeDisabled();
    });

    it('approves with every changed field selected by default', async () => {
        const user = userEvent.setup();
        const { onClose, onRescanCompleted } = setup();
        await screen.findByText('Ночной Дозор');

        await user.click(screen.getByRole('button', { name: 'rescanApprove' }));

        await waitFor(() => expect(approveRescan).toHaveBeenCalledTimes(1));
        expect(approveRescan.mock.calls[0][0]).toBe(12);
        expect(approvalBody()).toEqual({
            action: 'approve',
            update_title: true,
            update_annotation: true,
            update_lang: true,
            update_docdate: true,
            update_authors: true,
            update_series: true,
            update_cover: true,
            update_tags: true,
        });
        expect(onRescanCompleted).toHaveBeenCalledTimes(1);
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('leaves out the field the moderator unticked', async () => {
        const user = userEvent.setup();
        setup();
        await screen.findByText('Ночной Дозор');

        await user.click(screen.getByRole('checkbox', { name: 'title' }));
        await user.click(screen.getByRole('button', { name: 'rescanApprove' }));

        await waitFor(() => expect(approveRescan).toHaveBeenCalled());
        expect(approvalBody()).toMatchObject({ update_title: false, update_lang: true, update_cover: true });
    });

    it('offers a tick box only for the fields that differ', async () => {
        setup();
        await screen.findByText('Ночной Дозор');

        expect(screen.getAllByRole('checkbox')).toHaveLength(3);
        expect(screen.getByRole('checkbox', { name: 'rescanCover' })).toBeInTheDocument();
        expect(screen.queryByRole('checkbox', { name: 'annotation' })).not.toBeInTheDocument();
    });

    it('counts the fields that will be written', async () => {
        const user = userEvent.setup();
        setup();
        await screen.findByText('Ночной Дозор');

        expect(screen.getByText('rescanUpdateSummary 3 3')).toBeInTheDocument();

        await user.click(screen.getByRole('button', { name: 'deselectAll' }));
        expect(screen.getByText('rescanUpdateSummary 0 3')).toBeInTheDocument();

        await user.click(screen.getByRole('button', { name: 'selectAll' }));
        expect(screen.getByText('rescanUpdateSummary 3 3')).toBeInTheDocument();
    });

    it('rejects without sending any field flags', async () => {
        const user = userEvent.setup();
        const { onClose, onRescanCompleted } = setup();
        await screen.findByText('Ночной Дозор');

        await user.click(screen.getByRole('button', { name: 'rescanReject' }));

        await waitFor(() => expect(approveRescan).toHaveBeenCalledTimes(1));
        expect(approvalBody()).toEqual({ action: 'reject' });
        expect(onRescanCompleted).toHaveBeenCalledTimes(1);
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('keeps the dialog open when the decision itself fails', async () => {
        const user = userEvent.setup();
        vi.spyOn(console, 'error').mockImplementation(() => {});
        approveRescan.mockRejectedValue(
            new ApiError('gone', 410, { body: { detail: 'Пересканирование устарело' } }),
        );
        const { onClose, onRescanCompleted } = setup();
        await screen.findByText('Ночной Дозор');

        await user.click(screen.getByRole('button', { name: 'rescanApprove' }));

        expect(await screen.findByText('Пересканирование устарело')).toBeInTheDocument();
        expect(onRescanCompleted).not.toHaveBeenCalled();
        expect(onClose).not.toHaveBeenCalled();
    });

    it('fetches the candidate cover when the rescan found one', async () => {
        setup();
        await screen.findByText('Ночной Дозор');

        await waitFor(() => expect(getRescanCoverPreview).toHaveBeenCalledWith(12));
        const image = await screen.findByRole('img', { name: 'rescanCover' });
        expect(image).toHaveAttribute('src', 'blob:cover');
    });

    it('asks for no cover the rescan did not find', async () => {
        const preview = makePreview();
        rescanBook.mockResolvedValue({
            result: { ...preview, new: { ...preview.new, has_cover: false }, diff: ['title', 'lang'] },
        });
        setup();
        await screen.findByText('Ночной Дозор');

        expect(getRescanCoverPreview).not.toHaveBeenCalled();
        expect(screen.queryByRole('img')).not.toBeInTheDocument();
    });

    it('closes on cancel without deciding anything', async () => {
        const user = userEvent.setup();
        const { onClose, onRescanCompleted } = setup();
        await screen.findByText('Ночной Дозор');

        await user.click(screen.getByRole('button', { name: 'cancel' }));

        expect(onClose).toHaveBeenCalledTimes(1);
        expect(approveRescan).not.toHaveBeenCalled();
        expect(onRescanCompleted).not.toHaveBeenCalled();
    });
});
