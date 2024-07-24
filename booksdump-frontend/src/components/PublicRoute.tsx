import React from 'react';
import { Route, Navigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import Login from '../components/Auth/Login';

const PublicRoutes: React.FC = () => {
    const { isAuthenticated } = useAuth();

    if (isAuthenticated) {
        return <Navigate to="/books/page/1" replace />;
    } else {
        return <Route path="/login" element={<Login />} />;
    }
};

export default PublicRoutes;