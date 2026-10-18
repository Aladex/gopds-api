import React, {
    createContext,
    useCallback,
    useContext,
    useEffect,
    useMemo,
    useRef,
    useState,
    ReactNode,
} from 'react';
import { WS_URL, API_URL } from '@/api/config';
import { getCurrentUser } from '@/api/auth';
import { isApiError } from '@/api/errors';
import { useBookConversion } from '@/context/BookConversionContext';

// One frame from the server: a topic event or a per-connection reply.
export interface WSMessage {
    type: string;
    topic?: string;
    data?: unknown;
    [key: string]: unknown;
}

export type WSMessageHandler = (message: WSMessage) => void;

/**
 * KindleReply is how a send to the reader's Kindle went. "sent" means the mail
 * relay took the message, which is as far as anyone can see: Amazon says
 * nothing back. An error carries the server's code, or one of two of this
 * side's own — `unavailable` when there was no connection to ask over, and
 * `connection_lost` when it went away before the answer came.
 */
export interface KindleReply {
    status: 'sent' | 'error';
    code?: string;
}

interface WebSocketContextType {
    isConnected: boolean;
    // subscribe registers a topic handler and returns the unsubscribe
    // function; components call it from an effect so unmount unsubscribes.
    subscribe: (topic: string, handler: WSMessageHandler) => () => void;
    // sendToKindle asks the server to mail the book to the reader's Kindle
    // and resolves with its answer. It never rejects.
    sendToKindle: (bookID: number) => Promise<KindleReply>;
}

const WebSocketContext = createContext<WebSocketContextType | undefined>(undefined);

export const useWebSocket = (): WebSocketContextType => {
    const context = useContext(WebSocketContext);
    if (!context) {
        throw new Error('useWebSocket must be used within a WebSocketProvider');
    }
    return context;
};

const WS_ENDPOINT = '/api/ws';
// Reconnect schedule: 1 s doubling to a 30 s cap with up to 25% jitter, so a
// server restart does not meet every tab at the same instant. There is never
// a zero-delay retry.
const MIN_RECONNECT_DELAY_MS = 1_000;
const MAX_RECONNECT_DELAY_MS = 30_000;

// isConversionReplyFrame validates a conversion reply structurally. Beside
// the typed envelope the legacy shape {bookID, format, status, error?}
// without `type` is accepted: the compatibility window of one release, while
// a pre-topics backend may still answer conversion requests.
const isConversionReplyFrame = (message: WSMessage): boolean => {
    if (typeof message.type === 'string' && message.type !== 'conversion') {
        return false;
    }
    if (typeof message.bookID !== 'number') {
        return false;
    }
    if (typeof message.format !== 'string' || typeof message.status !== 'string') {
        return false;
    }
    return message.status === 'ready' || message.status === 'error';
};

/** How a renewal of an expired socket session ended. */
type RenewalOutcome = 'renewed' | 'revoked' | 'unavailable';

// isKindleReplyFrame recognizes the answer to a sendToKindle request.
const isKindleReplyFrame = (message: WSMessage): boolean =>
    message.type === 'kindle' &&
    typeof message.bookID === 'number' &&
    (message.status === 'sent' || message.status === 'error');

interface WebSocketProviderProps {
    isAuthenticated: boolean;
    // Fires on every successful reconnect after a dropped live connection, so
    // views can refetch a snapshot that events during the outage may have
    // moved past.
    onReconnect?: () => void;
    children: ReactNode;
}

