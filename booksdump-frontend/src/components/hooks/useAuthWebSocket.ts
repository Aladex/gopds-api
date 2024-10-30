import { useEffect, useRef, useState } from 'react';
import { WS_URL } from '../../api/config';
import { useBookConversion } from '../../context/BookConversionContext';

function useAuthWebSocket(endpoint: string) {
    const [isConnected, setIsConnected] = useState(false);
    const wsRef = useRef<WebSocket | null>(null);
    const { dispatch } = useBookConversion();

    useEffect(() => {
        // Check for authentication token in cookies
        const cookies = document.cookie;
        if (!cookies.includes("token")) {
            console.warn("User is not authenticated. WebSocket connection is not established.");
            return;
        }

        // Establish WebSocket connection
        const fullUrl = `${WS_URL}${endpoint}`;
        const ws = new WebSocket(fullUrl);
        wsRef.current = ws;

        ws.onopen = () => {
            setIsConnected(true);
            console.log("WebSocket connection established.");
        };

        ws.onmessage = (event) => {
            try {
                const bookID = parseInt(event.data, 10);
                console.log(`Received message via WebSocket - Book ID: ${bookID}`);
                dispatch({ type: 'REMOVE_CONVERTING_BOOK', payload: { bookID, format: 'mobi' } });
            } catch (error) {
                console.error("Error parsing WebSocket message:", event.data, error);
            }
        };

        ws.onerror = (error) => {
            console.error("WebSocket encountered an error:", error);
        };

        ws.onclose = () => {
            setIsConnected(false);
            console.log("WebSocket connection closed.");
        };

        // Cleanup function: close WebSocket connection on component unmount
        return () => {
            ws.close();
        };
    }, [endpoint, dispatch]);

    return { isConnected };
}

export default useAuthWebSocket;
