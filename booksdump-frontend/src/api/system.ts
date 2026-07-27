import { http } from '@/api/http';

/** Application-level endpoints that belong to no particular resource. */

/** getStatus returns the running application version. */
export const getStatus = () => http.get<{ result: string }>('/status');

/**
 * How the service asks to be supported. The list comes from the operator's
 * configuration, so an installation that has not set any offers none — the
 * interface should show nothing rather than someone else's wallet.
 */
export interface DonateMethod {
    id: string;
    label: string;
    /** How to present `value`: something to copy, a card number, or a link. */
    kind: 'address' | 'card' | 'link';
    value: string;
    /** An optional way to pay alongside a value worth showing, such as a bank page. */
    link?: string;
    /** Whether a scannable code is worth drawing for this one. */
    qr: boolean;
}

export const getDonateMethods = () => http.get<DonateMethod[]>('/donate');