// WebSocketProvider owns the single per-tab connection: every page's events
// and the conversion request/reply traffic share it. The connection
// reconnects with backoff and re-sends all active subscriptions on open.
export const WebSocketProvider: React.FC<WebSocketProviderProps> = ({
    isAuthenticated,
    onReconnect,
    children,
}) => {
    const [isConnected, setIsConnected] = useState(false);
    const wsRef = useRef<WebSocket | null>(null);
    const openRef = useRef(false);
    const handlersRef = useRef(new Map<string, Set<WSMessageHandler>>());
    const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    // Kindle requests waiting for their reply, by book. More than one may wait
    // on the same book; one reply answers them all.
    const kindleWaitersRef = useRef(new Map<number, Array<(reply: KindleReply) => void>>());
    const { state, dispatch } = useBookConversion();

    // A socket keeps the access token it was opened with, and that token lives
    // fifteen minutes; the server then answers a Kindle request with
    // session_expired and closes the socket. Such a request is not given up:
    // the session is renewed through the ordinary HTTP refresh, and the book
    // asked for once more on the next socket. kindleRetryRef holds those books
    // — false while the renewal runs, true once it succeeded — and
    // kindleRetriedRef the ones already asked a second time, which are not
    // retried again. expiredSocketRef is the socket that said so, never used
    // for the retry even if its close has not arrived yet.
    const kindleRetryRef = useRef(new Map<number, boolean>());
    const kindleRetriedRef = useRef(new Set<number>());
    const expiredSocketRef = useRef<WebSocket | null>(null);

    // settleKindle answers the waiters on one book, or on every book with
    // null — leaving out, unless everything is going away, the ones waiting
    // on a renewal, whose socket closing is part of the plan.
    const settleKindle = useCallback(
        (bookID: number | null, reply: KindleReply, everything = false) => {
            const waiters = kindleWaitersRef.current;
            const books =
                bookID === null
                    ? Array.from(waiters.keys()).filter(
                          (id) => everything || !kindleRetryRef.current.has(id),
                      )
                    : [bookID];
            for (const id of books) {
                kindleRetryRef.current.delete(id);
                kindleRetriedRef.current.delete(id);
                waiters.get(id)?.forEach((resolve) => resolve(reply));
                waiters.delete(id);
            }
        },
        [],
    );

    // resendKindle asks again for every book whose session was renewed, if a
    // socket other than the expired one is open; otherwise the next open does.
    const resendKindle = useCallback(() => {
        const ws = wsRef.current;
        if (!ws || !openRef.current || ws === expiredSocketRef.current) {
            return;
        }
        for (const [bookID, renewed] of kindleRetryRef.current) {
            if (renewed) {
                kindleRetryRef.current.delete(bookID);
                kindleRetriedRef.current.add(bookID);
                ws.send(JSON.stringify({ type: 'kindle', bookID }));
            }
        }
    }, []);

    // The renewal under way, shared by every book that met the expired
    // socket: several sends queued on one socket all come back expired
    // together, and a renewal each meant one refresh each — competing
    // rotations of the same refresh token, where the loser is told the token
    // was revoked and logs out a session the winner had just renewed.
    const renewalRef = useRef<Promise<RenewalOutcome> | null>(null);

    // renewAndRetry renews the session for a book whose send met an expired
    // socket, joining the renewal already running if there is one, and asks
    // for the book again once it succeeds. getCurrentUser goes through the
    // transport's refresh-and-replay. Only a 401 out of it is a verdict — the
    // transport has then already run the app's confirmed-logout path — and
    // the book is answered session_expired. Anything else (a 503, no network)
    // proves nothing about the session: it is kept, and the book answered
    // unavailable, to be sent again by hand.
    const renewAndRetry = useCallback(
        (bookID: number, ws: WebSocket) => {
            expiredSocketRef.current = ws;
            kindleRetryRef.current.set(bookID, false);
            let renewal = renewalRef.current;
            if (!renewal) {
                const started: Promise<RenewalOutcome> = getCurrentUser().then(
                    () => 'renewed',
                    (error: unknown) =>
                        isApiError(error) && error.status === 401 ? 'revoked' : 'unavailable',
                );
                renewal = started;
                renewalRef.current = started;
                void started.then(() => {
                    if (renewalRef.current === started) {
                        renewalRef.current = null;
                    }
                });
            }
            void renewal.then((outcome) => {
                if (!kindleRetryRef.current.has(bookID)) {
                    return;
                }
                if (outcome === 'renewed') {
                    kindleRetryRef.current.set(bookID, true);
                    resendKindle();
                    return;
                }
                settleKindle(bookID, {
                    status: 'error',
                    code: outcome === 'revoked' ? 'session_expired' : 'unavailable',
                });
            });
        },
        [resendKindle, settleKindle],
    );

    const sendFrame = useCallback((frame: Record<string, unknown>) => {
        if (wsRef.current && openRef.current) {
            wsRef.current.send(JSON.stringify(frame));
        }
    }, []);

    const handleConversionReply = useCallback(
        (message: WSMessage) => {
            const bookID = typeof message.bookID === 'number' ? message.bookID : NaN;
            if (Number.isNaN(bookID) || typeof message.status !== 'string') {
                return;
            }
            const format = typeof message.format === 'string' ? message.format : 'mobi';
            dispatch({ type: 'REMOVE_CONVERTING_BOOK', payload: { bookID, format } });
            if (message.status === 'ready') {
                const downloadUrl =
                    format === 'epub'
                        ? `${API_URL}/api/files/books/conversion/epub/${bookID}`
                        : `${API_URL}/api/files/books/conversion/${bookID}`;
                const iframe = document.createElement('iframe');
                iframe.style.display = 'none';
                iframe.src = downloadUrl;
                document.body.appendChild(iframe);

                iframe.onload = () => {
                    document.body.removeChild(iframe);
                };
            } else if (message.status === 'error') {
                dispatch({
                    type: 'ADD_CONVERSION_ERROR',
                    payload: {
                        bookID,
                        format,
                        message: String(message.error || 'Conversion failed'),
                    },
                });
            }
        },
        [dispatch],
    );

    // The connection lives exactly as long as this effect: an auth change
    // tears everything down, so a stale socket or timer can never reconnect
    // a logged-out session.
    useEffect(() => {
        if (!isAuthenticated) {
            return;
        }
        let tornDown = false;
        let everOpened = false;
        // One owner, one timer: connect never runs while a socket is
        // connecting or open, and every schedule replaces the pending one.
        let connecting = false;
        let attempts = 0;

        const clearReconnectTimer = () => {
            if (reconnectTimerRef.current) {
                clearTimeout(reconnectTimerRef.current);
                reconnectTimerRef.current = null;
            }
        };

        const scheduleReconnect = () => {
            clearReconnectTimer();
            attempts += 1;
            const backoff = Math.min(
                MIN_RECONNECT_DELAY_MS * 2 ** (attempts - 1),
                MAX_RECONNECT_DELAY_MS,
            );
            reconnectTimerRef.current = setTimeout(connect, backoff * (1 + Math.random() * 0.25));
        };

        const connect = () => {
            if (tornDown || connecting) {
                return;
            }
            connecting = true;
            const ws = new WebSocket(`${WS_URL}${WS_ENDPOINT}`);
            wsRef.current = ws;
            openRef.current = false;
            let opened = false;

            ws.onopen = () => {
                connecting = false;
                opened = true;
                openRef.current = true;
                attempts = 0;
                setIsConnected(true);
                // A fresh connection knows nothing: re-send every subscription.
                for (const topic of handlersRef.current.keys()) {
                    ws.send(JSON.stringify({ type: 'subscribe', topic }));
                }
                // And ask again for the books whose session was renewed while
                // the socket was down.
                resendKindle();
                if (everOpened) {
                    // This open recovers from a dropped live connection, and
                    // events since the drop are lost: let views catch up.
                    onReconnect?.();
                }
                everOpened = true;
            };

            ws.onmessage = (event) => {
                let parsed: WSMessage;
                try {
                    parsed = JSON.parse(
                        typeof event.data === 'string' ? event.data : String(event.data),
                    );
                } catch {
                    // Everything on this socket is JSON now; a non-JSON frame
                    // is not ours.
                    return;
                }
                if (!parsed || typeof parsed !== 'object') {
                    return;
                }
                if (isConversionReplyFrame(parsed)) {
                    handleConversionReply(parsed);
                    return;
                }
                if (isKindleReplyFrame(parsed)) {
                    const bookID = parsed.bookID as number;
                    if (
                        parsed.code === 'session_expired' &&
                        kindleWaitersRef.current.has(bookID) &&
                        !kindleRetriedRef.current.has(bookID)
                    ) {
                        renewAndRetry(bookID, ws);
                        return;
                    }
                    settleKindle(bookID, {
                        status: parsed.status as KindleReply['status'],
                        code: typeof parsed.code === 'string' ? parsed.code : undefined,
                    });
                    return;
                }
                if (typeof parsed.topic === 'string') {
                    handlersRef.current.get(parsed.topic)?.forEach((handler) => handler(parsed));
                }
            };

            ws.onerror = (error) => {
                console.error('WebSocket encountered an error:', error);
            };

            ws.onclose = () => {
                connecting = false;
                openRef.current = false;
                // A reply cannot arrive on a connection that is gone. The
                // server may still have sent the book; the reader is told the
                // answer was lost, not that it failed.
                settleKindle(null, { status: 'error', code: 'connection_lost' });
                if (wsRef.current === ws) {
                    wsRef.current = null;
                }
                setIsConnected(false);
                if (tornDown) {
                    return;
                }

                // One authoritative schedule per close; whatever the probe
                // concludes, the retry obeys the same backoff.
                scheduleReconnect();

                if (opened) {
                    // A dropped live connection is a transport problem: keep
                    // trying, no questions asked.
                    return;
                }

                // Close-before-open is ambiguous: a refused upgrade (expired
                // session) looks exactly like an outage from here. Ask the
                // read-only self-user endpoint — at most once per backoff
                // step. The transport refreshes once and replays on 401; a
                // second 401 runs the app's confirmed-logout path, which
                // flips isAuthenticated and stops this effect. Every other
                // outcome just keeps the capped backoff.
                void getCurrentUser().catch(() => {
                    // Outage or dead session: indistinguishable from here,
                    // and either way the timer above owns the next attempt.
                });
            };
        };
        connect();

        return () => {
            tornDown = true;
            clearReconnectTimer();
            const ws = wsRef.current;
            if (ws) {
                // Intentional close: no reconnect from this socket's onclose.
                ws.onclose = null;
                ws.close();
            }
            wsRef.current = null;
            openRef.current = false;
            settleKindle(null, { status: 'error', code: 'connection_lost' }, true);
            setIsConnected(false);
        };
    }, [
        isAuthenticated,
        handleConversionReply,
        onReconnect,
        settleKindle,
        renewAndRetry,
        resendKindle,
    ]);

    // Conversion requests ride the same socket, one per newly added book.
    useEffect(() => {
        if (!isConnected) {
            return;
        }
        const lastBook = state.convertingBooks[state.convertingBooks.length - 1];
        if (lastBook) {
            sendFrame({ type: 'convert', bookID: lastBook.bookID, format: lastBook.format });
        }
    }, [state.convertingBooks, isConnected, sendFrame]);

    const subscribe = useCallback(
        (topic: string, handler: WSMessageHandler) => {
            const handlers = handlersRef.current;
            let set = handlers.get(topic);
            const isFirst = !set || set.size === 0;
            if (!set) {
                set = new Set();
                handlers.set(topic, set);
            }
            set.add(handler);
            if (isFirst) {
                sendFrame({ type: 'subscribe', topic });
            }
            return () => {
                const current = handlers.get(topic);
                if (!current) {
                    return;
                }
                current.delete(handler);
                if (current.size === 0) {
                    handlers.delete(topic);
                    sendFrame({ type: 'unsubscribe', topic });
                }
            };
        },
        [sendFrame],
    );

    const sendToKindle = useCallback(
        (bookID: number) =>
            new Promise<KindleReply>((resolve) => {
                if (!wsRef.current || !openRef.current) {
                    resolve({ status: 'error', code: 'unavailable' });
                    return;
                }
                // A book already asked for is not asked for again: the later
                // caller waits on the same answer. A second frame would have
                // the server reply in_progress, and that reply — answering
                // every waiter on the book — settled the first request too,
                // before its real outcome came.
                const waiters = kindleWaitersRef.current;
                const pending = waiters.get(bookID);
                if (pending) {
                    pending.push(resolve);
                    return;
                }
                waiters.set(bookID, [resolve]);
                sendFrame({ type: 'kindle', bookID });
            }),
        [sendFrame],
    );

    const value = useMemo(
        () => ({ isConnected, subscribe, sendToKindle }),
        [isConnected, subscribe, sendToKindle],
    );

    return <WebSocketContext.Provider value={value}>{children}</WebSocketContext.Provider>;
};
