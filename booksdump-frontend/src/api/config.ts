import axios from 'axios';

const API_URL = process.env.REACT_APP_API_URL;

const axiosInstance = axios.create({
    baseURL: `${API_URL}/api`,
    headers: {
        'Content-Type': 'application/json',
    },
    withCredentials: true, // Include credentials with every request
});

const publicRoutesList = [
    '/login',
    '/registration',
    '/forgot-password',
    '/404',
];

const isPublicRoute = (path: string): boolean => {
    return publicRoutesList.some((route) => {
        if (route === '*') {
            return true;
        }
        return path.includes(route);
    });
};

axiosInstance.interceptors.response.use(
    (response) => response,
    (error) => {
        const currentPath = window.location.pathname;
        if (error.response.status === 401 && !isPublicRoute(currentPath)) {
            window.location.href = '/login';
        } else if (error.response.status === 404) {
            window.location.href = '/404';
        }
        return Promise.reject(error);
    }
);

export { API_URL, axiosInstance as fetchWithAuth };