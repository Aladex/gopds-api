import { useEffect, useRef, useState } from 'react';

function useAuthWebSocket(url: string) {
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

            const ws = new WebSocket(url);
            wsRef.current = ws;

            ws.onopen = () => {
                setIsConnected(true);
                setReconnectAttempts(0);
                console.log("WebSocket is connected.");
            };

            ws.onmessage = (event) => {
                console.log("Получено сообщение:", event.data);
                // Обработка сообщений
            };

            ws.onerror = (error) => {
                console.error("Error:", error);
            };

            ws.onclose = () => {
                setIsConnected(false);
                console.log("WebSocket is closed. Trying to reconnect...");

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
    }, [url, reconnectAttempts]);

    return { isConnected };
}

export default useAuthWebSocket;
