// src/components/PrivateRoute.tsx
import React from 'react';
import { Navigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import Header from './common/Header'

const PrivateRoute: React.FC<{ element: React.ReactNode }> = ({ element }) => {
    const { isAuthenticated } = useAuth();

    return isAuthenticated ? (
        <>
            <Header />
            {element}
        </>
    ) : (
        <Navigate to="/login" />
    );
};

export default PrivateRoute;
