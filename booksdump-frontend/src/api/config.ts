import axios from 'axios';

const API_URL = process.env.REACT_APP_API_URL;

const axiosInstance = axios.create({
    baseURL: `${API_URL}/api`,
    headers: {
        'Content-Type': 'application/json',
    },
    withCredentials: true, // Include credentials with every request
});

axiosInstance.interceptors.response.use(
    (response) => response,
    (error) => {
        if (error.response.status === 404) {
            window.location.href = '/404';
        }
        return Promise.reject(error);
    }
);

export { API_URL, axiosInstance as fetchWithAuth };