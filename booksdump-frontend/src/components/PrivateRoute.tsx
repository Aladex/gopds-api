import React from 'react';
import { Navigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import Header from './common/Header';

const PrivateRoute: React.FC<{ children: React.ReactNode }> = ({ children }) => {
    const { isAuthenticated } = useAuth();

    return isAuthenticated ? (
        <>
            <Header />
            {children}
        </>
    ) : (
        <Navigate to="/login" />
    );
};

export default PrivateRoute;