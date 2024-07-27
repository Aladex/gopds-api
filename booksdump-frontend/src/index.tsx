import React from 'react';
import ReactDOM from 'react-dom/client';
import './index.css';
import AppWrapper from './App';
import reportWebVitals from './reportWebVitals';
import { I18nextProvider } from 'react-i18next';
import i18n from './i18n'; // Adjust the path according to where your i18n configuration file is located
import { BrowserRouter as Router } from 'react-router-dom';
import { AuthProvider } from './context/AuthContext';

const root = ReactDOM.createRoot(document.getElementById('root') as HTMLElement);
root.render(
    <Router>
        <AuthProvider>
            <I18nextProvider i18n={i18n}>
                <AppWrapper />
            </I18nextProvider>
        </AuthProvider>
    </Router>
);

reportWebVitals();