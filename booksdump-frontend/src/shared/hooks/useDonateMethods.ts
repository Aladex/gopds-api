import { useEffect, useState } from 'react';

import * as systemApi from '@/api/system';

/**
 * Whether there is anything to offer at all — and, since the answer is the list
 * itself, the list. The header's pill and the phone's foot both go by it, so it
 * is asked once, above them.
 *
 * Nothing is asked until `enabled`: the route that holds this also runs for a
 * visitor on the way to the login page.
 */
export function useDonateMethods(enabled: boolean): systemApi.DonateMethod[] {
    const [methods, setMethods] = useState<systemApi.DonateMethod[]>([]);

    useEffect(() => {
        if (!enabled) {
            return;
        }
        let cancelled = false;
        systemApi
            .getDonateMethods()
            .then((list) => {
                if (!cancelled) setMethods(list ?? []);
            })
            .catch(() => {
                // Nothing to offer if we cannot find out.
            });
        return () => {
            cancelled = true;
        };
    }, [enabled]);

    return methods;
}
