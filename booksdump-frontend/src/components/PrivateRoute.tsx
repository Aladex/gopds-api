// src/components/PrivateRoute.tsx
import React, { memo } from 'react';
import { Navigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import Header from './common/Header';
import Footer from "./common/Footer";
import LoadingSpinner from './common/LoadingSpinner';

const PrivateRoute: React.FC<{ children: React.ReactNode, requireSuperuser?: boolean }> = memo(({ children, requireSuperuser = false }) => {
    const { isAuthenticated, user, isLoaded, isLoading } = useAuth();

    // Show loading spinner while checking authentication
    if (!isLoaded || isLoading) {
        return <LoadingSpinner message="loading" />;
    }

    if (!isAuthenticated) {
        return <Navigate to="/login" />;
    }

    if (requireSuperuser && !user?.is_superuser) {
        return <Navigate to="/404" />;
    }

    return (
        <>
            <Header />
            {children}
            <Footer />
        </>
    );
});

PrivateRoute.displayName = 'PrivateRoute';

export default PrivateRoute;