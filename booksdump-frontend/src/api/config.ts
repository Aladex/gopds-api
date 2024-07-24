import axios from 'axios';
import { removeToken } from '../services/authService';

const API_URL = process.env.REACT_APP_API_URL;

const axiosInstance = axios.create({
    baseURL: `${API_URL}/api`,
    headers: {
        'Content-Type': 'application/json',
    },
});

// Добавляем токен к каждому запросу
axiosInstance.interceptors.request.use((config) => {
    const token = localStorage.getItem('token');
    if (token) {
        config.headers.Authorization = `${token}`;
    }
    return config;
}, (error) => {
    return Promise.reject(error);
});

// Обрабатываем ошибки авторизации и 404
axiosInstance.interceptors.response.use((response) => {
    return response;
}, (error) => {
    if (error.response.status === 401) {
        removeToken();
        window.location.href = '/login';
    } else if (error.response.status === 404) {
        window.location.href = '/404';
    }
    return Promise.reject(error);
});

export { API_URL, axiosInstance as fetchWithAuth };
