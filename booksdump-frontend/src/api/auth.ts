import { http, request } from './http';

/**
 * Authentication and account endpoints.
 *
 * Session refresh is not exposed here as something callers drive: the transport
 * already refreshes once on a 401 and replays the request. A component that
 * refreshed as well would spend two round trips proving the same thing.
 */

/**
 * User is the shape GET /books/self-user and GET /init return. It used to be
 * declared inside AuthContext; it lives here so every consumer shares one
 * definition instead of restating a compatible-looking one.
 */
export interface User {
    username: string;
    first_name: string;
    last_name: string;
    is_superuser: boolean;
    books_lang?: string;
    have_favs?: boolean;
    has_bot_token?: boolean;
    date_joined?: string;
}

/** InitResponse is the bootstrap payload the app reads on start. */
export interface InitResponse {
    csrf_token?: string;
    user?: User | null;
}

export interface LoginRequest {
    username: string;
    password: string;
}

export interface RegisterRequest {
    username: string;
    password: string;
    email: string;
    invite: string;
}

export interface ChangePasswordRequest {
    token: string;
    password: string;
}

export interface CsrfTokenResponse {
    csrf_token: string;
}

/** login exchanges credentials for the session cookies the backend sets. */
export const login = (credentials: LoginRequest) => http.post<User>('/login', credentials);

export const logout = () => http.get<void>('/logout');

export const register = (payload: RegisterRequest) => http.post<unknown>('/register', payload);

/** activate confirms a registration or password-reset token. */
export const activate = (token: string) => http.post<unknown>('/token', { token });

/** requestPasswordChange sends the reset email. */
export const requestPasswordChange = (email: string) =>
    http.post<unknown>('/change-request', { email });

export const changePassword = (payload: ChangePasswordRequest) =>
    http.post<unknown>('/change-password', payload);

export const getCsrfToken = () => http.get<CsrfTokenResponse>('/csrf-token');

export const getCurrentUser = () => http.get<User>('/books/self-user');

export const updateCurrentUser = (payload: Partial<User> & Record<string, unknown>) =>
    http.post<User>('/books/change-me', payload);

/** getInit returns the bootstrap payload the app reads on start. */
export const getInit = () => http.get<InitResponse>('/init');

/**
 * refreshSession is only for callers that need to know whether the session is
 * still alive without making a real request. Ordinary calls do not need it.
 */
export const refreshSession = () =>
    request<void>('/refresh-token', { method: 'POST', skipRefresh: true });
