import { http } from './http';

/** Application-level endpoints that belong to no particular resource. */

/** getStatus returns the running application version. */
export const getStatus = () => http.get<{ result: string }>('/status');
