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
import { useBookConversion } from '@/context/BookConversionContext';

// One frame from the server: a topic event or a per-connection reply.
export interface WSMessage {
    type: string;
    topic?: string;
    data?: unknown;
    [key: string]: unknown;
}

export type WSMessageHandler = (message: WSMessage) => void;

interface WebSocketContextType {
    isConnected: boolean;
    // subscribe registers a topic handler and returns the unsubscribe
    // function; components call it from an effect so unmount unsubscribes.
    subscribe: (topic: string, handler: WSMessageHandler) => () => void;
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
    const { state, dispatch } = useBookConversion();

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
            setIsConnected(false);
        };
    }, [isAuthenticated, handleConversionReply, onReconnect]);

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

    const value = useMemo(() => ({ isConnected, subscribe }), [isConnected, subscribe]);

    return <WebSocketContext.Provider value={value}>{children}</WebSocketContext.Provider>;
};
