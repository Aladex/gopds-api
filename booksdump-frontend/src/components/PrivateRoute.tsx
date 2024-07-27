// src/components/PrivateRoute.tsx
import React from 'react';
import { Navigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import Header from './common/Header';
import Footer from "./common/Footer";

const PrivateRoute: React.FC<{ children: React.ReactNode, requireSuperuser?: boolean }> = ({ children, requireSuperuser = false }) => {
    const { isAuthenticated, user } = useAuth();

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
};

export default PrivateRoute;