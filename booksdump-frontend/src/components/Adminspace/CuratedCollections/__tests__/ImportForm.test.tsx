import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

jest.mock('../api', () => ({
    importCuratedCollection: jest.fn().mockResolvedValue({ collection_id: 1, status: 'importing' }),
}));

// react-i18next mock — return fallback as the rendered text.
jest.mock('react-i18next', () => ({
    useTranslation: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }),
}));

// Import after mocks so the component uses the mocked modules.
// eslint-disable-next-line @typescript-eslint/no-var-requires
const ImportForm = require('../ImportForm').default;
const api = require('../api');

describe('ImportForm', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        // clearAllMocks wipes mock implementations too — re-arm the default.
        api.importCuratedCollection.mockResolvedValue({ collection_id: 1, status: 'importing' });
    });

    it('renders both CSV and Paste tabs', () => {
        render(<ImportForm onCreated={() => {}} />);
        expect(screen.getByRole('tab', { name: /csv/i })).toBeInTheDocument();
        expect(screen.getByRole('tab', { name: /paste/i })).toBeInTheDocument();
    });

    it('shows preview rows after pasting valid CSV body', async () => {
        render(<ImportForm onCreated={() => {}} />);

        const csvBody = screen.getByLabelText(/CSV body/i);
        fireEvent.change(csvBody, {
            target: {
                value: 'title,author,year\n1984,Orwell,1949\nBrave New World,Huxley,1932',
            },
        });

        expect(await screen.findByText('1984')).toBeInTheDocument();
        expect(await screen.findByText('Brave New World')).toBeInTheDocument();
    });

    it('disables submit until both name and items are present', async () => {
        render(<ImportForm onCreated={() => {}} />);

        const submit = screen.getByRole('button', { name: /start import/i });
        expect(submit).toBeDisabled();

        fireEvent.change(screen.getByLabelText(/^Name/i), { target: { value: 'My Sel' } });
        expect(submit).toBeDisabled();

        fireEvent.change(screen.getByLabelText(/CSV body/i), {
            target: { value: 'title,author\n1984,Orwell' },
        });

        await waitFor(() => expect(submit).not.toBeDisabled());
    });

    it('calls importCuratedCollection on submit with parsed payload', async () => {
        const onCreated = jest.fn();
        render(<ImportForm onCreated={onCreated} />);

        fireEvent.change(screen.getByLabelText(/^Name/i), { target: { value: 'Sel' } });
        fireEvent.change(screen.getByLabelText(/CSV body/i), {
            target: { value: 'title,author,year\n1984,Orwell,1949' },
        });

        const submit = screen.getByRole('button', { name: /start import/i });
        await waitFor(() => expect(submit).not.toBeDisabled());
        fireEvent.click(submit);

        await waitFor(() =>
            expect(api.importCuratedCollection).toHaveBeenCalledWith('Sel', '', [
                { title: '1984', author: 'Orwell', year: 1949 },
            ]),
        );
        await waitFor(() => expect(onCreated).toHaveBeenCalledWith(1));
    });

    it('switching to Paste tab clears the preview', async () => {
        render(<ImportForm onCreated={() => {}} />);
        fireEvent.change(screen.getByLabelText(/CSV body/i), {
            target: { value: 'title,author\n1984,Orwell' },
        });
        expect(await screen.findByText('1984')).toBeInTheDocument();

        fireEvent.click(screen.getByRole('tab', { name: /paste/i }));
        await waitFor(() => expect(screen.queryByText('1984')).not.toBeInTheDocument());
    });
});
