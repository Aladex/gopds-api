import React from 'react';
import ReactDOM from 'react-dom/client';
import './index.css';
import AppWrapper from './App';
import reportWebVitals from './reportWebVitals';
import { I18nextProvider } from 'react-i18next';
import i18n from './i18n'; // Adjust the path according to where your i18n configuration file is located
import { BrowserRouter as Router } from 'react-router-dom';
import { AuthProvider } from './context/AuthContext';
import { ThemeProvider } from './context/ThemeContext';

const root = ReactDOM.createRoot(document.getElementById('root') as HTMLElement);

// Убираем StrictMode для production, оставляем только для development
const AppContent = (
    <Router>
        <AuthProvider>
            <ThemeProvider>
                <I18nextProvider i18n={i18n}>
                    <AppWrapper />
                </I18nextProvider>
            </ThemeProvider>
        </AuthProvider>
    </Router>
);

root.render(
    process.env.NODE_ENV === 'development' ? (
        <React.StrictMode>
            {AppContent}
        </React.StrictMode>
    ) : (
        AppContent
    )
);

reportWebVitals();
