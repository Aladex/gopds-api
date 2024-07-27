// src/components/PublicRoute.tsx
import React from 'react';
import { Outlet, Navigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';

const PublicRoute: React.FC = () => {
    const { isAuthenticated } = useAuth();

    if (isAuthenticated) {
        return <Navigate to="/books/page/1" replace />;
    }

    return <Outlet />;
};

export default PublicRoute;