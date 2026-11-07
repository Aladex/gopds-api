import { renderHook, waitFor } from '@testing-library/react';

import * as systemApi from '@/api/system';
import { useDonateMethods } from '@/shared/hooks/useDonateMethods';

vi.mock('@/api/system', () => ({ getDonateMethods: vi.fn() }));
const getDonateMethods = vi.mocked(systemApi.getDonateMethods);

const method = { id: 'a', label: 'A', kind: 'link', value: 'x', qr: false } as const;

describe('useDonateMethods', () => {
    beforeEach(() => vi.clearAllMocks());

    it('asks nothing until there is a reader to ask for', async () => {
        getDonateMethods.mockResolvedValue([method]);
        const { result, rerender } = renderHook(({ on }) => useDonateMethods(on), {
            initialProps: { on: false },
        });

        expect(getDonateMethods).not.toHaveBeenCalled();
        expect(result.current).toEqual([]);

        rerender({ on: true });
        await waitFor(() => expect(result.current).toEqual([method]));
        expect(getDonateMethods).toHaveBeenCalledTimes(1);
    });

    it('takes an empty answer as nothing to offer', async () => {
        getDonateMethods.mockResolvedValue(null as never);
        const { result } = renderHook(() => useDonateMethods(true));

        await waitFor(() => expect(getDonateMethods).toHaveBeenCalled());
        expect(result.current).toEqual([]);
    });

    it('takes a failure as nothing to offer', async () => {
        getDonateMethods.mockRejectedValue(new Error('down'));
        const { result } = renderHook(() => useDonateMethods(true));

        await waitFor(() => expect(getDonateMethods).toHaveBeenCalled());
        expect(result.current).toEqual([]);
    });
});
