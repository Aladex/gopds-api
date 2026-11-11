import { listAuthorReviewItems } from '@/api/admin';

const answer = () =>
    vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ items: [], next_cursor: null }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
        }),
    );

// The review queue's order travels as a query parameter; leaving it out keeps
// the server's default.
describe('listAuthorReviewItems', () => {
    it('sends the credits order with the cursor', async () => {
        const fetchSpy = answer();
        globalThis.fetch = fetchSpy as unknown as typeof fetch;
        await listAuthorReviewItems({ status: 'open', order: 'credits', cursor: 'abc', limit: 50 });
        const url = new URL(fetchSpy.mock.calls[0][0] as string, 'http://localhost');
        expect(url.pathname).toMatch(/\/admin\/author-metadata\/review$/);
        expect(url.searchParams.get('order')).toBe('credits');
        expect(url.searchParams.get('cursor')).toBe('abc');
        expect(url.searchParams.get('status')).toBe('open');
    });

    it('omits the order when none is asked for', async () => {
        const fetchSpy = answer();
        globalThis.fetch = fetchSpy as unknown as typeof fetch;
        await listAuthorReviewItems({ status: 'closed' });
        const url = new URL(fetchSpy.mock.calls[0][0] as string, 'http://localhost');
        expect(url.searchParams.has('order')).toBe(false);
    });
});
