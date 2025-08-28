import axios from 'axios';

const API_URL = process.env.REACT_APP_API_URL;
const WS_URL = process.env.REACT_APP_WS_URL;

// Store CSRF token
let csrfToken: string | null = null;

const axiosInstance = axios.create({
    baseURL: `${API_URL}/api`,
    headers: {
        'Content-Type': 'application/json',
    },
    withCredentials: true, // Include credentials with every request
});

// Request interceptor to add CSRF token
axiosInstance.interceptors.request.use(
    (config) => {
        // Add CSRF token to POST, PUT, DELETE requests
        if (['post', 'put', 'delete', 'patch'].includes(config.method?.toLowerCase() || '')) {
            if (csrfToken) {
                config.headers['X-CSRF-Token'] = csrfToken;
            }
        }
        return config;
    },
    (error) => Promise.reject(error)
);

// Response interceptor to handle token refresh
axiosInstance.interceptors.response.use(
    (response) => response,
    async (error) => {
        const originalRequest = error.config;

        // If 401 and not already retrying, try to refresh token
        if (error.response?.status === 401 && !originalRequest._retry) {
            originalRequest._retry = true;

            // Don't try to refresh if we're already on login page or making login/auth requests
            const currentPath = window.location.pathname;
            const isAuthPage = currentPath.includes('/login') ||
                              currentPath.includes('/register') ||
                              currentPath.includes('/forgot-password') ||
                              currentPath.includes('/activation');

            const isAuthRequest = originalRequest.url?.includes('/login') ||
                                 originalRequest.url?.includes('/refresh-token') ||
                                 originalRequest.url?.includes('/csrf-token');

            if (!isAuthPage && !isAuthRequest) {
                try {
                    // Try to refresh token
                    const refreshResponse = await axios.post(`${API_URL}/api/refresh-token`, {}, {
                        withCredentials: true
                    });

                    if (refreshResponse.status === 200) {
                        // Retry original request
                        return axiosInstance(originalRequest);
                    }
                } catch (refreshError) {
                    // Refresh failed, redirect to login only if not already on auth page
                    if (!isAuthPage) {
                        window.location.href = '/login';
                    }
                    return Promise.reject(refreshError);
                }
            }
        }

        if (error.response?.status === 404) {
            window.location.href = '/404';
        }

        return Promise.reject(error);
    }
);

// Function to set CSRF token
export const setCsrfToken = (token: string) => {
    csrfToken = token;
};

// Function to get CSRF token
export const getCsrfToken = async () => {
    try {
        const response = await axiosInstance.get('/csrf-token');
        if (response.data.csrf_token) {
            setCsrfToken(response.data.csrf_token);
            return response.data.csrf_token;
        }
    } catch (error) {
        console.error('Failed to get CSRF token:', error);
    }
    return null;
};

export { API_URL, axiosInstance as fetchWithAuth };
export { WS_URL };