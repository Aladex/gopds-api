// src/components/PrivateRoute.tsx
import React from 'react';
import { Navigate, Route } from 'react-router-dom';
import { useAuth } from './AuthContext';

const PrivateRoute: React.FC<{ element: React.ReactNode }> = ({ element }) => {
    const { isAuthenticated } = useAuth();

    return isAuthenticated ? <>{element}</> : <Navigate to="/login" />;
};

export default PrivateRoute;
