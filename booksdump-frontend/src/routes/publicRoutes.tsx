// src/routes/publicRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import PublicLayout from '../layouts/PublicLayout';
import Login from '../components/Auth/Login';
import Registration from '../components/Auth/Registration';
import ForgotPassword from '../components/Auth/ForgotPassword';

const publicRoutes = (
    <Route path="/" element={<PublicLayout />}>
        <Route path="login" element={<Login />} />
        <Route path="register" element={<Registration />} />
        <Route path="forgot-password" element={<ForgotPassword />} />
    </Route>
);

export default publicRoutes;
