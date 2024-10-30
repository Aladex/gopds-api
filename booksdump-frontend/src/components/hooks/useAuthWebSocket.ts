import { useEffect, useRef, useState } from 'react';
import { API_URL } from '../../api/config';

function useAuthWebSocket(endpoint: string) {
    const [isConnected, setIsConnected] = useState(false);
    const [reconnectAttempts, setReconnectAttempts] = useState(0);
    const wsRef = useRef<WebSocket | null>(null);

    useEffect(() => {
        let timeoutId: NodeJS.Timeout;

        const connectWebSocket = () => {
            if (!document.cookie.includes("sessionCookie")) {
                console.warn("User is not authenticated. WebSocket connection is not established.");
                return;
            }

            const ws = new WebSocket(`${API_URL}${endpoint}`);
            wsRef.current = ws;

            ws.onopen = () => {
                setIsConnected(true);
                setReconnectAttempts(0);
                console.log("WebSocket is connected.");
            };

            ws.onmessage = (event) => {
                const bookID = event.data;
                console.log(`Book ID received via WebSocket: ${bookID}`);
                window.location.href = `${API_URL}/api/books/download/${bookID}`;
            };

            ws.onerror = (error) => {
                console.error("WebSocket error:", error);
            };

            ws.onclose = () => {
                setIsConnected(false);
                console.log("WebSocket closed. Attempting to reconnect...");

                timeoutId = setTimeout(() => {
                    setReconnectAttempts((prev) => prev + 1);
                    connectWebSocket();
                }, Math.min(1000 * 2 ** reconnectAttempts, 30000)); // до 30 секунд
            };
        };

        connectWebSocket();

        return () => {
            if (wsRef.current) wsRef.current.close();
            clearTimeout(timeoutId);
        };
    }, [endpoint, reconnectAttempts]);

    return { isConnected };
}

export default useAuthWebSocket;
