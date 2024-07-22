// src/api/config.ts
import { removeToken } from '../services/authService';

const API_URL = process.env.REACT_APP_API_URL;
const APP_URL = process.env.REACT_APP_URL;

export const fetchWithAuth = async (url: string, options = {}) => {
    const token = localStorage.getItem('token'); // Получаем токен из localStorage
    const response = await fetch(`${API_URL}${url}`, {
        ...options,
        headers: {
            Authorization: `${token}`,
        },
    });

    if (response.status === 403) {
        removeToken(); // Удаляем токен из localStorage
        window.location.href = '/login'; // Перенаправляем на страницу логина
    }

    return response;
};

export { API_URL, APP_URL };