import { getAuthorLLMAudit } from '@/api/admin';

// The merge eval's audit sample (E4h round 3) is read from its own endpoint.
describe('getAuthorLLMAudit', () => {
    it('reads the audit sample of the run', async () => {
        const review = { run_id: 7, items: [] };
        const fetchSpy = vi.fn().mockResolvedValue(
            new Response(JSON.stringify({ review }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
            }),
        );
        globalThis.fetch = fetchSpy as unknown as typeof fetch;
        const response = await getAuthorLLMAudit(7);
        expect(response.review).toEqual(review);
        expect(fetchSpy.mock.calls[0][0] as string).toMatch(/\/llm\/merge\/eval-report\/7\/audit$/);
    });
});
