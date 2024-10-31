import { useEffect, useRef, useState } from 'react';
import { WS_URL, API_URL } from '../../api/config';
import { useBookConversion } from '../../context/BookConversionContext';

const OpPing = 0x9;
const OpPong = 0xa;

function useAuthWebSocket(endpoint: string, isAuthenticated: boolean) {
    const [isConnected, setIsConnected] = useState(false);
    const [reconnectAttempt, setReconnectAttempt] = useState(0);
    const wsRef = useRef<WebSocket | null>(null);
    const reconnectIntervalRef = useRef<NodeJS.Timeout | null>(null);
    const { state, dispatch } = useBookConversion();

    const setupWebSocket = () => {
        if (!isAuthenticated) return;

        const fullUrl = `${WS_URL}${endpoint}`;
        const ws = new WebSocket(fullUrl);
        wsRef.current = ws;

        ws.onopen = () => {
            setIsConnected(true);
            console.log("WebSocket connection established.");
            setReconnectAttempt(0); // Reset reconnect attempt counter
        };

        ws.onmessage = (event) => {
            const data = event.data;
            if (data instanceof ArrayBuffer) {
                const view = new DataView(data);
                const opcode = view.getUint8(0);
                if (opcode === OpPing) {
                    if (wsRef.current) {
                        wsRef.current.send(new Uint8Array([OpPong]));
                        console.log("Sent pong message to WebSocket");
                    }
                    return;
                }
            }

            try {
                const bookID = parseInt(data, 10);
                console.log(`Received message via WebSocket - Book ID: ${bookID}`);

                dispatch({ type: 'REMOVE_CONVERTING_BOOK', payload: { bookID, format: 'mobi' } });

                const downloadUrl = `${API_URL}/api/files/books/conversion/${bookID}`;
                const iframe = document.createElement('iframe');
                iframe.style.display = 'none';
                iframe.src = downloadUrl;
                document.body.appendChild(iframe);

                iframe.onload = () => {
                    document.body.removeChild(iframe);
                };
            } catch (error) {
                console.error("Error parsing WebSocket message:", data, error);
            }
        };

        ws.onerror = (error) => {
            console.error("WebSocket encountered an error:", error);
        };

        ws.onclose = () => {
            console.log("WebSocket connection closed.");
            setIsConnected(false);

            // Attempt to reconnect if the user is still authenticated
            if (isAuthenticated) {
                setReconnectAttempt((prev) => prev + 1);
            }
        };
    };

    useEffect(() => {
        if (isAuthenticated) {
            setupWebSocket();
        }

        return () => {
            if (wsRef.current) {
                wsRef.current.close();
                wsRef.current = null;
            }
            if (reconnectIntervalRef.current) {
                clearInterval(reconnectIntervalRef.current);
            }
        };
    }, [endpoint, isAuthenticated, dispatch]);

    useEffect(() => {
        if (!isConnected && isAuthenticated && reconnectAttempt > 0) {
            reconnectIntervalRef.current = setTimeout(() => {
                console.log(`Attempting to reconnect (Attempt ${reconnectAttempt})...`);
                setupWebSocket();
            }, Math.min(reconnectAttempt * 1000, 10000)); // Maximum 10 seconds
        }

        return () => {
            if (reconnectIntervalRef.current) {
                clearTimeout(reconnectIntervalRef.current);
            }
        };
    }, [isConnected, isAuthenticated, reconnectAttempt]);

    useEffect(() => {
        if (isConnected && wsRef.current) {
            const lastBook = state.convertingBooks[state.convertingBooks.length - 1];
            if (lastBook) {
                wsRef.current.send(JSON.stringify({ bookID: lastBook.bookID, format: lastBook.format }));
                console.log(`Sent book ID ${lastBook.bookID} to WebSocket`);
            }
        }
    }, [state.convertingBooks, isConnected]);

    return { isConnected };
}

export default useAuthWebSocket;
