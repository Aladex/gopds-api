import { useEffect, useRef, useState, useCallback } from 'react';
import { WS_URL, API_URL } from '../../api/config';
import { useBookConversion } from '../../context/BookConversionContext';

function useAuthWebSocket(endpoint: string) {
    const [isConnected, setIsConnected] = useState(false);
    const wsRef = useRef<WebSocket | null>(null);
    const { dispatch } = useBookConversion();

    const connectWebSocket = useCallback(() => {
        const cookies = document.cookie;
        if (!cookies.includes("token")) {
            console.warn("User is not authenticated. WebSocket connection is not established.");
            return;
        }

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

                // Убираем книгу из списка конвертации
                dispatch({ type: 'REMOVE_CONVERTING_BOOK', payload: { bookID, format: 'mobi' } });

                // Инициируем скачивание файла через iframe
                const downloadUrl = `${API_URL}/api/files/books/conversion/${bookID}`;
                const iframe = document.createElement('iframe');
                iframe.style.display = 'none';
                iframe.src = downloadUrl;
                document.body.appendChild(iframe);

                // Удаляем `iframe` после завершения скачивания
                iframe.onload = () => {
                    document.body.removeChild(iframe);
                };
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
    }, [dispatch, endpoint]);

    useEffect(() => {
        connectWebSocket();

        return () => {
            wsRef.current?.close();
        };
    }, [connectWebSocket]);

    return { isConnected };
}

export default useAuthWebSocket;
