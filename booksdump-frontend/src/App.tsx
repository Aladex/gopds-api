// src/App.tsx
import React from 'react';
import {BrowserRouter as Router, Routes, Route, Navigate} from 'react-router-dom';
import {ThemeProvider} from '@mui/material/styles';
import CssBaseline from '@mui/material/CssBaseline';
import theme from './theme';
import {AuthProvider} from './context/AuthContext';
import {AuthorProvider} from './context/AuthorContext';
import {FavProvider} from "./context/FavContext";
import {SearchBarProvider} from './context/SearchBarContext';
import publicRoutes from './routes/publicRoutes';
import privateRoutes from './routes/privateRoutes';

const App: React.FC = () => {
    return (
        <ThemeProvider theme={theme}>
            <CssBaseline/>
            <Router>
                <Routes>
                    <Route path="/" element={<Navigate to="/books/page/1"/>}/>
                    {publicRoutes}
                    {privateRoutes}
                    {/* Redirect unknown paths */}
                    <Route path="*" element={<Navigate to="/"/>}/>
                </Routes>
            </Router>
        </ThemeProvider>
    );
};

const AppWrapper: React.FC = () => (
    <AuthProvider>
        <FavProvider>
            <AuthorProvider>
                <SearchBarProvider>
                    <App/>
                </SearchBarProvider>
            </AuthorProvider>
        </FavProvider>
    </AuthProvider>
);

export default AppWrapper;
