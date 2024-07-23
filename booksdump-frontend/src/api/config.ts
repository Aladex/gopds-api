// src/api/config.ts
import { removeToken } from '../services/authService';

const API_URL = process.env.REACT_APP_API_URL;

export const fetchWithAuth = async (url: string, options = {}) => {
    const token = localStorage.getItem('token');
    const response = await fetch(`${API_URL}/api${url}`, {
        ...options,
        headers: {
            Authorization: `${token}`,
        },
    });

    if (response.status === 401) {
        removeToken();
        window.location.href = '/login';
    }

    return response;
};

export { API_URL };