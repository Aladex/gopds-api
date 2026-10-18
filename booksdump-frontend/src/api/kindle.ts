import { http } from '@/api/http';
import type { User } from '@/api/auth';

/**
 * The reader's Send-to-Kindle address. Both answer with the whole profile, so
 * the caller replaces its copy of the user rather than patching one field.
 *
 * Sending a book is not here: it rides the websocket, like a conversion — see
 * WebSocketContext's sendToKindle.
 */

export const setKindleEmail = (kindleEmail: string) =>
    http.put<User>('/books/kindle', { kindle_email: kindleEmail });

export const clearKindleEmail = () => http.delete<User>('/books/kindle');
