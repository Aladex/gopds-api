import axios from 'axios';
import { getToken, removeToken } from '../context/AuthContext';

const API_URL = process.env.REACT_APP_API_URL;

const axiosInstance = axios.create({
    baseURL: `${API_URL}/api`,
    headers: {
        'Content-Type': 'application/json',
    },
});

axiosInstance.interceptors.request.use((config) => {
    const token = getToken();
    if (token) {
        config.headers.Authorization = `${token}`;
    }
    return config;
}, (error) => {
    return Promise.reject(error);
});

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
