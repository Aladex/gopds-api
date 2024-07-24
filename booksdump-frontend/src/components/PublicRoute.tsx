import React from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import Login from '../components/Auth/Login';

const PublicRoutes: React.FC = () => {
    const { isAuthenticated } = useAuth();

    if (isAuthenticated) {
        return <Navigate to="/books/page/1" replace />;
    } else {
        return (
            <Routes>
                <Route path="/login" element={<Login />} />
            </Routes>
        );
    }
};

export default PublicRoutes;