import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogTitle,
    DialogTrigger,
} from '@/components/ui/dialog';

// These cover the primitives as contracts the feature slices will rely on:
// they render, they are operable by keyboard, and their variants are
// distinguishable. Appearance is deliberately not asserted — that is what the
// theme tokens are for.

describe('Button', () => {
    it('renders its label and reports clicks', async () => {
        const onClick = vi.fn();
        render(<Button onClick={onClick}>Download</Button>);

        await userEvent.click(screen.getByRole('button', { name: 'Download' }));

        expect(onClick).toHaveBeenCalledOnce();
    });

    it('is operable with the keyboard', async () => {
        const onClick = vi.fn();
        render(<Button onClick={onClick}>Search</Button>);

        await userEvent.tab();
        expect(screen.getByRole('button', { name: 'Search' })).toHaveFocus();

        await userEvent.keyboard('{Enter}');
        expect(onClick).toHaveBeenCalledOnce();
    });

    it('does not fire when disabled', async () => {
        const onClick = vi.fn();
        render(
            <Button onClick={onClick} disabled>
                Disabled
            </Button>,
        );

        await userEvent.click(screen.getByRole('button', { name: 'Disabled' }));

        expect(onClick).not.toHaveBeenCalled();
    });

    it('gives each variant a distinct class list', () => {
        const { rerender } = render(<Button variant="default">x</Button>);
        const seen = new Set<string>();
        seen.add(screen.getByRole('button').className);

        for (const variant of ['destructive', 'outline', 'ghost', 'secondary'] as const) {
            rerender(<Button variant={variant}>x</Button>);
            seen.add(screen.getByRole('button').className);
        }

        expect(seen.size).toBe(5);
    });
});

describe('Input', () => {
    it('is reachable through its label and accepts typing', async () => {
        render(
            <>
                <label htmlFor="q">Search</label>
                <Input id="q" />
            </>,
        );

        const input = screen.getByLabelText('Search');
        await userEvent.type(input, 'dune');

        expect(input).toHaveValue('dune');
    });

    it('forwards a ref to the underlying element', () => {
        const ref = React.createRef<HTMLInputElement>();
        render(<Input ref={ref} />);

        expect(ref.current).toBeInstanceOf(HTMLInputElement);
    });
});

describe('Dialog', () => {
    it('opens from its trigger and closes on Escape', async () => {
        render(
            <Dialog>
                <DialogTrigger>Open</DialogTrigger>
                <DialogContent>
                    <DialogTitle>Confirm</DialogTitle>
                    <DialogDescription>Are you sure?</DialogDescription>
                </DialogContent>
            </Dialog>,
        );

        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

        await userEvent.click(screen.getByText('Open'));
        expect(await screen.findByRole('dialog')).toBeInTheDocument();
        expect(screen.getByText('Confirm')).toBeInTheDocument();

        await userEvent.keyboard('{Escape}');
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    });

    it('labels the dialog for assistive technology', async () => {
        render(
            <Dialog defaultOpen>
                <DialogContent>
                    <DialogTitle>Delete book</DialogTitle>
                    <DialogDescription>This cannot be undone.</DialogDescription>
                </DialogContent>
            </Dialog>,
        );

        expect(await screen.findByRole('dialog', { name: 'Delete book' })).toBeInTheDocument();
    });
});

describe('Alert', () => {
    it('exposes itself as an alert with a title and description', () => {
        render(
            <Alert>
                <AlertTitle>Conversion failed</AlertTitle>
                <AlertDescription>Try another format.</AlertDescription>
            </Alert>,
        );

        const alert = screen.getByRole('alert');
        expect(alert).toHaveTextContent('Conversion failed');
        expect(alert).toHaveTextContent('Try another format.');
    });
});

describe('Card', () => {
    it('renders its heading and body', () => {
        render(
            <Card>
                <CardHeader>
                    <CardTitle>Dune</CardTitle>
                </CardHeader>
                <CardContent>Frank Herbert</CardContent>
            </Card>,
        );

        expect(screen.getByText('Dune')).toBeInTheDocument();
        expect(screen.getByText('Frank Herbert')).toBeInTheDocument();
    });
});

describe('Badge and Skeleton', () => {
    it('renders badge content', () => {
        render(<Badge>EPUB</Badge>);
        expect(screen.getByText('EPUB')).toBeInTheDocument();
    });

    it('marks a skeleton as a loading placeholder', () => {
        render(<Skeleton data-testid="ph" />);
        expect(screen.getByTestId('ph')).toBeInTheDocument();
    });
});
